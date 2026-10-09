package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// When every channel serving the model is in a 429 cooldown the distributor
// answers 503 + Retry-After under its own code, not model_not_found and not
// the provider-filter sentence.
func TestDistribute_AllChannelsCooling_503RetryAfter(t *testing.T) {
	db, cleanup := setupCoverDB(t)
	defer cleanup()
	prevCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	defer func() { common.MemoryCacheEnabled = prevCache }()
	app.ClearChannelCooldowns()
	defer app.ClearChannelCooldowns()

	w := uint(1)
	ch := &repo.Channel{Id: 9701, Type: 1, Status: common.ChannelStatusEnabled, Name: "cd", Models: "cd-model", Group: "default", TenantId: "default", Weight: &w}
	if err := db.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&repo.Ability{Group: "default", Model: "cd-model", ChannelId: 9701, Enabled: true, Weight: w}).Error; err != nil {
		t.Fatal(err)
	}
	app.MarkChannelCooldown(9701, 0, time.Now().Unix()+90)

	r := mountDistribute(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	rec := doDistribute(r, `{"model":"cd-model"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || secs < 60 || secs > 90 {
		t.Fatalf("Retry-After = %q, want ~90 seconds", rec.Header().Get("Retry-After"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, string(types.ErrorCodeAllChannelsCooling)) {
		t.Fatalf("body lacks the dedicated code %q: %s", types.ErrorCodeAllChannelsCooling, body)
	}
	if strings.Contains(body, "provider filter") || strings.Contains(body, string(types.ErrorCodeModelNotFound)) {
		t.Fatalf("body reuses another rejection's wording/code: %s", body)
	}
}
