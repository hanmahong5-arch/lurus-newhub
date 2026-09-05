package middleware

// tenant_relay_selection_test.go — TI-1: the ORDINARY (non-PIN) channel
// selection path inside Distribute() must never hand a tenant-owned channel
// to a caller from a different tenant. tenant_relay_guard_r3_test.go already
// covers the sk-<key>-<channelId> override path (#1); this covers the
// weighted-selection path every non-pinned relay request actually takes.

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// seedTenantRelayChannel creates a channel (with a matching enabled ability
// row, since InitChannelCache only indexes a channel's group if some ability
// row already established that group's entry in the cache's group set) at a
// deliberately lopsided weight so that a selection landing on the low-weight
// channel every single time can only be explained by the high-weight one
// having been removed from the candidate set — not merely outweighed.
func seedTenantRelayChannel(t *testing.T, db *gorm.DB, id int, tenantID string, weight uint) {
	t.Helper()
	priority := int64(0)
	ch := &repo.Channel{
		Id: id, Type: 1, Status: common.ChannelStatusEnabled,
		Name: "trc", Models: "gpt-4o", Group: "default", TenantId: tenantID,
		Weight: &weight, Priority: &priority,
	}
	if err := db.Create(ch).Error; err != nil {
		t.Fatalf("seed channel %d: %v", id, err)
	}
	if err := db.Create(&repo.Ability{
		Group: "default", Model: "gpt-4o", ChannelId: id, Enabled: true, Weight: weight, Priority: &priority,
	}).Error; err != nil {
		t.Fatalf("seed ability %d: %v", id, err)
	}
}

// mountDistributeCapture is mountDistribute (distributor_cover_test.go) plus
// a terminal handler that echoes the selected channel id back in the body, so
// tests here can assert WHICH channel was picked, not just the status code.
func mountDistributeCapture(ctxSetup func(c *gin.Context)) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if ctxSetup != nil {
			ctxSetup(c)
		}
		c.Next()
	})
	r.POST("/v1/chat/completions", Distribute(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"channel_id": common.GetContextKeyInt(c, constant.ContextKeyChannelId)})
	})
	return r
}

func doDistributeCapture(r *gin.Engine, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDistribute_TenantOwnedChannelNeverServesOtherTenant(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	seedTenantRelayChannel(t, db, 9601, "tenant-a", 1000) // A1 — heavily favoured by weight
	seedTenantRelayChannel(t, db, 9602, "default", 1)     // P1 — platform-shared, weight 1
	repo.InitChannelCache()

	for i := 0; i < 50; i++ {
		r := mountDistributeCapture(func(c *gin.Context) {
			c.Set("tenant_context", &TenantContext{TenantID: "tenant-b"})
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		})
		w := doDistributeCapture(r, `{"model":"gpt-4o"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, body=%s", i, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"channel_id":9602`) {
			t.Fatalf("attempt %d: tenant-b was served a channel other than the platform-shared one: %s", i, w.Body.String())
		}
	}

	// Positive control: tenant-a can still reach its own channel A1.
	r := mountDistributeCapture(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "tenant-a"})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := doDistributeCapture(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("tenant-a own-tenant selection: status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestDistribute_TenantOwnedChannelOnly_ForeignTenantGets503NotRelay(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()

	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	t.Cleanup(func() { common.MemoryCacheEnabled = prevCache })

	seedTenantRelayChannel(t, db, 9701, "tenant-a", 1000)
	repo.InitChannelCache()

	r := mountDistributeCapture(func(c *gin.Context) {
		c.Set("tenant_context", &TenantContext{TenantID: "tenant-b"})
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	w := doDistributeCapture(r, `{"model":"gpt-4o"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — tenant-b must not relay through tenant-a's channel; body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"channel_id":`+strconv.Itoa(9701)) {
		t.Fatalf("response leaked the foreign-tenant channel id: %s", w.Body.String())
	}
}
