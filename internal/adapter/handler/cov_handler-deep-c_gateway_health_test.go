package handler

// cov_handler-deep-c_gateway_health_test.go — business-acceptance coverage
// for v2_admin_gateway.go's GetGatewayHealthV2 (0% before this file): the
// root-only endpoint that reports live circuit-breaker state per channel.
// Exercises the empty-registry baseline, the open/half-open tallying, the
// best-effort channel-name/provider decoration (present AND absent-channel
// cases — a breaker can legitimately outlive its channel row), and asserts
// on the actual counted/decorated fields, not just HTTP 200.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

func handlerDeepCGatewayHealthRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/gateway/health", GetGatewayHealthV2)
	return r
}

func handlerDeepCDoGatewayHealth(r *gin.Engine) (*httptest.ResponseRecorder, map[string]interface{}) {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/gateway/health", nil))
	var out map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestGetGatewayHealthV2_NoTrafficYet_EmptyRoutes(t *testing.T) {
	// channelBreakers is a real PROCESS-level registry, and "no traffic yet"
	// is a property of the whole test binary, not of this test: every relay
	// test in this package registers an entry for its channel (relay.go's
	// RecordSuccess/RecordFailure), and GetGatewayHealthV2 then decorates each
	// entry through repo.CacheGetChannel — which nil-dereferences repo.DB,
	// because this test deliberately stands up no database. In source order
	// this test happened to run first; under `go test -shuffle=on` it does not,
	// and the panic is a hard package failure (cycle-12 L1). Establish the
	// precondition instead of inheriting it, and leave the registry as empty as
	// this test needs it to be.
	channelBreakers.Cleanup(map[int]struct{}{})
	t.Cleanup(func() { channelBreakers.Cleanup(map[int]struct{}{}) })
	r := handlerDeepCGatewayHealthRouter()
	w, resp := handlerDeepCDoGatewayHealth(r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if resp["success"] != true {
		t.Fatalf("success = %v, want true, body=%s", resp["success"], w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T", resp["data"])
	}
	if data["replica_scoped"] != true || data["lazy_registered"] != true {
		t.Errorf("expected the two honesty caveats always true, got replica_scoped=%v lazy_registered=%v",
			data["replica_scoped"], data["lazy_registered"])
	}
}

// TestGetGatewayHealthV2_FailuresRecorded_TrippedBreakerCountedAndDecorated
// drives a real channel through channelBreakers.RecordFailure past its
// configured threshold, seeds the corresponding channel row (memory cache
// disabled so CacheGetChannel falls through to a real DB read), and asserts
// the response actually reflects the open state + decoration — not merely
// that some route entry exists.
func TestGetGatewayHealthV2_FailuresRecorded_TrippedBreakerCountedAndDecorated(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	prevMemCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prevMemCache })

	channel := &repo.Channel{
		Name: "handlerdeepc-gw-channel", TenantId: ctx.TenantID,
		Type: constant.ChannelTypeOpenAI, Key: "sk-gw-test", Status: common.ChannelStatusEnabled,
	}
	if err := ctx.DB.Create(channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Trip the breaker: DefaultConfig threshold defaults to 5 consecutive
	// failures unless CB_THRESHOLD overrides it; record generously past any
	// reasonable threshold to make the open-state assertion environment-proof.
	//
	// channelBreakers is a PROCESS-global registry keyed by channel id, and an
	// open breaker stays open for CB_TIMEOUT_SEC (30s). channel.Id here comes
	// from a hermetic sqlite, so it is a low auto-increment number that every
	// other hermetic fixture in this package also hands out — and relay.go
	// skips a channel whose breaker is open, which surfaces as an empty HTTP
	// 200 with no settlement in whatever relay test runs next. Dropping every
	// breaker on cleanup keeps this test's deliberate damage inside this test
	// (cycle-12 L1; `go test -shuffle=on` is what made it visible).
	t.Cleanup(func() { channelBreakers.Cleanup(map[int]struct{}{}) })
	for i := 0; i < 25; i++ {
		channelBreakers.RecordFailure(channel.Id)
	}
	if channelBreakers.GetState(channel.Id).String() != "open" {
		t.Fatalf("precondition failed: breaker for channel %d did not open after 25 failures (state=%s)",
			channel.Id, channelBreakers.GetState(channel.Id).String())
	}

	r := handlerDeepCGatewayHealthRouter()
	w, resp := handlerDeepCDoGatewayHealth(r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not a map: %T body=%s", resp["data"], w.Body.String())
	}
	if openCount, _ := data["open"].(float64); openCount < 1 {
		t.Errorf("open count = %v, want >= 1 (tripped breaker must be counted)", data["open"])
	}
	routes, ok := data["routes"].([]interface{})
	if !ok {
		t.Fatalf("routes is not a list: %T", data["routes"])
	}
	var found map[string]interface{}
	for _, rt := range routes {
		m := rt.(map[string]interface{})
		if int(m["channel_id"].(float64)) == channel.Id {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatalf("expected channel %d in routes, got %v", channel.Id, routes)
	}
	if found["state"] != "open" {
		t.Errorf("route state = %v, want open", found["state"])
	}
	if found["channel_name"] != "handlerdeepc-gw-channel" {
		t.Errorf("channel_name = %v, want decorated name (CacheGetChannel must resolve it)", found["channel_name"])
	}
	if found["provider"] != constant.GetChannelTypeName(constant.ChannelTypeOpenAI) {
		t.Errorf("provider = %v, want %v", found["provider"], constant.GetChannelTypeName(constant.ChannelTypeOpenAI))
	}
}

// TestGetGatewayHealthV2_BreakerOutlivesChannelRow_DecorationBestEffort
// covers the "missing name must not drop the health entry" comment: a
// breaker exists for a channel ID with NO backing row (deleted after the
// breaker tripped) — the route must still appear, just undecorated.
func TestGetGatewayHealthV2_BreakerOutlivesChannelRow_DecorationBestEffort(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	prevMemCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prevMemCache })

	const orphanChannelID = 987654321
	// Same process-global registry as the test above; this id cannot collide
	// with a hermetic fixture's auto-increment, but leaving entries behind
	// still makes another test's Snapshot() non-deterministic.
	t.Cleanup(func() { channelBreakers.Cleanup(map[int]struct{}{}) })
	channelBreakers.RecordFailure(orphanChannelID)

	r := handlerDeepCGatewayHealthRouter()
	w, resp := handlerDeepCDoGatewayHealth(r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	data := resp["data"].(map[string]interface{})
	routes := data["routes"].([]interface{})
	var found map[string]interface{}
	for _, rt := range routes {
		m := rt.(map[string]interface{})
		if int(m["channel_id"].(float64)) == orphanChannelID {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatalf("expected orphaned breaker's channel %d to still appear in routes, got %v", orphanChannelID, routes)
	}
	if name, present := found["channel_name"]; present && name != "" {
		t.Errorf("channel_name = %v, want absent/empty for a channel row that no longer exists", name)
	}
}
