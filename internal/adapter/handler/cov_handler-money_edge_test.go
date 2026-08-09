package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ============================================================================
// cov_handler-money_edge_test.go
//
// Targeted edge-case coverage for the money-path handlers this agent owns:
// redemption.go, switch_redeem.go, internal_credit_pool_fund.go,
// v2_billing_invoices.go, internal_api.go, internal_backfill.go,
// switch_reconciliation.go, governance.go, governance_savings.go, usedata.go.
//
// This file intentionally reuses the package's existing test scaffolding
// (SetupV2TestRouter, setAnalyticsCtx, internalRequest/parseResponse,
// hashTestKey, testApiKeyAllScopes) rather than inventing a parallel rig.
// All new helpers/types are prefixed handler_money to avoid collisions with
// other agents editing this package concurrently.
// ============================================================================

// ─── internal_backfill.go: InternalBackfillTokenAccountIDs ─────────────────
//
// This handler had ZERO test coverage before this file. It backfills
// Token.IdentityAccountID by resolving each token owner's OIDC subject
// (via UserIdentityMapping) to a platform account ID (via an HTTP call to
// IdentityServiceURL). Money-path relevance: IdentityAccountID gates which
// platform wallet a relay call bills against, so a botched backfill either
// leaves a token unbillable or (worse) attaches it to the wrong account.

var handlerMoneyBackfillDBCounter atomic.Int64

type handlerMoneyBackfillCtx struct {
	router  *gin.Engine
	db      *gorm.DB
	cleanup func()
}

func handlerMoneySetupBackfillRouter(t *testing.T) *handlerMoneyBackfillCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:backfilltest%d?mode=memory&cache=shared", handlerMoneyBackfillDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.Token{}, &repo.UserIdentityMapping{}, &repo.User{}} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLog := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	prevIdentityURL := common.IdentityServiceURL
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false

	router := gin.New()
	router.POST("/internal/admin/backfill-token-accounts", InternalBackfillTokenAccountIDs)

	cleanup := func() {
		repo.DB, repo.LOG_DB = prevDB, prevLog
		common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
		common.IdentityServiceURL = prevIdentityURL
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	return &handlerMoneyBackfillCtx{router: router, db: db, cleanup: cleanup}
}

func handlerMoneyPostBackfill(router *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/admin/backfill-token-accounts", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestInternalBackfillTokenAccountIDs_NotConfigured: with IdentityServiceURL
// unset the handler must refuse outright (400) rather than silently no-op —
// a silent no-op would look identical to "nothing needed backfilling" in
// monitoring and hide a misconfigured deploy.
func TestInternalBackfillTokenAccountIDs_NotConfigured(t *testing.T) {
	ctx := handlerMoneySetupBackfillRouter(t)
	defer ctx.cleanup()
	common.IdentityServiceURL = ""

	w := handlerMoneyPostBackfill(ctx.router)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp["error"] != "identity service not configured" {
		t.Errorf("expected explanatory error, got %v", resp["error"])
	}
}

// TestInternalBackfillTokenAccountIDs_NoTokens: an already-fully-backfilled
// (or empty) token table must report updated=0 without touching any row —
// this is also the idempotency check: running backfill a second time after
// a successful run must land here, not attempt any writes.
func TestInternalBackfillTokenAccountIDs_NoTokens(t *testing.T) {
	ctx := handlerMoneySetupBackfillRouter(t)
	defer ctx.cleanup()
	common.IdentityServiceURL = "http://127.0.0.1:1" // unreachable; must not be dialed on this path

	w := handlerMoneyPostBackfill(ctx.router)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["message"] != "no tokens to backfill" {
		t.Errorf("expected 'no tokens to backfill', got %v", resp["message"])
	}
	if int(resp["updated"].(float64)) != 0 {
		t.Errorf("expected updated=0, got %v", resp["updated"])
	}
}

// TestInternalBackfillTokenAccountIDs_NoMapping: a token whose owner has no
// UserIdentityMapping row at all must be counted (total_tokens/unique_users)
// but left unmatched — the handler must not fabricate an account ID.
func TestInternalBackfillTokenAccountIDs_NoMapping(t *testing.T) {
	ctx := handlerMoneySetupBackfillRouter(t)
	defer ctx.cleanup()
	common.IdentityServiceURL = "http://127.0.0.1:1"

	tok := &repo.Token{UserId: 501, Key: "unmapped-key", Status: common.TokenStatusEnabled}
	if err := ctx.db.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	w := handlerMoneyPostBackfill(ctx.router)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["total_tokens"].(float64)) != 1 {
		t.Errorf("total_tokens = %v, want 1", resp["total_tokens"])
	}
	if int(resp["unique_users"].(float64)) != 1 {
		t.Errorf("unique_users = %v, want 1", resp["unique_users"])
	}
	if int(resp["users_matched"].(float64)) != 0 {
		t.Errorf("users_matched = %v, want 0 (no identity mapping exists)", resp["users_matched"])
	}
	if int(resp["tokens_updated"].(float64)) != 0 {
		t.Errorf("tokens_updated = %v, want 0", resp["tokens_updated"])
	}

	var reloaded repo.Token
	if err := ctx.db.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.IdentityAccountID != 0 {
		t.Errorf("IdentityAccountID = %d, want unchanged 0", reloaded.IdentityAccountID)
	}
}

// TestInternalBackfillTokenAccountIDs_InactiveMappingExcluded: the mapping
// lookup filters on is_active=true. A disabled/soft-revoked mapping must
// not be used to resolve the account — otherwise a de-linked OIDC identity
// could get silently re-attached to billing.
func TestInternalBackfillTokenAccountIDs_InactiveMappingExcluded(t *testing.T) {
	ctx := handlerMoneySetupBackfillRouter(t)
	defer ctx.cleanup()
	common.IdentityServiceURL = "http://127.0.0.1:1"

	tok := &repo.Token{UserId: 502, Key: "inactive-mapping-key", Status: common.TokenStatusEnabled}
	if err := ctx.db.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	mapping := &repo.UserIdentityMapping{
		LurusUserID: 502,
		IDPSubject:  "sub-inactive",
		TenantID:    "default",
		IsActive:    false, // must be excluded by the "is_active = true" filter
	}
	if err := ctx.db.Create(mapping).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	w := handlerMoneyPostBackfill(ctx.router)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["users_matched"].(float64)) != 0 {
		t.Errorf("users_matched = %v, want 0 (mapping is inactive)", resp["users_matched"])
	}
}

// TestInternalBackfillTokenAccountIDs_EmptySubjectSkipped: a mapping row
// with an empty IDPSubject must be skipped (continue) rather than crashing
// the outbound lookup with an empty-subject request.
func TestInternalBackfillTokenAccountIDs_EmptySubjectSkipped(t *testing.T) {
	ctx := handlerMoneySetupBackfillRouter(t)
	defer ctx.cleanup()
	common.IdentityServiceURL = "http://127.0.0.1:1"

	tok := &repo.Token{UserId: 503, Key: "empty-subject-key", Status: common.TokenStatusEnabled}
	if err := ctx.db.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	mapping := &repo.UserIdentityMapping{
		LurusUserID: 503,
		IDPSubject:  "", // empty: handler's `if m.IDPSubject == "" { continue }`
		TenantID:    "default",
		IsActive:    true,
	}
	if err := ctx.db.Create(mapping).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	w := handlerMoneyPostBackfill(ctx.router)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["users_matched"].(float64)) != 0 {
		t.Errorf("users_matched = %v, want 0 (empty subject must be skipped)", resp["users_matched"])
	}
	if int(resp["tokens_updated"].(float64)) != 0 {
		t.Errorf("tokens_updated = %v, want 0", resp["tokens_updated"])
	}
}

// TestInternalBackfillTokenAccountIDs_SuccessfulMatch drives the full
// success path against a fake identity service: the mapping resolves to a
// real platform account, the token's IdentityAccountID must be persisted
// with exactly that account ID, and a second run must then be a no-op
// (idempotency).
func TestInternalBackfillTokenAccountIDs_SuccessfulMatch(t *testing.T) {
	ctx := handlerMoneySetupBackfillRouter(t)
	defer ctx.cleanup()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/accounts/by-idp-sub/sub-good" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 77001, "idp_subject": "sub-good", "email": "u@x.com"}`))
	}))
	defer fake.Close()
	common.IdentityServiceURL = fake.URL

	tok := &repo.Token{UserId: 504, Key: "matched-key", Status: common.TokenStatusEnabled}
	if err := ctx.db.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	mapping := &repo.UserIdentityMapping{
		LurusUserID: 504,
		IDPSubject:  "sub-good",
		TenantID:    "default",
		IsActive:    true,
	}
	if err := ctx.db.Create(mapping).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	w := handlerMoneyPostBackfill(ctx.router)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["users_matched"].(float64)) != 1 {
		t.Fatalf("users_matched = %v, want 1", resp["users_matched"])
	}
	if int(resp["tokens_updated"].(float64)) != 1 {
		t.Fatalf("tokens_updated = %v, want 1, body=%s", resp["tokens_updated"], w.Body.String())
	}

	var reloaded repo.Token
	if err := ctx.db.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reloaded.IdentityAccountID != 77001 {
		t.Fatalf("IdentityAccountID = %d, want 77001", reloaded.IdentityAccountID)
	}

	// Idempotency: a second run must find nothing left to backfill.
	w2 := handlerMoneyPostBackfill(ctx.router)
	var resp2 map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2["message"] != "no tokens to backfill" {
		t.Errorf("second run: expected no-op, got %v", resp2["message"])
	}
}

// NOTE: the degenerate-account-id case (platform answers id=0) is covered by
// TestFixBackfillZeroAccountIDNotCountedAsMatch in fix_backfill_account_test.go,
// which asserts the same three facts through the same handler with the corrected
// expectation — users_matched must be 0, not 1.

// TestInternalBackfillTokenAccountIDs_MultipleTokensSameUser: two tokens for
// the same user must both get updated from the single resolved account
// (the per-user resolution is amortized across all of that user's tokens).
func TestInternalBackfillTokenAccountIDs_MultipleTokensSameUser(t *testing.T) {
	ctx := handlerMoneySetupBackfillRouter(t)
	defer ctx.cleanup()

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": 88002, "idp_subject": "sub-multi"}`))
	}))
	defer fake.Close()
	common.IdentityServiceURL = fake.URL

	tok1 := &repo.Token{UserId: 506, Key: "multi-key-1", Status: common.TokenStatusEnabled}
	tok2 := &repo.Token{UserId: 506, Key: "multi-key-2", Status: common.TokenStatusEnabled}
	if err := ctx.db.Create(tok1).Error; err != nil {
		t.Fatalf("seed token1: %v", err)
	}
	if err := ctx.db.Create(tok2).Error; err != nil {
		t.Fatalf("seed token2: %v", err)
	}
	mapping := &repo.UserIdentityMapping{
		LurusUserID: 506, IDPSubject: "sub-multi", TenantID: "default", IsActive: true,
	}
	if err := ctx.db.Create(mapping).Error; err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	w := handlerMoneyPostBackfill(ctx.router)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if int(resp["unique_users"].(float64)) != 1 {
		t.Errorf("unique_users = %v, want 1", resp["unique_users"])
	}
	if int(resp["tokens_updated"].(float64)) != 2 {
		t.Fatalf("tokens_updated = %v, want 2 (both tokens for the matched user)", resp["tokens_updated"])
	}
}

// ─── redemption.go: SearchRedemptions (v1) ──────────────────────────────────
//
// SearchRedemptions had ZERO test coverage. Root sees a global search;
// non-root callers are pinned to their own tenant via
// SearchRedemptionsByTenant — an IDOR here would let a tenant admin
// enumerate other tenants' unredeemed codes by name.

func TestSearchRedemptions_V1_TenantScopedExcludesOtherTenant(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	own := &repo.Redemption{
		UserId: ctx.AdminUser.Id, TenantId: ctx.TenantID,
		Key: common.GetUUID(), Name: "promo-own", Quota: 1000,
		Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(own); err != nil {
		t.Fatalf("seed own redemption: %v", err)
	}
	otherTenant := "other-tenant-search-xyz"
	other := &repo.Redemption{
		UserId: 999999, TenantId: otherTenant,
		Key: common.GetUUID(), Name: "promo-other", Quota: 2000,
		Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(other); err != nil {
		t.Fatalf("seed other-tenant redemption: %v", err)
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id": ctx.AdminUser.Id, "role": common.RoleAdminUser, "tenant_id": ctx.TenantID,
	}))
	r.GET("/api/redemption/search", SearchRedemptions)

	w := doAnalyticsGet(r, "/api/redemption/search?keyword=promo&p=1&page_size=10")
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("tenant-scoped search must not leak other tenant's redemption, got %d items: %v", len(items), items)
	}
	got := items[0].(map[string]interface{})
	if got["name"].(string) != "promo-own" {
		t.Errorf("expected own tenant's row, got %v", got["name"])
	}
}

func TestSearchRedemptions_V1_RootSeesAllTenants(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	own := &repo.Redemption{
		UserId: ctx.RootUser.Id, TenantId: ctx.TenantID,
		Key: common.GetUUID(), Name: "rootsearch-own", Quota: 1000,
		Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(own); err != nil {
		t.Fatalf("seed own redemption: %v", err)
	}
	otherTenant := "other-tenant-root-search"
	other := &repo.Redemption{
		UserId: 999999, TenantId: otherTenant,
		Key: common.GetUUID(), Name: "rootsearch-other", Quota: 2000,
		Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(other); err != nil {
		t.Fatalf("seed other-tenant redemption: %v", err)
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id": ctx.RootUser.Id, "role": common.RoleRootUser, "tenant_id": ctx.TenantID,
	}))
	r.GET("/api/redemption/search", SearchRedemptions)

	w := doAnalyticsGet(r, "/api/redemption/search?keyword=rootsearch&p=1&page_size=10")
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("root search must see both tenants' matching rows, got %d: %v", len(items), items)
	}
}

func TestSearchRedemptions_V1_KeywordPrefixOnly(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	match := &repo.Redemption{
		UserId: ctx.AdminUser.Id, TenantId: ctx.TenantID,
		Key: common.GetUUID(), Name: "alpha-batch", Quota: 500,
		Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(),
	}
	noMatch := &repo.Redemption{
		UserId: ctx.AdminUser.Id, TenantId: ctx.TenantID,
		Key: common.GetUUID(), Name: "beta-batch-alpha-suffix", Quota: 500,
		Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(),
	}
	if err := repo.RedemptionInsert(match); err != nil {
		t.Fatalf("seed match: %v", err)
	}
	if err := repo.RedemptionInsert(noMatch); err != nil {
		t.Fatalf("seed no-match: %v", err)
	}

	r := gin.New()
	r.Use(setAnalyticsCtx(map[string]interface{}{
		"id": ctx.AdminUser.Id, "role": common.RoleAdminUser, "tenant_id": ctx.TenantID,
	}))
	r.GET("/api/redemption/search", SearchRedemptions)

	// Prefix-only match ("alpha%"): the suffix-containing row must be excluded.
	w := doAnalyticsGet(r, "/api/redemption/search?keyword=alpha&p=1&page_size=10")
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("prefix search should match exactly 1 row, got %d: %v", len(items), items)
	}
	if items[0].(map[string]interface{})["name"].(string) != "alpha-batch" {
		t.Errorf("expected 'alpha-batch', got %v", items[0])
	}
}

// ─── v2_billing_invoices.go: parseBillingMonthRange edge cases ─────────────
//
// Pure-function coverage: malformed month strings, inverted range, and the
// caller-supplied span silently clamped to the 12-month ceiling regardless
// of what the caller asked for (an unbounded range would let one request
// force an unbounded GROUP BY scan).

func TestParseBillingMonthRange_InvalidFromFormat(t *testing.T) {
	_, _, err := parseBillingMonthRange("2026/01", "")
	if err == nil {
		t.Fatal("expected error for malformed 'from' month, got nil")
	}
	if !strings.Contains(err.Error(), "from must be") {
		t.Errorf("expected 'from must be' in error, got: %v", err)
	}
}

func TestParseBillingMonthRange_InvalidToFormat(t *testing.T) {
	_, _, err := parseBillingMonthRange("2026-01", "not-a-month")
	if err == nil {
		t.Fatal("expected error for malformed 'to' month, got nil")
	}
	if !strings.Contains(err.Error(), "to must be") {
		t.Errorf("expected 'to must be' in error, got: %v", err)
	}
}

func TestParseBillingMonthRange_FromAfterTo(t *testing.T) {
	_, _, err := parseBillingMonthRange("2026-06", "2026-01")
	if err == nil {
		t.Fatal("expected error when from > to, got nil")
	}
	if !strings.Contains(err.Error(), "from must be before to") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestParseBillingMonthRange_ClampsOverLongSpan(t *testing.T) {
	from, toExcl, err := parseBillingMonthRange("2000-01", "2026-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// toExcl is the first second of the month after "to" (2026-02-01).
	wantToExcl := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	if !toExcl.Equal(wantToExcl) {
		t.Errorf("toExcl = %v, want %v", toExcl, wantToExcl)
	}
	if months := monthsBetween(from, toExcl); months != maxInvoiceMonths {
		t.Errorf("expected the range clamped to exactly %d months regardless of caller span, got %d (from=%v)", maxInvoiceMonths, months, from)
	}
}

func TestParseBillingMonthRange_DefaultsToTrailingWindow(t *testing.T) {
	from, toExcl, err := parseBillingMonthRange("", "")
	if err != nil {
		t.Fatalf("unexpected error on defaults: %v", err)
	}
	if months := monthsBetween(from, toExcl); months != maxInvoiceMonths {
		t.Errorf("default window should span exactly %d months, got %d", maxInvoiceMonths, months)
	}
	if !from.Before(toExcl) {
		t.Errorf("from (%v) must be before toExcl (%v)", from, toExcl)
	}
}

func TestParseBillingMonthRange_FromOnlySameMonthValid(t *testing.T) {
	from, toExcl, err := parseBillingMonthRange("2026-01", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFrom := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if !from.Equal(wantFrom) {
		t.Errorf("from = %v, want %v", from, wantFrom)
	}
	if !from.Before(toExcl) {
		t.Errorf("from must be before the default toExcl, got from=%v toExcl=%v", from, toExcl)
	}
}

// ─── v2_billing_invoices.go: ListInvoicesV2 HTTP-level error branches ──────

func TestListInvoicesV2_NoTenantContext(t *testing.T) {
	r := gin.New()
	r.GET("/api/v2/:tenant_slug/billing/invoices", ListInvoicesV2)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/some-tenant/billing/invoices", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without tenant context, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != false {
		t.Errorf("expected success=false, got %v", resp["success"])
	}
}

func TestListInvoicesV2_InvalidFromQueryParam(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("tenant_context", &middleware.TenantContext{TenantID: "t1", UserID: 1})
		c.Next()
	})
	r.GET("/api/v2/:tenant_slug/billing/invoices", ListInvoicesV2)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/t1/billing/invoices?from=garbage", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed 'from', got %d body=%s", w.Code, w.Body.String())
	}
}

// ─── switch_reconciliation.go: tenant_slug cross-check branches ────────────
//
// The tenant_slug branch only activates when a tenant-scoped sibling route
// is registered. Uncovered until now — a token minted for tenant A must be
// rejected on a URL scoped to tenant B (classic cross-tenant IDOR), and an
// unknown tenant_slug must not leak an internal error.

func handlerMoneySetupReconcileTenantRouter(t *testing.T) (*V2TestContext, *gin.Engine) {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	r := gin.New()
	r.POST("/api/v2/:tenant_slug/switch/reconciliation", SwitchReconciliation)
	return ctx, r
}

func TestSwitchReconciliation_TenantSlugMismatchForbidden(t *testing.T) {
	ctx, r := handlerMoneySetupReconcileTenantRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "recon-tenant-check")

	otherTenant := &repo.Tenant{
		Id: "recon-other-tenant", Name: "Other", Slug: "recon-other-tenant",
		Status: repo.TenantStatusEnabled, IDPOrgID: "org_recon_other",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(otherTenant).Error; err != nil {
		t.Fatalf("seed other tenant: %v", err)
	}

	body, _ := json.Marshal(map[string]int64{"start_time": 0, "end_time": 0})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v2/"+otherTenant.Slug+"/switch/reconciliation", bytes.NewReader(body))
	req.Header.Set("Authorization", tok.Key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant token, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != false {
		t.Errorf("expected success=false, got %v", resp["success"])
	}
}

func TestSwitchReconciliation_TenantSlugMatchesAllowed(t *testing.T) {
	ctx, r := handlerMoneySetupReconcileTenantRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "recon-tenant-ok")

	body, _ := json.Marshal(map[string]int64{"start_time": 0, "end_time": 0})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v2/"+ctx.TenantID+"/switch/reconciliation", bytes.NewReader(body))
	req.Header.Set("Authorization", tok.Key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("same-tenant token should be allowed, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSwitchReconciliation_UnknownTenantSlug404sAsForbidden(t *testing.T) {
	ctx, r := handlerMoneySetupReconcileTenantRouter(t)
	defer ctx.Cleanup()

	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "recon-unknown-tenant")

	body, _ := json.Marshal(map[string]int64{})
	req := httptest.NewRequest(http.MethodPost,
		"/api/v2/does-not-exist-tenant/switch/reconciliation", bytes.NewReader(body))
	req.Header.Set("Authorization", tok.Key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unknown tenant_slug, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSwitchReconciliation_MalformedJSONBody(t *testing.T) {
	ctx, r := handlerMoneySetupReconcileTenantRouter(t)
	defer ctx.Cleanup()
	tok := SeedV2Token(t, ctx, ctx.NormalUser.Id, "recon-malformed-body")

	req := httptest.NewRequest(http.MethodPost,
		"/api/v2/"+ctx.TenantID+"/switch/reconciliation", strings.NewReader(`{"start_time": "not-a-number"}`))
	req.Header.Set("Authorization", tok.Key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d body=%s", w.Code, w.Body.String())
	}
}

// ─── internal_credit_pool_fund.go: request-shape edge cases ────────────────

func TestInternalFundCreditPool_MalformedJSONBody(t *testing.T) {
	router, cleanup, tenant, _ := setupFundRouter(t)
	t.Cleanup(cleanup)

	req := httptest.NewRequest(http.MethodPost,
		"/internal/v1/provisioning/tenants/"+tenant.Slug+"/credit-pool/fund",
		strings.NewReader(`{"amount": "not-a-number"`)) // truncated + wrong type
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testApiKeyAllScopes)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "INVALID_REQUEST" {
		t.Errorf("expected error_code=INVALID_REQUEST, got %v", resp["error_code"])
	}
}

// TestInternalFundCreditPool_MissingSlug drives the handler directly (not
// through the router) with an empty :slug param — gin's own router would
// never dispatch an empty path segment to this handler, but the handler
// still carries a defensive check for it; this exercises that guard clause.
func TestInternalFundCreditPool_MissingSlug(t *testing.T) {
	_, cleanup, _, _ := setupFundRouter(t)
	t.Cleanup(cleanup)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "slug", Value: ""}}
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/provisioning/tenants//credit-pool/fund", nil)
	c.Request = req

	InternalFundCreditPool(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty slug, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "MISSING_TENANT_SLUG" {
		t.Errorf("expected error_code=MISSING_TENANT_SLUG, got %v", resp["error_code"])
	}
}

// ─── internal_api.go: Admin API key CRUD success + error paths ─────────────
//
// These lines were previously exercised only for parse-failure (invalid id)
// and permission-check (wildcard scope) branches; the actual DB read/write
// success paths, and the toggle-on-unknown-id error path, were uncovered.

func handlerMoneySetupApiKeyAdminRouter(t *testing.T) (*gin.Engine, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbName := fmt.Sprintf("file:apikeyadmin%d?mode=memory&cache=shared", handlerMoneyBackfillDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&repo.InternalApiKey{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	prevDB, prevLog := repo.DB, repo.LOG_DB
	prevSQLite, prevPG := common.UsingSQLite, common.UsingPostgreSQL
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("role", common.RoleRootUser)
		c.Next()
	})
	router.POST("/api/admin/api-keys", AdminCreateApiKey)
	router.PUT("/api/admin/api-keys/:id", AdminUpdateApiKey)
	router.DELETE("/api/admin/api-keys/:id", AdminDeleteApiKey)
	router.PUT("/api/admin/api-keys/:id/toggle", AdminToggleApiKey)

	cleanup := func() {
		repo.DB, repo.LOG_DB = prevDB, prevLog
		common.UsingSQLite, common.UsingPostgreSQL = prevSQLite, prevPG
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	return router, cleanup
}

func TestAdminApiKeyCRUD_FullLifecycle(t *testing.T) {
	router, cleanup := handlerMoneySetupApiKeyAdminRouter(t)
	defer cleanup()

	// Create (non-wildcard scope, so no permission short-circuit).
	createBody := map[string]interface{}{
		"name":   "lifecycle-key",
		"scopes": []string{repo.ScopeBalanceRead},
	}
	w := internalRequest(router, "POST", "/api/admin/api-keys", createBody, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("create: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	data := resp["data"].(map[string]interface{})
	fullKey, _ := data["key"].(string)
	if fullKey == "" {
		t.Fatalf("create: expected a full key in the one-time response, got: %s", w.Body.String())
	}
	keyInfo := data["key_info"].(map[string]interface{})
	keyID := int(keyInfo["id"].(float64))
	if keyID == 0 {
		t.Fatalf("create: expected non-zero persisted key id")
	}

	// Update (non-wildcard).
	updateBody := map[string]interface{}{
		"name":   "lifecycle-key-renamed",
		"scopes": []string{repo.ScopeBalanceRead, repo.ScopeQuotaRead},
	}
	w = internalRequest(router, "PUT", fmt.Sprintf("/api/admin/api-keys/%d", keyID), updateBody, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var reloaded repo.InternalApiKey
	if err := repo.DB.First(&reloaded, keyID).Error; err != nil {
		t.Fatalf("reload after update: %v", err)
	}
	if reloaded.Name != "lifecycle-key-renamed" {
		t.Errorf("update did not persist: name = %q", reloaded.Name)
	}

	// Toggle: enabled -> disabled.
	wasEnabled := reloaded.Enabled
	w = internalRequest(router, "PUT", fmt.Sprintf("/api/admin/api-keys/%d/toggle", keyID), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var afterToggle repo.InternalApiKey
	if err := repo.DB.First(&afterToggle, keyID).Error; err != nil {
		t.Fatalf("reload after toggle: %v", err)
	}
	if afterToggle.Enabled == wasEnabled {
		t.Errorf("toggle did not flip Enabled: still %v", afterToggle.Enabled)
	}

	// Delete.
	w = internalRequest(router, "DELETE", fmt.Sprintf("/api/admin/api-keys/%d", keyID), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var count int64
	repo.DB.Model(&repo.InternalApiKey{}).Where("id = ?", keyID).Count(&count)
	if count != 0 {
		t.Errorf("delete did not remove the row, count=%d", count)
	}
}

func TestAdminToggleApiKey_UnknownIDFails(t *testing.T) {
	router, cleanup := handlerMoneySetupApiKeyAdminRouter(t)
	defer cleanup()

	w := internalRequest(router, "PUT", "/api/admin/api-keys/999999/toggle", nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a nonexistent key id, got %d body=%s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp["success"] != false {
		t.Errorf("expected success=false, got %v", resp["success"])
	}
}
