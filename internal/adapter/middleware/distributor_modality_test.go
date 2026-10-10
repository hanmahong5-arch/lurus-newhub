package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// distributor_modality_test.go - when the modality filter (enforce) leaves a
// model with no suitable route the distributor answers 501
// provider_capability_not_supported with a structured error.details, instead of
// the 503 model_not_found a missing model gets. Nothing was sent upstream, and
// the body names causes and counts only.

func seedModalityRoute(t *testing.T, channelID int, channelType int, model string) {
	t.Helper()
	db, cleanup := setupCoverDB(t)
	t.Cleanup(cleanup)
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })
	w := uint(1)
	ch := &repo.Channel{Id: channelID, Type: channelType, Status: common.ChannelStatusEnabled,
		Name: "secret-channel-name", BaseURL: common.GetPointer("https://secret-upstream.example"),
		Key: "sk-secret-key", Models: model, Group: "default", TenantId: "default", Weight: &w}
	if err := db.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&repo.Ability{Group: "default", Model: model, ChannelId: channelID, Enabled: true, Weight: w}).Error; err != nil {
		t.Fatal(err)
	}
}

func doModalityRequest(path, body string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	})
	pass := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	r.POST(path, Distribute(), pass)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestDistribute_ModalityMiss_501WithDetails(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
	seedModalityRoute(t, 9811, constant.ChannelTypeTypeSafe, "text-embedding-model-a")

	rec := doModalityRequest("/v1/embeddings", `{"model":"text-embedding-model-a","input":"x"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Type    string `json:"type"`
			Details struct {
				Stage             string `json:"stage"`
				UpstreamAttempted *bool  `json:"upstream_attempted"`
				Reasons           []struct {
					Code       string `json:"code"`
					Message    string `json:"message"`
					RouteCount int    `json:"route_count"`
				} `json:"reasons"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, rec.Body.String())
	}
	if got.Error.Code != string(types.ErrorCodeProviderCapabilityNotSupported) {
		t.Errorf("code = %q", got.Error.Code)
	}
	if got.Error.Type == "" || got.Error.Message == "" {
		t.Errorf("type/message missing: %+v", got.Error)
	}
	d := got.Error.Details
	if d.Stage != "route_selection" || d.UpstreamAttempted == nil || *d.UpstreamAttempted {
		t.Errorf("details = %+v, want stage=route_selection upstream_attempted=false", d)
	}
	if len(d.Reasons) != 1 || d.Reasons[0].Code != "modality_mismatch" || d.Reasons[0].RouteCount != 1 || d.Reasons[0].Message == "" {
		t.Errorf("reasons = %+v", d.Reasons)
	}
	for _, leak := range []string{"secret-channel-name", "secret-upstream", "sk-secret-key", "9811"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("body leaks %q: %s", leak, rec.Body.String())
		}
	}
}

func TestDistribute_ModalityMiss_ChatOnDecisionChannel(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
	seedModalityRoute(t, 9812, constant.ChannelTypeTypeSafe, "model-a")
	rec := doModalityRequest("/v1/chat/completions", `{"model":"model-a"}`)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDistribute_ModalityObserveAndOff_DoNotRefuse(t *testing.T) {
	for _, mode := range []string{"", "observe", "off"} {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Setenv("ROUTING_MODALITY_FILTER", mode)
			seedModalityRoute(t, 9813, constant.ChannelTypeTypeSafe, "text-embedding-model-a")
			rec := doModalityRequest("/v1/embeddings", `{"model":"text-embedding-model-a","input":"x"}`)
			if rec.Code == http.StatusNotImplemented {
				t.Fatalf("mode %q refused a route: %s", mode, rec.Body.String())
			}
		})
	}
}

func TestDistribute_ModalityEnforce_CompatibleRouteStillServes(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
	seedModalityRoute(t, 9814, 1, "text-embedding-model-a")
	rec := doModalityRequest("/v1/embeddings", `{"model":"text-embedding-model-a","input":"x"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
