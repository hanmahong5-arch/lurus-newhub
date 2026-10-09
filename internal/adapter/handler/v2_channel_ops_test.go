package handler

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/planquota"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type opsFixture struct {
	*V2TestContext
	engine *gin.Engine
}

func setupChannelOps(t *testing.T) *opsFixture {
	t.Helper()
	ctx := SetupV2TestRouter(t)
	if err := ctx.DB.AutoMigrate(&repo.ChannelOverrideTemplate{}, &repo.ChannelTemplateApplication{}); err != nil {
		t.Fatal(err)
	}
	auth := func(c *gin.Context) {
		uid, _ := strconv.Atoi(c.GetHeader("X-Test-User-ID"))
		c.Set("tenant_context", &middleware.TenantContext{TenantID: ctx.TenantID, UserID: uid})
		c.Set("tenant_id", ctx.TenantID)
		c.Set("id", uid)
		if u, _ := repo.GetUserById(uid, false); u != nil {
			c.Set("role", u.Role)
		}
		c.Next()
	}
	e := gin.New()
	g := e.Group("/api/v2/:tenant_slug/channels")
	g.Use(auth)
	g.GET("", ListChannelsV2)
	g.GET("/health-summary", GetChannelHealthSummaryV2)
	g.GET("/:id/health", GetChannelHealthV2)
	a := e.Group("/api/v2/admin")
	a.Use(auth)
	a.GET("/channel-templates/:id/applications", ListChannelTemplateApplicationsV2)
	t.Cleanup(ctx.Cleanup)
	return &opsFixture{V2TestContext: ctx, engine: e}
}

func (f *opsFixture) get(user *repo.User, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, bytes.NewReader(nil))
	req.Header.Set("X-Test-User-ID", strconv.Itoa(user.Id))
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func (f *opsFixture) base() string { return "/api/v2/" + f.TenantID + "/channels" }

func (f *opsFixture) seed(t *testing.T, name string, mutate func(*repo.Channel)) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name: name, TenantId: f.TenantID, Key: "sk-" + name, Status: common.ChannelStatusEnabled,
		Type: 1, Models: "model-a", Group: "default", CreatedTime: common.GetTimestamp(),
	}
	if mutate != nil {
		mutate(ch)
	}
	if err := f.DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	return ch
}

func seedOpsFleet(t *testing.T, f *opsFixture) (plan, multi, disabled *repo.Channel) {
	t.Helper()
	exp := time.Now().Add(72 * time.Hour).Unix()
	plan = f.seed(t, "plan", func(ch *repo.Channel) {
		ch.SetSetting(dto.ChannelSettings{PlanKind: planquota.KindZhipuCoding, ExpiresAt: exp})
	})
	multi = f.seed(t, "multi", func(ch *repo.Channel) {
		ch.Key = "k1\nk2\nk3"
		ch.ChannelInfo.IsMultiKey = true
		ch.ChannelInfo.MultiKeyStatusList = map[int]int{1: common.ChannelStatusManuallyDisabled}
		ch.ChannelInfo.MultiKeyMeta = map[int]entity.KeyMeta{2: {ExpiresAt: 1893456000}}
	})
	disabled = f.seed(t, "off", func(ch *repo.Channel) { ch.Status = common.ChannelStatusManuallyDisabled })
	planquota.StoreSnapshot(&planquota.Snapshot{ChannelID: plan.Id, Kind: planquota.KindZhipuCoding, FetchedAt: time.Now().Unix(),
		Windows: []planquota.Window{{Name: planquota.Window5h, UsedPct: 42, ResetAt: exp}, {Name: planquota.WindowWeekly, UsedPct: 71.5, ResetAt: exp}}})
	return
}

func TestChannelHealthWindowIsWired(t *testing.T) {
	f := setupChannelOps(t)
	plan, _, _ := seedOpsFleet(t, f)

	w := f.get(f.AdminUser, fmt.Sprintf("%s/%d/health", f.base(), plan.Id))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	win := gjson.Get(w.Body.String(), "data.window")
	if !win.Exists() || win.Type == gjson.Null {
		t.Fatalf("health.window must not be null once a snapshot exists: %s", w.Body.String())
	}
	if n := len(win.Get("windows").Array()); n != 2 {
		t.Fatalf("want 2 windows, got %d: %s", n, win.Raw)
	}
	if got := win.Get("windows.0.used_pct").Float(); got != 42 {
		t.Errorf("used_pct = %v", got)
	}
	if win.Get("windows.0.name").String() != "5h" || win.Get("windows.0.reset_at").Int() == 0 {
		t.Errorf("window fields: %s", win.Raw)
	}
	if gjson.Get(w.Body.String(), "data.keys.0.window.windows.1.used_pct").Float() != 71.5 {
		t.Errorf("key window missing: %s", w.Body.String())
	}
	unprobed := f.seed(t, "unprobed", nil)
	w = f.get(f.AdminUser, fmt.Sprintf("%s/%d/health", f.base(), unprobed.Id))
	if gjson.Get(w.Body.String(), "data.window").Type != gjson.Null {
		t.Errorf("unprobed window should be null: %s", w.Body.String())
	}
}

func TestListChannelsV2_OpsFields(t *testing.T) {
	f := setupChannelOps(t)
	plan, multi, disabled := seedOpsFleet(t, f)

	w := f.get(f.AdminUser, f.base())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	byID := map[int64]gjson.Result{}
	for _, it := range gjson.Get(w.Body.String(), "data.channels").Array() {
		byID[it.Get("id").Int()] = it
	}
	p := byID[int64(plan.Id)]
	if p.Get("plan_kind").String() != "zhipu_coding" || p.Get("expires_at").Int() == 0 {
		t.Errorf("plan fields: %s", p.Raw)
	}
	if p.Get("key_count").Int() != 1 || p.Get("enabled_key_count").Int() != 1 || !p.Get("routable").Bool() {
		t.Errorf("plan key/routable: %s", p.Raw)
	}
	if !p.Get("unroutable_reasons").IsArray() || len(p.Get("unroutable_reasons").Array()) != 0 {
		t.Errorf("reasons must be an empty array: %s", p.Raw)
	}
	m := byID[int64(multi.Id)]
	if m.Get("key_count").Int() != 3 || m.Get("enabled_key_count").Int() != 2 || !m.Get("routable").Bool() {
		t.Errorf("multi counts: %s", m.Raw)
	}
	if m.Get("expires_at").Int() != 1893456000 {
		t.Errorf("multi expiry: %s", m.Raw)
	}
	d := byID[int64(disabled.Id)]
	if d.Get("routable").Bool() || d.Get("unroutable_reasons.0").String() != ReasonDisabledManual {
		t.Errorf("disabled: %s", d.Raw)
	}
	if strings.Contains(w.Body.String(), "sk-plan") || strings.Contains(w.Body.String(), "k1") {
		t.Errorf("key material leaked: %s", w.Body.String())
	}
	for _, ch := range []*repo.Channel{plan, multi, disabled} {
		hw := f.get(f.AdminUser, fmt.Sprintf("%s/%d/health", f.base(), ch.Id))
		if got, want := byID[int64(ch.Id)].Get("routable").Bool(), gjson.Get(hw.Body.String(), "data.routable").Bool(); got != want {
			t.Errorf("channel %d list routable=%v health=%v", ch.Id, got, want)
		}
	}
}

func TestChannelHealthSummary_MatchesPerChannelHealth(t *testing.T) {
	f := setupChannelOps(t)
	plan, multi, disabled := seedOpsFleet(t, f)

	w := f.get(f.AdminUser, f.base()+"/health-summary")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	items := gjson.Get(w.Body.String(), "data.channels").Array()
	if len(items) != 3 {
		t.Fatalf("want 3 channels, got %d: %s", len(items), w.Body.String())
	}
	for _, it := range items {
		id := it.Get("id").Int()
		hw := f.get(f.AdminUser, fmt.Sprintf("%s/%d/health", f.base(), id))
		h := gjson.Get(hw.Body.String(), "data")
		if it.Get("routable").Bool() != h.Get("routable").Bool() {
			t.Errorf("channel %d routable mismatch: %s vs %s", id, it.Raw, h.Raw)
		}
		if it.Get("reasons").Raw != h.Get("reasons").Raw {
			t.Errorf("channel %d reasons mismatch: %s vs %s", id, it.Get("reasons").Raw, h.Get("reasons").Raw)
		}
		max := -1.0
		for _, wd := range h.Get("window.windows").Array() {
			if v := wd.Get("used_pct").Float(); v > max {
				max = v
			}
		}
		got := it.Get("window_max_used_pct")
		if max < 0 && got.Type != gjson.Null {
			t.Errorf("channel %d expected null window pct: %s", id, it.Raw)
		}
		if max >= 0 && got.Float() != max {
			t.Errorf("channel %d window max %v != %v", id, got.Float(), max)
		}
		switch id {
		case int64(plan.Id):
			if got.Float() != 71.5 || it.Get("expires_at").Int() == 0 || it.Get("name").String() != "plan" {
				t.Errorf("plan item: %s", it.Raw)
			}
		case int64(multi.Id):
			if it.Get("expires_at").Int() != 1893456000 {
				t.Errorf("per-key expiry must surface: %s", it.Raw)
			}
		case int64(disabled.Id):
			if it.Get("routable").Bool() {
				t.Errorf("disabled routable: %s", it.Raw)
			}
		}
	}
}

func TestChannelHealthSummary_TenantScopeAndPermission(t *testing.T) {
	f := setupChannelOps(t)
	seedOpsFleet(t, f)
	other := &repo.Channel{Name: "foreign", TenantId: "other-tenant-xyz", Key: "sk-foreign", Status: common.ChannelStatusEnabled, Type: 1, Group: "default"}
	if err := f.DB.Create(other).Error; err != nil {
		t.Fatal(err)
	}
	if w := f.get(f.NormalUser, f.base()+"/health-summary"); w.Code != http.StatusForbidden {
		t.Errorf("member must get 403, got %d", w.Code)
	}
	w := f.get(f.RootUser, f.base()+"/health-summary")
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "foreign") {
		t.Errorf("tenant leak or status %d: %s", w.Code, w.Body.String())
	}
	if w := f.get(f.NormalUser, f.base()); w.Code != http.StatusForbidden {
		t.Errorf("list: member must get 403, got %d", w.Code)
	}
}

func TestListChannelTemplateApplicationsV2(t *testing.T) {
	f := setupChannelOps(t)
	tpl := &repo.ChannelOverrideTemplate{Name: "t1", ParamOverride: `{"a":1}`, Version: 2}
	if err := repo.CreateChannelOverrideTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	for i, ch := range []int64{11, 12, 13} {
		f.DB.Create(&repo.ChannelTemplateApplication{ChannelId: ch, TemplateId: tpl.Id, TemplateVersion: int64(i + 1), AppliedBy: 9, AppliedAt: int64(1000 + i)})
	}
	f.DB.Create(&repo.ChannelTemplateApplication{ChannelId: 99, TemplateId: tpl.Id + 1000, TemplateVersion: 1})

	path := fmt.Sprintf("/api/v2/admin/channel-templates/%d/applications", tpl.Id)
	w := f.get(f.RootUser, path)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if gjson.Get(w.Body.String(), "data.total").Int() != 3 {
		t.Errorf("total: %s", w.Body.String())
	}
	apps := gjson.Get(w.Body.String(), "data.applications").Array()
	if len(apps) != 3 || apps[0].Get("channel_id").Int() != 13 || apps[0].Get("template_version").Int() != 3 || apps[0].Get("applied_by").Int() != 9 {
		t.Errorf("rows (newest first): %s", w.Body.String())
	}
	if w := f.get(f.RootUser, "/api/v2/admin/channel-templates/987654/applications"); w.Code != http.StatusNotFound {
		t.Errorf("missing template: %d", w.Code)
	}
	if w := f.get(f.RootUser, "/api/v2/admin/channel-templates/abc/applications"); w.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", w.Code)
	}
}
