package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ─── Hermetic test setup ─────────────────────────────────────────────────────

type invoiceCtx struct {
	router   *gin.Engine
	db       *gorm.DB
	userID   int
	tenantID string
	cleanup  func()
}

func setupInvoiceRouter(t *testing.T) *invoiceCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:inv%d?mode=memory&cache=shared", invoiceDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &repo.Log{}, &repo.Option{},
	} {
		if err := db.AutoMigrate(tbl); err != nil {
			// SQLite shares index names across tables — "already exists" is harmless.
			t.Logf("auto migrate warning: %v", err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	user := &repo.User{
		Username:    "inv-tester",
		DisplayName: "Invoice Tester",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "inv@test.local",
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	tenantID := fmt.Sprintf("tenant-inv-%d", invoiceDBCounter.Load())

	ctx := &invoiceCtx{
		db:       db,
		userID:   user.Id,
		tenantID: tenantID,
	}

	router := gin.New()
	mockAuth := func(c *gin.Context) {
		c.Set("tenant_context", &middleware.TenantContext{
			TenantID: ctx.tenantID,
			UserID:   ctx.userID,
		})
		c.Next()
	}
	router.GET("/api/v2/:tenant_slug/billing/invoices", mockAuth, ListInvoicesV2)
	ctx.router = router

	ctx.cleanup = func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	t.Cleanup(ctx.cleanup)
	return ctx
}

// invoiceDBCounter is package-level so each test gets a unique in-memory DB.
var invoiceDBCounter invoiceCounterType

type invoiceCounterType struct{ v int64 }

func (c *invoiceCounterType) Add(n int64) int64 { c.v += n; return c.v }
func (c *invoiceCounterType) Load() int64       { return c.v }

func getInvoices(ctx *invoiceCtx, query string) *httptest.ResponseRecorder {
	path := "/api/v2/" + ctx.tenantID + "/billing/invoices"
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	ctx.router.ServeHTTP(w, req)
	return w
}

func parseInvoiceResp(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("parse body: %v — raw: %s", err, w.Body.String())
	}
	return out
}

// seedLog inserts a LogTypeConsume row with the given quota and unix timestamp.
func seedLog(t *testing.T, ctx *invoiceCtx, quota int, createdAt int64) {
	t.Helper()
	l := &repo.Log{
		UserId:    ctx.userID,
		TenantId:  ctx.tenantID,
		Type:      repo.LogTypeConsume,
		Quota:     quota,
		CreatedAt: createdAt,
	}
	if err := ctx.db.Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

// seedLogWithOther inserts a LogTypeConsume row with the given quota, unix
// timestamp and raw Other JSON string. Unlike RecordConsumeLog's own
// json.Marshal, the caller controls the exact bytes under test — used to
// pin repo.BillableConsumePredicate's LIKE patterns against the exact
// substrings settlement_outcome.go and channel-test.go write (see
// repo.BillableConsumePredicate's doc comment for why this package cannot
// import either of those to derive the string instead).
func seedLogWithOther(t *testing.T, ctx *invoiceCtx, quota int, createdAt int64, other string) {
	t.Helper()
	l := &repo.Log{
		UserId:    ctx.userID,
		TenantId:  ctx.tenantID,
		Type:      repo.LogTypeConsume,
		Quota:     quota,
		CreatedAt: createdAt,
		Other:     other,
	}
	if err := ctx.db.Create(l).Error; err != nil {
		t.Fatalf("seed log with other: %v", err)
	}
}

// monthStart returns the unix timestamp for the first second of YYYY-MM in UTC.
func monthStart(year int, month time.Month) int64 {
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Unix()
}

// ─── Tests ───────────────────────────────────────────────────────────────────

// TestV2Billing_InvoicesHappyPath inserts 6 consume logs spread across 3
// calendar months and expects the response to contain 3 buckets with correct
// quota sums and a non-zero amount_cny.
func TestV2Billing_InvoicesHappyPath(t *testing.T) {
	ctx := setupInvoiceRouter(t)

	// 2 logs in 2026-01 (quota 1000 + 2000 = 3000)
	seedLog(t, ctx, 1000, monthStart(2026, time.January)+100)
	seedLog(t, ctx, 2000, monthStart(2026, time.January)+200)
	// 1 log in 2026-02 (quota 500)
	seedLog(t, ctx, 500, monthStart(2026, time.February)+100)
	// 3 logs in 2026-03 (quota 100+200+300 = 600)
	seedLog(t, ctx, 100, monthStart(2026, time.March)+100)
	seedLog(t, ctx, 200, monthStart(2026, time.March)+200)
	seedLog(t, ctx, 300, monthStart(2026, time.March)+300)

	w := getInvoices(ctx, "from=2026-01&to=2026-03")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parseInvoiceResp(t, w)
	if resp["success"] != true {
		t.Fatalf("success != true: %v", resp)
	}
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("items len = %d, want 3", len(items))
	}

	// Build a map month → quota_sum for assertion.
	quotaByMonth := make(map[string]float64)
	for _, it := range items {
		m := it.(map[string]interface{})
		quotaByMonth[m["month"].(string)] = m["quota"].(float64)
	}

	if quotaByMonth["2026-01"] != 3000 {
		t.Errorf("2026-01 quota = %v, want 3000", quotaByMonth["2026-01"])
	}
	if quotaByMonth["2026-02"] != 500 {
		t.Errorf("2026-02 quota = %v, want 500", quotaByMonth["2026-02"])
	}
	if quotaByMonth["2026-03"] != 600 {
		t.Errorf("2026-03 quota = %v, want 600", quotaByMonth["2026-03"])
	}

	// Sanity-check amount_cny is non-zero when quota > 0.
	for _, it := range items {
		m := it.(map[string]interface{})
		if m["quota"].(float64) > 0 && m["amount_cny"].(float64) <= 0 {
			t.Errorf("month %s: quota %v > 0 but amount_cny %v <= 0", m["month"], m["quota"], m["amount_cny"])
		}
	}

	// total_invoices must match items count.
	if total := data["total_invoices"].(float64); total != 3 {
		t.Errorf("total_invoices = %v, want 3", total)
	}
}

// TestV2Billing_EmptyInvoices expects an empty items slice and zero totals
// when there are no consume logs for the tenant/user.
func TestV2Billing_EmptyInvoices(t *testing.T) {
	ctx := setupInvoiceRouter(t)

	w := getInvoices(ctx, "from=2025-01&to=2025-06")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	resp := parseInvoiceResp(t, w)
	if resp["success"] != true {
		t.Fatalf("success != true: %v", resp)
	}
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("items len = %d, want 0", len(items))
	}
	if total := data["total_invoices"].(float64); total != 0 {
		t.Errorf("total_invoices = %v, want 0", total)
	}
	summary := data["summary"].(map[string]interface{})
	if summary["total_quota"].(float64) != 0 {
		t.Errorf("summary.total_quota = %v, want 0", summary["total_quota"])
	}
}

// TestV2Billing_TenantIsolation seeds logs for two distinct tenant IDs in the
// same DB and verifies each tenant's invoices endpoint returns only its own rows.
// Both queries share a single setupInvoiceRouter context (one DB, one global
// pointer) so there are no global-overwrite races between the two assertions.
func TestV2Billing_TenantIsolation(t *testing.T) {
	ctx := setupInvoiceRouter(t)

	tenantA := ctx.tenantID
	tenantB := tenantA + "-b"

	// Seed two users — one per tenant.
	userB := &repo.User{
		Username: "inv-tester-b", DisplayName: "Tester B",
		Role:   common.RoleCommonUser,
		Status: common.UserStatusEnabled,
		Email:  "invb@test.local",
	}
	if err := ctx.db.Create(userB).Error; err != nil {
		t.Fatalf("seed userB: %v", err)
	}

	// Log under tenantA / ctx.userID.
	ctx.db.Create(&repo.Log{
		UserId:    ctx.userID,
		TenantId:  tenantA,
		Type:      repo.LogTypeConsume,
		Quota:     9999,
		CreatedAt: monthStart(2026, time.January) + 50,
	})

	// Log under tenantB / userB.
	ctx.db.Create(&repo.Log{
		UserId:    userB.Id,
		TenantId:  tenantB,
		Type:      repo.LogTypeConsume,
		Quota:     1111,
		CreatedAt: monthStart(2026, time.January) + 50,
	})

	// Query tenantA as ctx.userID — should see only the 9999 row.
	wA := getInvoices(ctx, "from=2026-01&to=2026-01")
	if wA.Code != http.StatusOK {
		t.Fatalf("tenantA status = %d, body: %s", wA.Code, wA.Body.String())
	}
	respA := parseInvoiceResp(t, wA)
	itemsA := respA["data"].(map[string]interface{})["items"].([]interface{})
	if len(itemsA) != 1 {
		t.Fatalf("tenantA items = %d, want 1", len(itemsA))
	}
	if quotaA := itemsA[0].(map[string]interface{})["quota"].(float64); quotaA != 9999 {
		t.Errorf("tenantA quota = %v, want 9999", quotaA)
	}

	// Build a second router that uses tenantB + userB.Id as auth context.
	routerB := gin.New()
	routerB.GET("/api/v2/:tenant_slug/billing/invoices",
		func(c *gin.Context) {
			c.Set("tenant_context", &middleware.TenantContext{
				TenantID: tenantB,
				UserID:   userB.Id,
			})
			c.Next()
		},
		ListInvoicesV2,
	)

	reqB := httptest.NewRequest(http.MethodGet,
		"/api/v2/"+tenantB+"/billing/invoices?from=2026-01&to=2026-01", nil)
	wB := httptest.NewRecorder()
	routerB.ServeHTTP(wB, reqB)

	if wB.Code != http.StatusOK {
		t.Fatalf("tenantB status = %d, body: %s", wB.Code, wB.Body.String())
	}
	respB := parseInvoiceResp(t, wB)
	itemsB := respB["data"].(map[string]interface{})["items"].([]interface{})
	if len(itemsB) != 1 {
		t.Fatalf("tenantB items = %d, want 1", len(itemsB))
	}
	if quotaB := itemsB[0].(map[string]interface{})["quota"].(float64); quotaB != 1111 {
		t.Errorf("tenantB quota = %v, want 1111 (not tenantA's 9999)", quotaB)
	}
}

// TestV2Billing_InvoiceViewForbiddenFields verifies that invoiceMonthBucket
// (the view struct returned by ListInvoicesV2) does not expose raw payment
// provider IDs, internal transaction references, or tenant-scoped counters.
// invoiceMonthBucket only carries month/quota/amount_cny/request_count
// so this test confirms no raw entity fields bleed through.
func TestV2Billing_InvoiceViewForbiddenFields(t *testing.T) {
	ctx := setupInvoiceRouter(t)

	// Seed one consume log so the bucket has a row to return.
	seedLog(t, ctx, 5000, monthStart(2026, time.January)+60)

	w := getInvoices(ctx, "from=2026-01&to=2026-01")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parseInvoiceResp(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 invoice bucket, got %d", len(items))
	}
	bucket := items[0].(map[string]interface{})

	// Fields that must NOT appear in the invoice bucket view.
	for _, forbidden := range []string{
		"stripe_id", "alipay_id", "wechat_id", "payment_method_id",
		"tenant_id", "user_id", "internal_id",
	} {
		if _, ok := bucket[forbidden]; ok {
			t.Errorf("forbidden field %q leaked through invoiceMonthBucket", forbidden)
		}
	}

	// Fields that must be present (the whitelisted invoice shape).
	for _, required := range []string{"month", "quota", "amount_cny", "request_count"} {
		if _, ok := bucket[required]; !ok {
			t.Errorf("expected field %q in invoice bucket, not found", required)
		}
	}

	// amount_usd must NOT be present: a prior version emitted it as a 1:1
	// copy of amount_cny, which is off by the real CNY/USD rate (~7x) — there
	// is no FX source wired into this package. Re-adding it without a real
	// conversion would silently regress into that bug.
	if _, ok := bucket["amount_usd"]; ok {
		t.Errorf("amount_usd should not be present in the invoice bucket (no real FX source wired in); got %v", bucket["amount_usd"])
	}

	// amount_cny must be consistent with quota for the billing model.
	if quota := bucket["quota"].(float64); quota > 0 {
		if amountCNY := bucket["amount_cny"].(float64); amountCNY <= 0 {
			t.Errorf("quota=%v > 0 but amount_cny=%v <= 0 — billing calculation is wrong", quota, amountCNY)
		}
	}
}

// TestInvoiceMonths_ExcludesUnbilledRows is cycle13 L2's core oracle: a
// month with a clean row, a channel-test-probe row and a settlement-failed
// row must bill only the clean row — amount_cny/quota/request_count — and
// must surface the other two as unbilled_quota/unbilled_request_count
// instead of silently dropping them from the response (v2_billing_invoices.go
// used to SUM(quota) over every type=consume row unconditionally).
func TestInvoiceMonths_ExcludesUnbilledRows(t *testing.T) {
	ctx := setupInvoiceRouter(t)

	when := monthStart(2026, time.January) + 100
	seedLog(t, ctx, 1000, when)                                          // clean
	seedLogWithOther(t, ctx, 2000, when+10, `{"source":"channel_test"}`) // manual probe
	seedLogWithOther(t, ctx, 3000, when+20, `{"settlement":"failed"}`)   // settlement failed

	w := getInvoices(ctx, "from=2026-01&to=2026-01")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	resp := parseInvoiceResp(t, w)
	items := resp["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	bucket := items[0].(map[string]interface{})

	if quota := bucket["quota"].(float64); quota != 1000 {
		t.Errorf("quota = %v, want 1000 (only the clean row is billable)", quota)
	}
	if rc := bucket["request_count"].(float64); rc != 1 {
		t.Errorf("request_count = %v, want 1", rc)
	}
	if uq := bucket["unbilled_quota"].(float64); uq != 5000 {
		t.Errorf("unbilled_quota = %v, want 5000 (2000 probe + 3000 settlement-failed)", uq)
	}
	if urc := bucket["unbilled_request_count"].(float64); urc != 2 {
		t.Errorf("unbilled_request_count = %v, want 2", urc)
	}
	wantAmount := 1000.0 / common.QuotaPerUnit
	if amt := bucket["amount_cny"].(float64); amt != wantAmount {
		t.Errorf("amount_cny = %v, want %v (billable-only)", amt, wantAmount)
	}
}

// TestProbeChannel_RowIsUnbilled drives the real probeChannel (via
// testChannel, channel-test.go — same package, so the unexported function is
// directly reachable) against a local httptest upstream, then asserts the
// resulting consume-log row is excluded from ListInvoicesV2's billable
// bucket and counted in unbilled_quota/unbilled_request_count instead. This
// is the strong form of TestInvoiceMonths_ExcludesUnbilledRows above: the
// "source":"channel_test" marker comes from the real production write path
// (probeChannel, channel-test.go:481), not a hand-built Other string, so a
// drift between that literal and repo.BillableConsumePredicate's LIKE
// pattern shows up here even though this package cannot import repo's
// pattern to compare it directly (see BillableConsumePredicate's doc
// comment).
func TestProbeChannel_RowIsUnbilled(t *testing.T) {
	setupContextTierChannelTestDB(t) // seeds user id 1 + Channel/Log/Option tables (context_tier_channel_test_test.go)
	allowLoopbackEgress(t)
	app.InitHttpClient()

	const model = "billing-l2-probe-model"
	seedModelRatio(t, model) // channel_probe_auto_test.go — probeChannel needs a configured ratio before it will call the upstream at all

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-l2","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}`))
	}))
	defer upstream.Close()

	channel := &repo.Channel{
		Type:    1, // OpenAI
		Status:  common.ChannelStatusEnabled,
		Name:    "billing-l2-probe-channel",
		Key:     "sk-billing-l2-probe",
		Models:  model,
		Group:   "default",
		BaseURL: func() *string { u := upstream.URL; return &u }(),
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	result := testChannel(channel, model, "")
	if result.localErr != nil {
		t.Fatalf("testChannel localErr: %v", result.localErr)
	}
	if result.newAPIError != nil {
		t.Fatalf("testChannel newAPIError: %v", result.newAPIError.Error())
	}

	var probeRow repo.Log
	if err := repo.DB.Where("channel_id = ?", channel.Id).Order("id desc").First(&probeRow).Error; err != nil {
		t.Fatalf("query probe log row: %v", err)
	}
	if probeRow.Quota <= 0 {
		t.Fatalf("probe row quota = %d, want > 0 (otherwise billable vs. unbilled is not distinguishable)", probeRow.Quota)
	}
	if !strings.Contains(probeRow.Other, `"source":"channel_test"`) {
		t.Fatalf("probe row Other = %q, does not carry the channel_test marker probeChannel is supposed to write", probeRow.Other)
	}

	router := gin.New()
	router.GET("/api/v2/:tenant_slug/billing/invoices", func(c *gin.Context) {
		c.Set("tenant_context", &middleware.TenantContext{TenantID: "default", UserID: 1})
		c.Next()
	}, ListInvoicesV2)

	now := time.Now().UTC()
	month := fmt.Sprintf("%04d-%02d", now.Year(), now.Month())
	req := httptest.NewRequest(http.MethodGet, "/api/v2/default/billing/invoices?from="+month+"&to="+month, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("invoices status = %d, body: %s", w.Code, w.Body.String())
	}
	resp := parseInvoiceResp(t, w)
	items := resp["data"].(map[string]interface{})["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1 (the probe row's month)", len(items))
	}
	bucket := items[0].(map[string]interface{})
	if quota := bucket["quota"].(float64); quota != 0 {
		t.Errorf("billable quota = %v, want 0 — a channel-test probe must not be billed", quota)
	}
	if rc := bucket["request_count"].(float64); rc != 0 {
		t.Errorf("billable request_count = %v, want 0", rc)
	}
	if uq := bucket["unbilled_quota"].(float64); uq != float64(probeRow.Quota) {
		t.Errorf("unbilled_quota = %v, want %d (the probe row's own quota)", uq, probeRow.Quota)
	}
	if urc := bucket["unbilled_request_count"].(float64); urc != 1 {
		t.Errorf("unbilled_request_count = %v, want 1", urc)
	}
}
