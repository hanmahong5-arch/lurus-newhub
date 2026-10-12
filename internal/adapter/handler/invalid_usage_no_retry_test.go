package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// An upstream that answers a rerank with a self-contradictory usage block has
// already done (and likely billed us for) the work; asking it again only
// repeats the cost and the same malformed answer. invalid_provider_usage must
// therefore end the request after one upstream call, like its sibling
// invalid_provider_response. The http_500 control proves retries are live in
// this harness, so the single call is not an artefact of RetryTimes=0.
// Mutation: drop ErrOptionWithSkipRetry from the Jina path's site
// (common_handler/rerank.go) and the bad-usage request reaches the upstream
// RetryTimes+1 times. The other site (provider/common/rerank_finalize.go, the
// non-Jina adaptors) is held by TestValidateRerankResponse.
func TestRelay_InvalidProviderUsageIsNotRetried(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", faultSimTestKey)
	pinRetryTimes(t, 2)
	var hits atomic.Int64
	sim := faultSimRetrievalUpstream()
	ctx := setupRelaySuccessRouter(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sim(w, r)
	})

	prevRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(prevRatio) })
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"ok-badusage-rerank":1.0,"http_500":1.0}`); err != nil {
		t.Fatal(err)
	}
	ctx.channel.Type = constant.ChannelTypeJina
	ctx.channel.Models = "ok-badusage-rerank,http_500"
	if err := ctx.db.Save(ctx.channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := ctx.db.Where("channel_id = ?", ctx.channel.Id).Delete(&repo.Ability{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ctx.channel.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	g := ctx.router.Group("/v1")
	g.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
	g.POST("/rerank", func(c *gin.Context) { Relay(c, types.RelayFormatRerank) })

	rerank := func(model string) *httptest.ResponseRecorder {
		hits.Store(0)
		req := httptest.NewRequest(http.MethodPost, "/v1/rerank",
			strings.NewReader(`{"model":"`+model+`","query":"q","documents":["a","b"]}`))
		req.Header.Set("Authorization", "Bearer sk-"+ctx.token.Key)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		ctx.router.ServeHTTP(w, req)
		return w
	}

	w := rerank("ok-badusage-rerank")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "invalid_provider_usage") {
		t.Fatalf("status=%d, want 502 invalid_provider_usage; body=%s", w.Code, w.Body.String())
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("contradictory usage reached the upstream %d times, want 1 (no retry)", got)
	}

	w = rerank("http_500")
	if w.Code/100 != 5 {
		t.Fatalf("control: status=%d, want a 5xx; body=%s", w.Code, w.Body.String())
	}
	if got := hits.Load(); got < 2 {
		t.Fatalf("control: a plain upstream 500 reached the upstream %d times; retries are not live in this harness", got)
	}
}
