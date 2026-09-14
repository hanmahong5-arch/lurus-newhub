package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

func doDistributeWithHeaders(r *gin.Engine, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The session-affinity pin is consulted only by the FIRST channel selection
// (retries skip the lookup). That selection happens in this middleware, before
// the relay handler parses the typed body and derives the key — so the
// distributor must derive it itself, or the pin is never looked up nor stored
// and only the response header is emitted. This test drives the real
// Distribute() with two equal-weight channels: the first request must record
// a miss and store a binding; the second must record a hit and land on the
// same channel; the key stored is the key echoed in X-Lurus-Affinity-Key, so
// an operator can purge it by that value.
func TestDistribute_SessionAffinity_FirstSelectionUsesPin(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })
	t.Setenv("SESSION_AFFINITY_ENABLED", "true")

	seedTenantRelayChannel(t, db, 9701, "default", 100)
	seedTenantRelayChannel(t, db, 9702, "default", 100)
	repo.InitChannelCache()

	setup := func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("token_id", 7)
		c.Set("group", "default")
		c.Set("tenant_context", &TenantContext{TenantID: "default"})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	}
	headers := map[string]string{"X-Session-Id": "conv-affinity-probe"}

	before := app.AffinityStatsSnapshot()

	r := mountDistributeCapture(setup)
	w1 := doDistributeWithHeaders(r, `{"model":"gpt-4o"}`, headers)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, body=%s", w1.Code, w1.Body.String())
	}
	key := w1.Header().Get(app.AffinityKeyResponseHeader)
	if key == "" {
		t.Fatal("first response carries no X-Lurus-Affinity-Key")
	}
	mid := app.AffinityStatsSnapshot()
	if mid.Miss-before.Miss != 1 || mid.Hit-before.Hit != 0 {
		t.Fatalf("after first request: miss delta = %d, hit delta = %d, want 1/0 (the first selection must consult the pin)", mid.Miss-before.Miss, mid.Hit-before.Hit)
	}

	w2 := doDistributeWithHeaders(mountDistributeCapture(setup), `{"model":"gpt-4o"}`, headers)
	if w2.Code != http.StatusOK {
		t.Fatalf("second request status = %d, body=%s", w2.Code, w2.Body.String())
	}
	after := app.AffinityStatsSnapshot()
	if after.Hit-mid.Hit != 1 {
		t.Fatalf("second request: hit delta = %d, want 1 (the binding stored by the first selection must be found)", after.Hit-mid.Hit)
	}
	if w1.Body.String() != w2.Body.String() {
		t.Errorf("pinned selection differs: first=%s second=%s", w1.Body.String(), w2.Body.String())
	}
	if got := w2.Header().Get(app.AffinityKeyResponseHeader); got != key {
		t.Errorf("affinity key changed between turns: %s vs %s", key, got)
	}

	found, err := app.PurgeAffinityKey(w2ctx(t), key)
	if err != nil || !found {
		t.Fatalf("purge by the echoed key: found=%v err=%v — the stored key must equal the header value", found, err)
	}
}

// The body-sourced ids must produce the same key the relay handler derives
// (header absent → prompt_cache_key → metadata.user_id).
func TestSessionAffinityRawID_BodySourcesAndPrecedence(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	if got := sessionAffinityRawID(c, &ModelRequest{PromptCacheKey: []byte(`"pck-1"`)}); got != "pck-1" {
		t.Errorf("prompt_cache_key → %q, want pck-1", got)
	}
	if got := sessionAffinityRawID(c, &ModelRequest{PromptCacheKey: []byte(`123`)}); got != "" {
		t.Errorf("non-string prompt_cache_key → %q, want empty", got)
	}
	m := &ModelRequest{}
	m.Metadata = &struct {
		UserId string `json:"user_id"`
	}{UserId: " user-9 "}
	if got := sessionAffinityRawID(c, m); got != "user-9" {
		t.Errorf("metadata.user_id → %q, want user-9", got)
	}
	c.Request.Header.Set("X-Session-Id", "hdr")
	if got := sessionAffinityRawID(c, &ModelRequest{PromptCacheKey: []byte(`"pck-1"`)}); got != "hdr" {
		t.Errorf("header must win over body: got %q", got)
	}
	if got := sessionAffinityRawID(c, nil); got != "hdr" {
		t.Errorf("nil model request with header → %q, want hdr", got)
	}
}

func w2ctx(t *testing.T) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	return c
}
