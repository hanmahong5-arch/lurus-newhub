package middleware

// r4_cost_spike_wire_test.go — L3-CONTRACT-TAXONOMY item 4 + item 6 for
// CostSpikeLimit's enforce-mode 429: before the root-mapping fix this
// middleware wrote a hand-built OpenAI-shaped c.AbortWithStatusJSON body
// unconditionally — a Claude caller breaching the limit would get an
// OpenAI envelope its SDK cannot parse. Proven here through the real
// middleware on both wires, plus the X-RateLimit-Scope/Type headers item 6
// added to this gate (Scope "user" / Type "cost" — the account CostSpikeLimit
// itself just disabled).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// mountCostSpikeWire mirrors relay-router.go's ordering: StampRelayFormat is
// the group's first Use(), ahead of CostSpikeLimit — load-bearing because
// gin snapshots a group's chain at Use()/route-registration time.
func mountCostSpikeWire(userID int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	setID := func(c *gin.Context) { c.Set("id", userID); c.Next() }
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }

	v1 := r.Group("/v1")
	v1.Use(StampRelayFormat(), setID)
	v1.POST("/chat/completions", CostSpikeLimit(), pass)
	v1.POST("/messages", CostSpikeLimit(), pass)
	return r
}

func TestR4CostSpikeLimit_EnforceBreach_TypedEnvelopeBothWiresAndHeaders(t *testing.T) {
	_, dbCleanup := setupCoverDB(t)
	defer dbCleanup()

	_, rdb, redisCleanup := withMiniRedis(t)
	defer redisCleanup()
	restore := r4SetCostSpikeGlobals(true, 50000)
	defer restore()

	t.Run("openai_wire", func(t *testing.T) {
		user := &repo.User{Username: "r4-wire-openai", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "r4-wire-openai@local", TenantId: "default"}
		if err := repo.DB.Create(user).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
		r4SeedBreachWindow(t, rdb, user.Id, 60000)

		r := mountCostSpikeWire(user.Id)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
		}
		var body struct {
			Error struct {
				Type string `json:"type"`
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body did not unmarshal into the OpenAI envelope: %v; body=%s", err, w.Body.String())
		}
		if body.Error.Type != "rate_limit_error" {
			t.Errorf("error.type = %q, want rate_limit_error", body.Error.Type)
		}
		if body.Error.Code != "cost_spike_limit_exceeded" {
			t.Errorf("error.code = %q, want cost_spike_limit_exceeded", body.Error.Code)
		}
		if got := w.Header().Get("X-RateLimit-Scope"); got != "user" {
			t.Errorf("X-RateLimit-Scope = %q, want user", got)
		}
		if got := w.Header().Get("X-RateLimit-Type"); got != "cost" {
			t.Errorf("X-RateLimit-Type = %q, want cost", got)
		}
	})

	t.Run("claude_wire", func(t *testing.T) {
		user := &repo.User{Username: "r4-wire-claude", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Email: "r4-wire-claude@local", TenantId: "default"}
		if err := repo.DB.Create(user).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
		r4SeedBreachWindow(t, rdb, user.Id, 60000)

		r := mountCostSpikeWire(user.Id)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429; body=%s", w.Code, w.Body.String())
		}
		var body struct {
			Type  string `json:"type"`
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body did not unmarshal into the Claude envelope: %v; body=%s", err, w.Body.String())
		}
		if body.Type != "error" {
			t.Errorf("top-level type = %q, want \"error\" (Claude wire)", body.Type)
		}
		if body.Error.Type != "rate_limit_error" {
			t.Errorf("error.type = %q, want rate_limit_error (not an OpenAI-shaped literal leaking onto the Claude wire)", body.Error.Type)
		}
		if strings.Contains(w.Body.String(), "new_api_error") {
			t.Errorf("body = %s, must not leak the retired new_api_error literal", w.Body.String())
		}
		if got := w.Header().Get("X-RateLimit-Scope"); got != "user" {
			t.Errorf("X-RateLimit-Scope = %q, want user", got)
		}
		if got := w.Header().Get("X-RateLimit-Type"); got != "cost" {
			t.Errorf("X-RateLimit-Type = %q, want cost", got)
		}
	})
}
