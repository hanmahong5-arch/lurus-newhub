package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// usageRouter mounts only the two usage routes, in the production order
// (static usage-summary next to the /:id sibling), behind a header-driven
// stand-in for session auth. role=<n> sets the v1 session role.
func usageRouter(tenantID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v2/:tenant_slug/channels")
	g.Use(func(c *gin.Context) {
		role, _ := strconv.Atoi(c.GetHeader("X-Test-Role"))
		c.Set("role", role)
		c.Set("tenant_context", &middleware.TenantContext{TenantID: tenantID, UserID: 1})
		c.Set("tenant_id", tenantID)
		c.Next()
	})
	g.GET("/usage-summary", GetChannelUsageSummaryV2)
	g.GET("/:id/usage", GetChannelUsageV2)
	return r
}

func usageGet(r *gin.Engine, path string, role int) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Test-Role", strconv.Itoa(role))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func seedUsageChannel(t *testing.T, ctx *V2TestContext, tenant, name string, multiSize int, feeCNY4 int64) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{Name: name, TenantId: tenant, Key: "sk-secret-do-not-leak", Status: common.ChannelStatusEnabled,
		Type: 1, Models: "model-a", Group: "default", CreatedTime: common.GetTimestamp()}
	if multiSize > 0 {
		ch.ChannelInfo = entity.ChannelInfo{IsMultiKey: true, MultiKeySize: multiSize}
	}
	if feeCNY4 > 0 {
		s := fmt.Sprintf(`{"plan_monthly_fee_cny4":%d}`, feeCNY4)
		ch.Setting = &s
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return ch
}

func seedUsageRow(t *testing.T, ctx *V2TestContext, ch int, key int64, typ int, ago int64, priced int64) {
	t.Helper()
	k := key
	row := &repo.Log{UserId: 1, Type: typ, CreatedAt: common.GetTimestamp() - ago, ChannelId: ch, ChannelKeyIdx: &k,
		PromptTokens: 10, CompletionTokens: 5, Quota: 3, PricedCNY4: priced, ModelName: "model-a"}
	if err := ctx.DB.Create(row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

type usageBody struct {
	Success bool `json:"success"`
	Data    struct {
		ChannelId   int      `json:"channel_id"`
		Window      string   `json:"window"`
		Utilization *float64 `json:"utilization"`
		Keys        []struct {
			KeyIdx   int64 `json:"key_idx"`
			Requests int64 `json:"requests"`
			Errors   int64 `json:"errors"`
			CostCNY4 int64 `json:"cost_cny4"`
			Prompt   int64 `json:"prompt_tokens"`
		} `json:"keys"`
		Total struct {
			Requests int64 `json:"requests"`
			CostCNY4 int64 `json:"cost_cny4"`
		} `json:"total"`
		Channels []struct {
			ChannelId   int      `json:"channel_id"`
			Verdict     string   `json:"verdict"`
			Utilization *float64 `json:"utilization"`
		} `json:"channels"`
	} `json:"data"`
}

func decodeUsage(t *testing.T, w *httptest.ResponseRecorder) usageBody {
	t.Helper()
	var b usageBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	return b
}

func TestChannelUsageV2_PerKeyAggregationAndUtilization(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	// 300 CNY / month plan, 3 keys; key 2 never used.
	ch := seedUsageChannel(t, ctx, ctx.TenantID, "plan-ch", 3, 300_0000)
	seedUsageRow(t, ctx, ch.Id, 0, repo.LogTypeConsume, 60, 40_0000)
	seedUsageRow(t, ctx, ch.Id, 0, repo.LogTypeError, 90, 0)
	seedUsageRow(t, ctx, ch.Id, 1, repo.LogTypeConsume, 120, 20_0000)
	seedUsageRow(t, ctx, ch.Id, 1, repo.LogTypeConsume, 40*24*3600, 999_0000) // outside 30d

	r := usageRouter(ctx.TenantID)
	w := usageGet(r, fmt.Sprintf("/api/v2/t/channels/%d/usage?window=30d", ch.Id), common.RoleAdminUser)
	AssertV2Status(t, w, http.StatusOK)
	if got := w.Body.String(); contains(got, "sk-secret") {
		t.Fatalf("response leaks channel key: %s", got)
	}
	b := decodeUsage(t, w)
	if len(b.Data.Keys) != 3 {
		t.Fatalf("want 3 keys (idle key listed), got %+v", b.Data.Keys)
	}
	k0, k1, k2 := b.Data.Keys[0], b.Data.Keys[1], b.Data.Keys[2]
	if k0.KeyIdx != 0 || k0.Requests != 2 || k0.Errors != 1 || k0.CostCNY4 != 40_0000 || k0.Prompt != 20 {
		t.Errorf("key0 = %+v", k0)
	}
	if k1.KeyIdx != 1 || k1.Requests != 1 || k1.CostCNY4 != 20_0000 {
		t.Errorf("key1 = %+v", k1)
	}
	if k2.KeyIdx != 2 || k2.Requests != 0 || k2.CostCNY4 != 0 {
		t.Errorf("idle key2 = %+v", k2)
	}
	if b.Data.Total.Requests != 3 || b.Data.Total.CostCNY4 != 60_0000 {
		t.Errorf("total = %+v", b.Data.Total)
	}
	// 60 spent of 300 for a full 30d window = 0.2.
	if b.Data.Utilization == nil || *b.Data.Utilization < 0.1999 || *b.Data.Utilization > 0.2001 {
		t.Errorf("utilization = %v, want 0.2", b.Data.Utilization)
	}
}

func TestChannelUsageV2_NoFeeNoUtilization_SingleKey(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := seedUsageChannel(t, ctx, ctx.TenantID, "payg", 0, 0)
	seedUsageRow(t, ctx, ch.Id, -1, repo.LogTypeConsume, 10, 5_0000)

	r := usageRouter(ctx.TenantID)
	w := usageGet(r, fmt.Sprintf("/api/v2/t/channels/%d/usage?window=24h", ch.Id), common.RoleAdminUser)
	AssertV2Status(t, w, http.StatusOK)
	b := decodeUsage(t, w)
	if b.Data.Utilization != nil {
		t.Errorf("no plan fee must not report utilization, got %v", *b.Data.Utilization)
	}
	if len(b.Data.Keys) != 1 || b.Data.Keys[0].KeyIdx != -1 || b.Data.Keys[0].Requests != 1 {
		t.Errorf("keys = %+v", b.Data.Keys)
	}
}

func TestChannelUsageV2_WindowRespected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := seedUsageChannel(t, ctx, ctx.TenantID, "w", 0, 0)
	seedUsageRow(t, ctx, ch.Id, -1, repo.LogTypeConsume, 3600, 1_0000)    // 1h ago
	seedUsageRow(t, ctx, ch.Id, -1, repo.LogTypeConsume, 10*3600, 1_0000) // 10h ago

	r := usageRouter(ctx.TenantID)
	for window, want := range map[string]int64{"5h": 1, "24h": 2, "7d": 2} {
		w := usageGet(r, fmt.Sprintf("/api/v2/t/channels/%d/usage?window=%s", ch.Id, window), common.RoleAdminUser)
		AssertV2Status(t, w, http.StatusOK)
		if got := decodeUsage(t, w).Data.Total.Requests; got != want {
			t.Errorf("window %s: requests=%d want %d", window, got, want)
		}
	}
	w := usageGet(r, fmt.Sprintf("/api/v2/t/channels/%d/usage?window=1y", ch.Id), common.RoleAdminUser)
	AssertV2Status(t, w, http.StatusBadRequest)
}

func TestChannelUsageV2_Authz(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	own := seedUsageChannel(t, ctx, ctx.TenantID, "own", 0, 0)
	foreign := seedUsageChannel(t, ctx, "other-tenant-xyz", "foreign", 0, 0)
	r := usageRouter(ctx.TenantID)

	// Plain member: refused on both endpoints.
	AssertV2Status(t, usageGet(r, fmt.Sprintf("/api/v2/t/channels/%d/usage", own.Id), common.RoleCommonUser), http.StatusForbidden)
	AssertV2Status(t, usageGet(r, "/api/v2/t/channels/usage-summary", common.RoleCommonUser), http.StatusForbidden)
	// Admin of another tenant's channel: refused, and nothing leaks.
	w := usageGet(r, fmt.Sprintf("/api/v2/t/channels/%d/usage", foreign.Id), common.RoleAdminUser)
	AssertV2Status(t, w, http.StatusForbidden)
	if contains(w.Body.String(), "foreign") || contains(w.Body.String(), "keys") {
		t.Errorf("cross-tenant refusal leaked data: %s", w.Body.String())
	}
	// Unknown channel.
	AssertV2Status(t, usageGet(r, "/api/v2/t/channels/999999/usage", common.RoleAdminUser), http.StatusNotFound)
}

func TestChannelUsageSummaryV2_SortVerdictAndTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	idle := seedUsageChannel(t, ctx, ctx.TenantID, "idle", 0, 300_0000)
	over := seedUsageChannel(t, ctx, ctx.TenantID, "over", 0, 300_0000)
	mid := seedUsageChannel(t, ctx, ctx.TenantID, "mid", 0, 300_0000)
	payg := seedUsageChannel(t, ctx, ctx.TenantID, "payg", 0, 0)
	foreign := seedUsageChannel(t, ctx, "other-tenant-xyz", "foreign", 0, 300_0000)

	seedUsageRow(t, ctx, idle.Id, -1, repo.LogTypeConsume, 60, 10_0000)   // 10/300 = 0.033
	seedUsageRow(t, ctx, over.Id, -1, repo.LogTypeConsume, 60, 450_0000)  // 1.5
	seedUsageRow(t, ctx, mid.Id, -1, repo.LogTypeConsume, 60, 150_0000)   // 0.5
	seedUsageRow(t, ctx, payg.Id, -1, repo.LogTypeConsume, 60, 999_0000)  // no fee
	seedUsageRow(t, ctx, foreign.Id, -1, repo.LogTypeConsume, 60, 1_0000) // other tenant

	r := usageRouter(ctx.TenantID)
	w := usageGet(r, "/api/v2/t/channels/usage-summary", common.RoleAdminUser)
	AssertV2Status(t, w, http.StatusOK)
	b := decodeUsage(t, w)
	if b.Data.Window != "30d" {
		t.Errorf("window = %q", b.Data.Window)
	}
	var order []int
	verdict := map[int]string{}
	for _, c := range b.Data.Channels {
		order = append(order, c.ChannelId)
		verdict[c.ChannelId] = c.Verdict
		if c.ChannelId == foreign.Id {
			t.Fatalf("foreign tenant channel in summary")
		}
	}
	want := []int{over.Id, mid.Id, idle.Id, payg.Id}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if verdict[over.Id] != "overuse" || verdict[idle.Id] != "idle" || verdict[mid.Id] != "" || verdict[payg.Id] != "" {
		t.Errorf("verdicts = %v", verdict)
	}
	for _, c := range b.Data.Channels {
		if c.ChannelId == payg.Id && c.Utilization != nil {
			t.Errorf("fee-less channel reported utilization")
		}
	}
}
