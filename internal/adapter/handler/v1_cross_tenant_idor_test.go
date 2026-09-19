package handler

// ============================================================================
// Cross-tenant IDOR regression tests for the legacy v1 admin console handlers.
//
// AdminAuth() only checks role >= RoleAdminUser (10), which is ALSO the v2
// per-tenant admin role. Before enforceTenantScope was added, a tenant admin
// could read / mutate / delete another tenant's channel, user, redemption or
// logs simply by addressing the resource's numeric id. These tests assert:
//
//   (a) a tenant admin (role 10) may still reach its OWN tenant's resource;
//   (b) a tenant admin is 403'd on ANOTHER tenant's resource (by-id);
//   (c) a root operator (role 100) retains global cross-tenant access;
//   (d) list endpoints converge to the caller's tenant for non-root, while
//       root still sees every tenant.
//
// The victim resources live in a different tenant ("other-tenant-xyz") than the
// harness's default tenant, so a mismatch is the whole point of the fixture.
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

const idorVictimTenant = "other-tenant-xyz"
const idorVictimChannelKey = "sk-v1-victim-SECRET-DO-NOT-LEAK"

// v1Ctx builds a gin.Context carrying the v1 session identity keys that
// authHelper injects in production: role, tenant_id and id.
func v1Ctx(method, target string, body interface{}, role int, tenant string, id int) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var req *http.Request
	if body != nil {
		data, _ := json.Marshal(body)
		req = httptest.NewRequest(method, target, bytes.NewReader(data))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Set("role", role)
	c.Set("tenant_id", tenant)
	c.Set("id", id)
	return c, w
}

func v1Body(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	return m
}

func v1Items(t *testing.T, w *httptest.ResponseRecorder) []interface{} {
	t.Helper()
	m := v1Body(t, w)
	data, ok := m["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, body=%s", w.Body.String())
	}
	items, _ := data["items"].([]interface{})
	return items
}

func seedV1VictimChannel(t *testing.T, ctx *V2TestContext) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name:        "victim-channel",
		TenantId:    idorVictimTenant,
		Key:         idorVictimChannelKey,
		Status:      common.ChannelStatusEnabled,
		Type:        1,
		Models:      "gpt-4",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed victim channel: %v", err)
	}
	return ch
}

// ─── Channel ────────────────────────────────────────────────────────────────

func TestV1Channel_CrossTenantIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	own := SeedV2Channel(t, ctx, "own-channel") // ctx.TenantID
	victim := seedV1VictimChannel(t, ctx)
	adminID := ctx.AdminUser.Id
	rootID := ctx.RootUser.Id

	// (a) tenant admin reads its OWN channel → success.
	c, w := v1Ctx(http.MethodGet, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(own.Id)}}
	GetChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("(a) own-tenant read must succeed, code=%d body=%s", w.Code, w.Body.String())
	}

	// (b) tenant admin reads ANOTHER tenant's channel → 403, no key leak.
	c, w = v1Ctx(http.MethodGet, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	GetChannel(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("(b) cross-tenant read must 403, got code=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), idorVictimChannelKey) {
		t.Errorf("(b) victim channel key leaked: %s", w.Body.String())
	}

	// (c) root reads ANY tenant's channel → success.
	c, w = v1Ctx(http.MethodGet, "/", nil, common.RoleRootUser, ctx.TenantID, rootID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	GetChannel(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("(c) root cross-tenant read must succeed, code=%d body=%s", w.Code, w.Body.String())
	}

	// (b-delete) tenant admin cannot delete another tenant's channel.
	c, w = v1Ctx(http.MethodDelete, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	DeleteChannel(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant delete must 403, got code=%d body=%s", w.Code, w.Body.String())
	}
	var count int64
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", victim.Id).Count(&count)
	if count != 1 {
		t.Errorf("victim channel deleted cross-tenant, count=%d want 1", count)
	}

	// (b-update) tenant admin cannot mutate another tenant's channel.
	c, w = v1Ctx(http.MethodPut, "/", map[string]interface{}{"id": victim.Id, "type": 1, "name": "hijacked"},
		common.RoleAdminUser, ctx.TenantID, adminID)
	UpdateChannel(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant update must 403, got code=%d body=%s", w.Code, w.Body.String())
	}
	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, victim.Id).Error; err != nil {
		t.Fatalf("reload victim: %v", err)
	}
	if reloaded.Name != "victim-channel" || reloaded.Key != idorVictimChannelKey {
		t.Errorf("victim channel mutated cross-tenant: name=%q key=%q", reloaded.Name, reloaded.Key)
	}
}

func TestV1Channel_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	SeedV2Channel(t, ctx, "own-channel") // ctx.TenantID
	seedV1VictimChannel(t, ctx)          // other-tenant-xyz

	// (d) tenant admin sees ONLY its own tenant's channel.
	c, w := v1Ctx(http.MethodGet, "/api/channel/?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllChannels(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("tenant admin channel total=%v want 1", got)
	}

	// root sees every tenant's channel.
	c, w = v1Ctx(http.MethodGet, "/api/channel/?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllChannels(c)
	data = v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("root channel total=%v want 2", got)
	}
}

// ─── User ───────────────────────────────────────────────────────────────────

func seedV1VictimUser(t *testing.T, ctx *V2TestContext) *repo.User {
	t.Helper()
	u := &repo.User{
		Username:    "victim-user",
		DisplayName: "Victim",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Email:       "victim@other.local",
		TenantId:    idorVictimTenant,
		Quota:       123,
	}
	if err := ctx.DB.Create(u).Error; err != nil {
		t.Fatalf("seed victim user: %v", err)
	}
	return u
}

func TestV1User_CrossTenantIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedV1VictimUser(t, ctx)
	adminID := ctx.AdminUser.Id
	rootID := ctx.RootUser.Id

	// (a) tenant admin reads a normal user in its OWN tenant → success.
	c, w := v1Ctx(http.MethodGet, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ctx.NormalUser.Id)}}
	GetUser(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(a) own-tenant user read must succeed, body=%s", w.Body.String())
	}

	// (b) tenant admin reads ANOTHER tenant's user → 403.
	c, w = v1Ctx(http.MethodGet, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	GetUser(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("(b) cross-tenant user read must 403, got code=%d body=%s", w.Code, w.Body.String())
	}

	// (c) root reads ANY tenant's user → success.
	c, w = v1Ctx(http.MethodGet, "/", nil, common.RoleRootUser, ctx.TenantID, rootID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	GetUser(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(c) root cross-tenant user read must succeed, body=%s", w.Body.String())
	}

	// (b-update) tenant admin cannot mutate another tenant's user.
	c, w = v1Ctx(http.MethodPut, "/", map[string]interface{}{
		"id": victim.Id, "username": "victim-user", "quota": 999999,
	}, common.RoleAdminUser, ctx.TenantID, adminID)
	UpdateUser(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant user update must 403, got code=%d body=%s", w.Code, w.Body.String())
	}
	var reloaded repo.User
	if err := ctx.DB.First(&reloaded, victim.Id).Error; err != nil {
		t.Fatalf("reload victim user: %v", err)
	}
	if reloaded.Quota != 123 {
		t.Errorf("victim user quota mutated cross-tenant: %d want 123", reloaded.Quota)
	}
}

func TestV1User_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedV1VictimUser(t, ctx)

	containsUserID := func(items []interface{}, id int) bool {
		for _, it := range items {
			if m, ok := it.(map[string]interface{}); ok {
				if idf, ok := m["id"].(float64); ok && int(idf) == id {
					return true
				}
			}
		}
		return false
	}

	// (d) tenant admin: victim (other tenant) must be absent; own users present.
	c, w := v1Ctx(http.MethodGet, "/api/user/?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllUsers(c)
	items := v1Items(t, w)
	if containsUserID(items, victim.Id) {
		t.Errorf("tenant admin user list leaked cross-tenant victim id=%d", victim.Id)
	}
	if !containsUserID(items, ctx.NormalUser.Id) {
		t.Errorf("tenant admin user list missing own-tenant user")
	}

	// root sees the victim too.
	c, w = v1Ctx(http.MethodGet, "/api/user/?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllUsers(c)
	if !containsUserID(v1Items(t, w), victim.Id) {
		t.Errorf("root user list must include every tenant")
	}
}

// ─── Redemption ─────────────────────────────────────────────────────────────

func seedV1Redemption(t *testing.T, ctx *V2TestContext, tenant string) *repo.Redemption {
	t.Helper()
	r := &repo.Redemption{
		UserId:      ctx.AdminUser.Id,
		TenantId:    tenant,
		Key:         common.GetRandomString(32),
		Name:        "redeem-" + tenant,
		Quota:       500,
		Status:      common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(r).Error; err != nil {
		t.Fatalf("seed redemption: %v", err)
	}
	return r
}

func TestV1Redemption_CrossTenantIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	own := seedV1Redemption(t, ctx, ctx.TenantID)
	victim := seedV1Redemption(t, ctx, idorVictimTenant)
	adminID := ctx.AdminUser.Id

	// (a) own-tenant read → success.
	c, w := v1Ctx(http.MethodGet, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(own.Id)}}
	GetRedemption(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(a) own redemption read must succeed, body=%s", w.Body.String())
	}

	// (b) cross-tenant read → 403.
	c, w = v1Ctx(http.MethodGet, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	GetRedemption(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("(b) cross-tenant redemption read must 403, got code=%d body=%s", w.Code, w.Body.String())
	}

	// (c) root read → success.
	c, w = v1Ctx(http.MethodGet, "/", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	GetRedemption(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(c) root cross-tenant redemption read must succeed, body=%s", w.Body.String())
	}

	// (b-delete) tenant admin cannot delete another tenant's redemption.
	c, w = v1Ctx(http.MethodDelete, "/", nil, common.RoleAdminUser, ctx.TenantID, adminID)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(victim.Id)}}
	DeleteRedemption(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant redemption delete must 403, got code=%d body=%s", w.Code, w.Body.String())
	}
	var count int64
	ctx.DB.Model(&repo.Redemption{}).Where("id = ?", victim.Id).Count(&count)
	if count != 1 {
		t.Errorf("victim redemption deleted cross-tenant, count=%d want 1", count)
	}
}

func TestV1Redemption_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedV1Redemption(t, ctx, ctx.TenantID)
	seedV1Redemption(t, ctx, idorVictimTenant)

	// (d) tenant admin → only own tenant's code.
	c, w := v1Ctx(http.MethodGet, "/api/redemption/?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllRedemptions(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("tenant admin redemption total=%v want 1", got)
	}

	// root → both.
	c, w = v1Ctx(http.MethodGet, "/api/redemption/?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllRedemptions(c)
	data = v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("root redemption total=%v want 2", got)
	}
}

// ─── Logs ───────────────────────────────────────────────────────────────────

func seedIDORLog(t *testing.T, ctx *V2TestContext, tenant string, userID int) {
	t.Helper()
	l := &repo.Log{
		UserId:    userID,
		TenantId:  tenant,
		Type:      2, // consume
		Username:  "log-" + tenant,
		ModelName: "gpt-4",
		Quota:     10,
		Group:     "default",
		CreatedAt: common.GetTimestamp(),
	}
	if err := ctx.DB.Table("logs").Create(l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

func TestV1Logs_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedIDORLog(t, ctx, ctx.TenantID, ctx.NormalUser.Id)
	seedIDORLog(t, ctx, idorVictimTenant, 4242)

	// (d) tenant admin → only own tenant's logs.
	c, w := v1Ctx(http.MethodGet, "/api/log/?p=1&page_size=50&type=0", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllLogs(c)
	if got := len(v1Items(t, w)); got != 1 {
		t.Errorf("tenant admin log count=%d want 1", got)
	}

	// root → every tenant's logs.
	c, w = v1Ctx(http.MethodGet, "/api/log/?p=1&page_size=50&type=0", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllLogs(c)
	if got := len(v1Items(t, w)); got != 2 {
		t.Errorf("root log count=%d want 2", got)
	}
}

func TestV1Logs_DeleteHistoryTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedIDORLog(t, ctx, ctx.TenantID, ctx.NormalUser.Id)
	seedIDORLog(t, ctx, idorVictimTenant, 4242)

	future := common.GetTimestamp() + 100000
	// A tenant admin's retention cleanup must not touch another tenant's logs.
	c, w := v1Ctx(http.MethodDelete, "/api/log/?target_timestamp="+strconv.FormatInt(future, 10), nil,
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	DeleteHistoryLogs(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("tenant admin delete-history must succeed for own tenant, body=%s", w.Body.String())
	}

	var victimCount int64
	ctx.DB.Table("logs").Where("tenant_id = ?", idorVictimTenant).Count(&victimCount)
	if victimCount != 1 {
		t.Errorf("tenant admin deleted another tenant's logs: victim count=%d want 1", victimCount)
	}
	var ownCount int64
	ctx.DB.Table("logs").Where("tenant_id = ?", ctx.TenantID).Count(&ownCount)
	if ownCount != 0 {
		t.Errorf("tenant admin's own logs not purged: own count=%d want 0", ownCount)
	}
}

// ─── Channel batch / tag operations ───────────────────────────────────────────
//
// The single-resource by-id handlers (GetChannel/DeleteChannel/UpdateChannel)
// gained enforceTenantScope earlier, but the *bulk* handlers reached over their
// tenant boundary: DeleteChannelBatch (by id array), DisableTagChannels /
// EnableTagChannels / EditTagChannels (by tag string), DeleteDisabledChannel
// (all disabled) and BatchSetChannelTag (by id array). A tenant admin could
// delete, disable, enable, re-tag or prune another tenant's channels — the id
// array or the tag string is fully attacker-controlled. These tests assert the
// same (a)/(b)/(c) contract: own works, victim untouched, root global.

// seedV1TaggedChannel seeds a channel in an arbitrary tenant with an optional
// tag and an explicit status, for the batch/tag cross-tenant regression tests.
func seedV1TaggedChannel(t *testing.T, ctx *V2TestContext, tenant, name, tag string, status int) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name:        name,
		TenantId:    tenant,
		Key:         "sk-" + common.GetRandomString(20),
		Status:      status,
		Type:        1,
		Models:      "gpt-4",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if tag != "" {
		tg := tag
		ch.Tag = &tg
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed tagged channel: %v", err)
	}
	return ch
}

func v1ChannelCount(ctx *V2TestContext, id int) int64 {
	var n int64
	ctx.DB.Model(&repo.Channel{}).Where("id = ?", id).Count(&n)
	return n
}

func v1ReloadChannel(t *testing.T, ctx *V2TestContext, id int) repo.Channel {
	t.Helper()
	var ch repo.Channel
	if err := ctx.DB.First(&ch, id).Error; err != nil {
		t.Fatalf("reload channel %d: %v", id, err)
	}
	return ch
}

func TestV1Channel_DeleteBatchTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	own := SeedV2Channel(t, ctx, "own-batch")
	victim := seedV1VictimChannel(t, ctx)

	// (a)+(b) tenant admin batch-deletes a mixed id set: only its own channel is
	// removed, the victim survives.
	c, w := v1Ctx(http.MethodPost, "/api/channel/batch",
		map[string]interface{}{"ids": []int{own.Id, victim.Id}},
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	DeleteChannelBatch(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("(a) tenant admin batch delete must succeed, code=%d body=%s", w.Code, w.Body.String())
	}
	if got := v1ChannelCount(ctx, own.Id); got != 0 {
		t.Errorf("(a) own channel not deleted, count=%d want 0", got)
	}
	if got := v1ChannelCount(ctx, victim.Id); got != 1 {
		t.Errorf("(b) victim channel deleted cross-tenant, count=%d want 1", got)
	}

	// (c) root batch-delete reaches every tenant.
	c, _ = v1Ctx(http.MethodPost, "/api/channel/batch",
		map[string]interface{}{"ids": []int{victim.Id}},
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	DeleteChannelBatch(c)
	if got := v1ChannelCount(ctx, victim.Id); got != 0 {
		t.Errorf("(c) root batch delete must remove victim, count=%d want 0", got)
	}
}

func TestV1Channel_BatchSetTagTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	own := SeedV2Channel(t, ctx, "own-settag")
	victim := seedV1VictimChannel(t, ctx) // no tag

	// (a)+(b) tenant admin sets a tag across a mixed id set: only its own channel
	// is retagged, the victim's tag is untouched.
	c, w := v1Ctx(http.MethodPost, "/api/channel/batch/tag",
		map[string]interface{}{"ids": []int{own.Id, victim.Id}, "tag": "hijack-tag"},
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	BatchSetChannelTag(c)
	if w.Code != http.StatusOK || v1Body(t, w)["success"] != true {
		t.Fatalf("(a) tenant admin batch set tag must succeed, code=%d body=%s", w.Code, w.Body.String())
	}
	if reloaded := v1ReloadChannel(t, ctx, own.Id); reloaded.Tag == nil || *reloaded.Tag != "hijack-tag" {
		t.Errorf("(a) own channel tag not set: %v", reloaded.Tag)
	}
	if reloaded := v1ReloadChannel(t, ctx, victim.Id); reloaded.Tag != nil {
		t.Errorf("(b) victim channel tag mutated cross-tenant: %v", *reloaded.Tag)
	}

	// (c) root retags every tenant.
	c, _ = v1Ctx(http.MethodPost, "/api/channel/batch/tag",
		map[string]interface{}{"ids": []int{victim.Id}, "tag": "root-tag"},
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	BatchSetChannelTag(c)
	if reloaded := v1ReloadChannel(t, ctx, victim.Id); reloaded.Tag == nil || *reloaded.Tag != "root-tag" {
		t.Errorf("(c) root batch set tag must retag victim: %v", reloaded.Tag)
	}
}

func TestV1Channel_DisableEnableTagTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const sharedTag = "shared-tag"
	own := seedV1TaggedChannel(t, ctx, ctx.TenantID, "own-tagged", sharedTag, common.ChannelStatusEnabled)
	victim := seedV1TaggedChannel(t, ctx, idorVictimTenant, "victim-tagged", sharedTag, common.ChannelStatusEnabled)

	// (a)+(b) tenant admin disables the shared tag: only its own channel flips;
	// the victim (same tag string, other tenant) stays enabled.
	c, w := v1Ctx(http.MethodPost, "/api/channel/tag/disabled",
		map[string]interface{}{"tag": sharedTag},
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	DisableTagChannels(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(a) tenant admin disable-tag must succeed, body=%s", w.Body.String())
	}
	if got := v1ReloadChannel(t, ctx, own.Id).Status; got != common.ChannelStatusManuallyDisabled {
		t.Errorf("(a) own channel not disabled, status=%d", got)
	}
	if got := v1ReloadChannel(t, ctx, victim.Id).Status; got != common.ChannelStatusEnabled {
		t.Errorf("(b) victim channel disabled cross-tenant, status=%d want enabled", got)
	}

	// (c) root disables every tenant's channel sharing the tag.
	c, _ = v1Ctx(http.MethodPost, "/api/channel/tag/disabled",
		map[string]interface{}{"tag": sharedTag},
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	DisableTagChannels(c)
	if got := v1ReloadChannel(t, ctx, victim.Id).Status; got != common.ChannelStatusManuallyDisabled {
		t.Errorf("(c) root disable-tag must disable victim, status=%d", got)
	}

	// Enable path: the victim is now disabled; a tenant admin enabling the shared
	// tag must re-enable only its own channel.
	c, w = v1Ctx(http.MethodPost, "/api/channel/tag/enabled",
		map[string]interface{}{"tag": sharedTag},
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	EnableTagChannels(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("tenant admin enable-tag must succeed, body=%s", w.Body.String())
	}
	if got := v1ReloadChannel(t, ctx, own.Id).Status; got != common.ChannelStatusEnabled {
		t.Errorf("own channel not re-enabled, status=%d", got)
	}
	if got := v1ReloadChannel(t, ctx, victim.Id).Status; got != common.ChannelStatusManuallyDisabled {
		t.Errorf("victim channel enabled cross-tenant, status=%d want disabled", got)
	}
}

func TestV1Channel_EditTagTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const sharedTag = "edit-tag"
	own := seedV1TaggedChannel(t, ctx, ctx.TenantID, "own-edit", sharedTag, common.ChannelStatusEnabled)
	victim := seedV1TaggedChannel(t, ctx, idorVictimTenant, "victim-edit", sharedTag, common.ChannelStatusEnabled)

	// (a)+(b) tenant admin renames the shared tag: only its own channel is
	// renamed, the victim keeps the original tag.
	c, w := v1Ctx(http.MethodPut, "/api/channel/tag",
		map[string]interface{}{"tag": sharedTag, "new_tag": "own-only-tag"},
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	EditTagChannels(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(a) tenant admin edit-tag must succeed, body=%s", w.Body.String())
	}
	if reloaded := v1ReloadChannel(t, ctx, own.Id); reloaded.Tag == nil || *reloaded.Tag != "own-only-tag" {
		t.Errorf("(a) own channel tag not renamed: %v", reloaded.Tag)
	}
	if reloaded := v1ReloadChannel(t, ctx, victim.Id); reloaded.Tag == nil || *reloaded.Tag != sharedTag {
		t.Errorf("(b) victim channel tag mutated cross-tenant: %v", reloaded.Tag)
	}

	// (c) root renames every tenant's channel sharing the tag.
	c, _ = v1Ctx(http.MethodPut, "/api/channel/tag",
		map[string]interface{}{"tag": sharedTag, "new_tag": "root-tag"},
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	EditTagChannels(c)
	if reloaded := v1ReloadChannel(t, ctx, victim.Id); reloaded.Tag == nil || *reloaded.Tag != "root-tag" {
		t.Errorf("(c) root edit-tag must rename victim: %v", reloaded.Tag)
	}
}

func TestV1Channel_DeleteDisabledTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ownDisabled := seedV1TaggedChannel(t, ctx, ctx.TenantID, "own-disabled", "", common.ChannelStatusManuallyDisabled)
	ownEnabled := seedV1TaggedChannel(t, ctx, ctx.TenantID, "own-enabled", "", common.ChannelStatusEnabled)
	victimDisabled := seedV1TaggedChannel(t, ctx, idorVictimTenant, "victim-disabled", "", common.ChannelStatusManuallyDisabled)

	// (a)+(b) tenant admin prunes disabled channels: only its own disabled channel
	// is removed; the victim's disabled channel and its own enabled one survive.
	c, w := v1Ctx(http.MethodDelete, "/api/channel/disabled", nil,
		common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	DeleteDisabledChannel(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("(a) tenant admin delete-disabled must succeed, body=%s", w.Body.String())
	}
	if got := v1ChannelCount(ctx, ownDisabled.Id); got != 0 {
		t.Errorf("(a) own disabled channel not pruned, count=%d want 0", got)
	}
	if got := v1ChannelCount(ctx, ownEnabled.Id); got != 1 {
		t.Errorf("own enabled channel wrongly pruned, count=%d want 1", got)
	}
	if got := v1ChannelCount(ctx, victimDisabled.Id); got != 1 {
		t.Errorf("(b) victim disabled channel pruned cross-tenant, count=%d want 1", got)
	}

	// (c) root prunes every tenant's disabled channels.
	c, _ = v1Ctx(http.MethodDelete, "/api/channel/disabled", nil,
		common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	DeleteDisabledChannel(c)
	if got := v1ChannelCount(ctx, victimDisabled.Id); got != 0 {
		t.Errorf("(c) root delete-disabled must prune victim, count=%d want 0", got)
	}
}

// ─── Task / Midjourney (cycle-11 L8) ───────────────────────────────────────
//
// GET /api/task/ and GET /api/mj/ (both AdminAuth-gated, same as channel/
// user/redemption above) built their query with no tenant filter at all —
// a tenant admin could enumerate every tenant's async render/generation
// jobs. SetupV2TestRouter does not migrate the task/midjourney tables (they
// are not part of its route table), so these two tests extend ctx.DB with
// them directly instead of touching that shared fixture file.

func seedV1VictimTask(t *testing.T, ctx *V2TestContext) *repo.Task {
	t.Helper()
	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate Task: %v", err)
	}
	victimUser := seedV1VictimUser(t, ctx)
	task := &repo.Task{
		TaskID: "victim-task", Platform: constant.TaskPlatformSuno,
		UserId: victimUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%",
	}
	if err := ctx.DB.Create(task).Error; err != nil {
		t.Fatalf("seed victim task: %v", err)
	}
	return task
}

func TestV1Task_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate Task: %v", err)
	}

	ownTask := &repo.Task{
		TaskID: "own-task", Platform: constant.TaskPlatformSuno,
		UserId: ctx.AdminUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%",
	}
	if err := ctx.DB.Create(ownTask).Error; err != nil {
		t.Fatalf("seed own task: %v", err)
	}
	seedV1VictimTask(t, ctx) // other-tenant-xyz

	containsTaskID := func(items []interface{}, taskID string) bool {
		for _, it := range items {
			if m, ok := it.(map[string]interface{}); ok && m["task_id"] == taskID {
				return true
			}
		}
		return false
	}

	// (d) tenant admin sees ONLY its own tenant's task — both the reported
	// total AND the actual rows (a filter that only narrows total() while
	// leaving the row query unscoped would still pass a total-only check).
	c, w := v1Ctx(http.MethodGet, "/api/task/?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllTask(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("tenant admin task total=%v want 1", got)
	}
	items := data["items"].([]interface{})
	if !containsTaskID(items, "own-task") {
		t.Errorf("tenant admin task list missing own-tenant task, items=%v", items)
	}
	if containsTaskID(items, "victim-task") {
		t.Errorf("tenant admin task list leaked cross-tenant victim task, items=%v", items)
	}

	// root sees every tenant's task.
	c, w = v1Ctx(http.MethodGet, "/api/task/?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllTask(c)
	data = v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("root task total=%v want 2", got)
	}
	items = data["items"].([]interface{})
	if !containsTaskID(items, "victim-task") {
		t.Errorf("root task list must include every tenant, items=%v", items)
	}
}

// TestV1Task_ListTenantScoped_EmptyTenantFailsClosed: a non-root admin whose
// session carries no tenant (c.GetString("tenant_id") == "", e.g. a session
// resolved before the tenant lookup populated it) must see an EMPTY page,
// not the whole platform. Before the TenantScoped flag this fell through
// `if queryParams.TenantID != ""` and returned every tenant's rows —
// fail-open. Mirrors GetAllChannels, which applies `WHERE tenant_id = ""`
// unconditionally for any non-root caller and likewise matches nothing.
func TestV1Task_ListTenantScoped_EmptyTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate Task: %v", err)
	}

	ownTask := &repo.Task{
		TaskID: "own-task-empty-tenant", Platform: constant.TaskPlatformSuno,
		UserId: ctx.AdminUser.Id, ChannelId: 1, Status: repo.TaskStatusSuccess, Progress: "100%",
	}
	if err := ctx.DB.Create(ownTask).Error; err != nil {
		t.Fatalf("seed own task: %v", err)
	}
	seedV1VictimTask(t, ctx) // other-tenant-xyz

	c, w := v1Ctx(http.MethodGet, "/api/task/?p=1&page_size=50", nil, common.RoleAdminUser, "", ctx.AdminUser.Id)
	GetAllTask(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 0 {
		t.Errorf("non-root admin with empty tenant task total=%v want 0 (fail-closed)", got)
	}
	items, _ := data["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("non-root admin with empty tenant task items=%v want empty", items)
	}
}

func seedV1VictimMidjourney(t *testing.T, ctx *V2TestContext) *repo.Midjourney {
	t.Helper()
	if err := ctx.DB.AutoMigrate(&repo.Midjourney{}); err != nil {
		t.Fatalf("migrate Midjourney: %v", err)
	}
	victimUser := seedV1VictimUser(t, ctx)
	mj := &repo.Midjourney{MjId: "victim-mj", UserId: victimUser.Id, Status: "SUCCESS", Progress: "100%"}
	if err := ctx.DB.Create(mj).Error; err != nil {
		t.Fatalf("seed victim mj: %v", err)
	}
	return mj
}

func TestV1Midjourney_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.Midjourney{}); err != nil {
		t.Fatalf("migrate Midjourney: %v", err)
	}

	ownMJ := &repo.Midjourney{MjId: "own-mj", UserId: ctx.AdminUser.Id, Status: "SUCCESS", Progress: "100%"}
	if err := ctx.DB.Create(ownMJ).Error; err != nil {
		t.Fatalf("seed own mj: %v", err)
	}
	seedV1VictimMidjourney(t, ctx) // other-tenant-xyz

	containsMjID := func(items []interface{}, mjID string) bool {
		for _, it := range items {
			if m, ok := it.(map[string]interface{}); ok && m["mj_id"] == mjID {
				return true
			}
		}
		return false
	}

	// (d) tenant admin sees ONLY its own tenant's mj job — both the
	// reported total AND the actual rows.
	c, w := v1Ctx(http.MethodGet, "/api/mj/?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllMidjourney(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("tenant admin mj total=%v want 1", got)
	}
	items := data["items"].([]interface{})
	if !containsMjID(items, "own-mj") {
		t.Errorf("tenant admin mj list missing own-tenant job, items=%v", items)
	}
	if containsMjID(items, "victim-mj") {
		t.Errorf("tenant admin mj list leaked cross-tenant victim job, items=%v", items)
	}

	// root sees every tenant's mj job.
	c, w = v1Ctx(http.MethodGet, "/api/mj/?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllMidjourney(c)
	data = v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("root mj total=%v want 2", got)
	}
	items = data["items"].([]interface{})
	if !containsMjID(items, "victim-mj") {
		t.Errorf("root mj list must include every tenant, items=%v", items)
	}
}

// TestV1Midjourney_ListTenantScoped_EmptyTenantFailsClosed mirrors
// TestV1Task_ListTenantScoped_EmptyTenantFailsClosed for the Midjourney
// list: a non-root admin with no tenant on its session gets an empty page,
// not the platform's.
func TestV1Midjourney_ListTenantScoped_EmptyTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.Midjourney{}); err != nil {
		t.Fatalf("migrate Midjourney: %v", err)
	}

	ownMJ := &repo.Midjourney{MjId: "own-mj-empty-tenant", UserId: ctx.AdminUser.Id, Status: "SUCCESS", Progress: "100%"}
	if err := ctx.DB.Create(ownMJ).Error; err != nil {
		t.Fatalf("seed own mj: %v", err)
	}
	seedV1VictimMidjourney(t, ctx) // other-tenant-xyz

	c, w := v1Ctx(http.MethodGet, "/api/mj/?p=1&page_size=50", nil, common.RoleAdminUser, "", ctx.AdminUser.Id)
	GetAllMidjourney(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 0 {
		t.Errorf("non-root admin with empty tenant mj total=%v want 0 (fail-closed)", got)
	}
	items, _ := data["items"].([]interface{})
	if len(items) != 0 {
		t.Errorf("non-root admin with empty tenant mj items=%v want empty", items)
	}
}

// ─── Search variants (cycle-11 L8) ─────────────────────────────────────────
//
// SearchChannels/SearchUsers/SearchRedemptions already carry the same
// isRoot/callerTenant scope as their List siblings above; these lock that
// existing behaviour through the real handler with an empty keyword (the
// "browse everything" case), matching the *_ListTenantScoped naming
// convention above so a name-based route/test-pairing gate finds all of
// list+search by pattern.

func TestV1ChannelSearch_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	SeedV2Channel(t, ctx, "own-search-channel") // ctx.TenantID
	victim := seedV1VictimChannel(t, ctx)       // other-tenant-xyz

	containsChannelID := func(items []interface{}, id int) bool {
		for _, it := range items {
			if m, ok := it.(map[string]interface{}); ok {
				if idf, ok := m["id"].(float64); ok && int(idf) == id {
					return true
				}
			}
		}
		return false
	}

	c, w := v1Ctx(http.MethodGet, "/api/channel/search?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	SearchChannels(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("tenant admin channel search total=%v want 1", got)
	}
	items, _ := data["items"].([]interface{})
	if containsChannelID(items, victim.Id) {
		t.Errorf("tenant admin channel search leaked cross-tenant victim channel id=%d, items=%v", victim.Id, items)
	}

	c, w = v1Ctx(http.MethodGet, "/api/channel/search?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	SearchChannels(c)
	data = v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("root channel search total=%v want 2", got)
	}
	items, _ = data["items"].([]interface{})
	if !containsChannelID(items, victim.Id) {
		t.Errorf("root channel search must include every tenant, missing victim channel id=%d, items=%v", victim.Id, items)
	}
}

func TestV1UserSearch_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedV1VictimUser(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/user/search?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	SearchUsers(c)
	items := v1Items(t, w)
	for _, it := range items {
		if m, ok := it.(map[string]interface{}); ok {
			if idf, ok := m["id"].(float64); ok && int(idf) == victim.Id {
				t.Errorf("tenant admin user search leaked cross-tenant victim id=%d", victim.Id)
			}
		}
	}

	c, w = v1Ctx(http.MethodGet, "/api/user/search?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	SearchUsers(c)
	found := false
	for _, it := range v1Items(t, w) {
		if m, ok := it.(map[string]interface{}); ok {
			if idf, ok := m["id"].(float64); ok && int(idf) == victim.Id {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("root user search must include every tenant")
	}
}

func TestV1RedemptionSearch_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedV1Redemption(t, ctx, ctx.TenantID)
	victim := seedV1Redemption(t, ctx, idorVictimTenant)

	containsRedemptionID := func(items []interface{}, id int) bool {
		for _, it := range items {
			if m, ok := it.(map[string]interface{}); ok {
				if idf, ok := m["id"].(float64); ok && int(idf) == id {
					return true
				}
			}
		}
		return false
	}

	c, w := v1Ctx(http.MethodGet, "/api/redemption/search?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	SearchRedemptions(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("tenant admin redemption search total=%v want 1", got)
	}
	items, _ := data["items"].([]interface{})
	if containsRedemptionID(items, victim.Id) {
		t.Errorf("tenant admin redemption search leaked cross-tenant victim redemption id=%d, items=%v", victim.Id, items)
	}

	c, w = v1Ctx(http.MethodGet, "/api/redemption/search?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	SearchRedemptions(c)
	data = v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("root redemption search total=%v want 2", got)
	}
	items, _ = data["items"].([]interface{})
	if !containsRedemptionID(items, victim.Id) {
		t.Errorf("root redemption search must include every tenant, missing victim redemption id=%d, items=%v", victim.Id, items)
	}
}

// ─── Model discovery (cycle-12 plan §L5) ────────────────────────────────────
//
// The four v1 discovery endpoints below answered from the whole abilities /
// channels table regardless of the caller's tenant, so a tenant admin could
// enumerate another tenant's model names — including private fine-tune ids —
// without ever addressing that tenant's resources by id.

// seedV1DiscoveryChannel seeds one channel plus one enabled ability row, the
// pair repo.GetEnabledModels / GetGroupEnabledModels answer from.
func seedV1DiscoveryChannel(t *testing.T, ctx *V2TestContext, tenant, name, model, group string) *repo.Channel {
	t.Helper()
	ch := &repo.Channel{
		Name:        name,
		TenantId:    tenant,
		Key:         "sk-" + name,
		Status:      common.ChannelStatusEnabled,
		Type:        1,
		Models:      model,
		Group:       group,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed discovery channel %s: %v", name, err)
	}
	if err := ctx.DB.Create(&repo.Ability{
		Group: group, Model: model, ChannelId: ch.Id, Enabled: true,
	}).Error; err != nil {
		t.Fatalf("seed discovery ability %s: %v", model, err)
	}
	return ch
}

// seedV1DiscoveryTrio seeds the shared / own / victim triple every discovery
// test below asserts against and returns the three model names.
func seedV1DiscoveryTrio(t *testing.T, ctx *V2TestContext) (shared, own, victim string) {
	t.Helper()
	shared, own, victim = "disc-shared-model", "disc-own-model", "disc-victim-model"
	seedV1DiscoveryChannel(t, ctx, "default", "disc-shared", shared, "default")
	seedV1DiscoveryChannel(t, ctx, ctx.TenantID, "disc-own", own, "default")
	seedV1DiscoveryChannel(t, ctx, idorVictimTenant, "disc-victim", victim, "default")
	return shared, own, victim
}

func v1StringList(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	raw, _ := v1Body(t, w)["data"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func v1ListHas(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// GET /api/user/models — the caller's own model list.
func TestV1UserModels_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	shared, own, victim := seedV1DiscoveryTrio(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/user/models", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetUserModels(c)
	got := v1StringList(t, w)
	for _, want := range []string{shared, own} {
		if !v1ListHas(got, want) {
			t.Errorf("tenant admin /api/user/models missing %q: %v", want, got)
		}
	}
	if v1ListHas(got, victim) {
		t.Errorf("tenant admin /api/user/models leaked cross-tenant model %q: %v", victim, got)
	}

	c, w = v1Ctx(http.MethodGet, "/api/user/models", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetUserModels(c)
	if got = v1StringList(t, w); !v1ListHas(got, victim) {
		t.Errorf("root /api/user/models must keep the global view, missing %q: %v", victim, got)
	}
}

// GET /api/channel/models_enabled — the channel editor's "which models are
// live" picker.
func TestV1ModelsEnabled_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	shared, own, victim := seedV1DiscoveryTrio(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/channel/models_enabled", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	EnabledListModels(c)
	got := v1StringList(t, w)
	for _, want := range []string{shared, own} {
		if !v1ListHas(got, want) {
			t.Errorf("tenant admin models_enabled missing %q: %v", want, got)
		}
	}
	if v1ListHas(got, victim) {
		t.Errorf("tenant admin models_enabled leaked cross-tenant model %q: %v", victim, got)
	}

	c, w = v1Ctx(http.MethodGet, "/api/channel/models_enabled", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	EnabledListModels(c)
	if got = v1StringList(t, w); !v1ListHas(got, victim) {
		t.Errorf("root models_enabled must keep the global view, missing %q: %v", victim, got)
	}
}

// GET /api/models/missing — "which live model names have no metadata row".
func TestV1MissingModels_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	// SetupV2TestRouter does not migrate the models metadata table; this
	// endpoint subtracts it from the enabled set, so it must exist.
	if err := ctx.DB.AutoMigrate(&repo.Model{}); err != nil {
		t.Fatalf("migrate models table: %v", err)
	}
	shared, own, victim := seedV1DiscoveryTrio(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/models/missing", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetMissingModels(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("tenant admin missing-models call failed: %s", w.Body.String())
	}
	got := v1StringList(t, w)
	for _, want := range []string{shared, own} {
		if !v1ListHas(got, want) {
			t.Errorf("tenant admin missing-models missing %q: %v", want, got)
		}
	}
	if v1ListHas(got, victim) {
		t.Errorf("tenant admin missing-models leaked cross-tenant model %q: %v", victim, got)
	}

	c, w = v1Ctx(http.MethodGet, "/api/models/missing", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetMissingModels(c)
	if got = v1StringList(t, w); !v1ListHas(got, victim) {
		t.Errorf("root missing-models must keep the global view, missing %q: %v", victim, got)
	}
}

// GET /api/channel/tag/models?tag=… — any tag string was readable, so a
// tenant admin who guessed (or read from a shared naming convention) another
// tenant's tag got that tenant's longest model list back.
func TestV1ChannelTagModels_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const victimTag = "victim-only-tag"
	const ownTag = "own-only-tag"
	victim := seedV1TaggedChannel(t, ctx, idorVictimTenant, "tagmodels-victim", victimTag, common.ChannelStatusEnabled)
	victim.Models = "victim-secret-model,victim-secret-model-2"
	if err := ctx.DB.Save(victim).Error; err != nil {
		t.Fatalf("set victim models: %v", err)
	}
	own := seedV1TaggedChannel(t, ctx, ctx.TenantID, "tagmodels-own", ownTag, common.ChannelStatusEnabled)
	own.Models = "own-model-a,own-model-b"
	if err := ctx.DB.Save(own).Error; err != nil {
		t.Fatalf("set own models: %v", err)
	}

	c, w := v1Ctx(http.MethodGet, "/api/channel/tag/models?tag="+victimTag, nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetTagModels(c)
	if got, _ := v1Body(t, w)["data"].(string); got != "" {
		t.Errorf("tenant admin read another tenant's tag model list: %q", got)
	}

	c, w = v1Ctx(http.MethodGet, "/api/channel/tag/models?tag="+ownTag, nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetTagModels(c)
	if got, _ := v1Body(t, w)["data"].(string); got != "own-model-a,own-model-b" {
		t.Errorf("tenant admin own-tag model list = %q, want the own channel's models", got)
	}

	c, w = v1Ctx(http.MethodGet, "/api/channel/tag/models?tag="+victimTag, nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetTagModels(c)
	if got, _ := v1Body(t, w)["data"].(string); got != "victim-secret-model,victim-secret-model-2" {
		t.Errorf("root tag model list = %q, want the victim channel's models (root keeps the global view)", got)
	}
}

// v1ChannelNames pulls the channel names out of a GetAllChannels page.
func v1ChannelNames(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	items := v1Items(t, w)
	names := make([]string, 0, len(items))
	for _, it := range items {
		row, _ := it.(map[string]interface{})
		if row == nil {
			continue
		}
		if name, ok := row["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// GET /api/channel/?tag_mode=true — the tag-grouped channel list. The rows
// were already filtered by tenant (channel.go's per-row filter) but the page
// of tags and the total were not, so a tenant admin paged through other
// tenants' tag names and saw a total that counted them.
//
// The page assertion is what pins the PAGE half: the victim's tag is seeded
// first AND sorts first, so with the tenant-blind repo.GetPaginatedTags a
// page of one holds the victim's tag, every row under it is filtered away,
// and the caller's own channel never appears. Both halves (page + total) must
// therefore be scoped for this to stay green.
func TestV1ChannelTagMode_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const victimTag = "aaa-victim-tag"
	const ownTag = "zzz-own-tag"
	seedV1TaggedChannel(t, ctx, idorVictimTenant, "tagmode-victim", victimTag, common.ChannelStatusEnabled)
	seedV1TaggedChannel(t, ctx, ctx.TenantID, "tagmode-own", ownTag, common.ChannelStatusEnabled)

	c, w := v1Ctx(http.MethodGet, "/api/channel/?p=1&page_size=1&tag_mode=true", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllChannels(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 1 {
		t.Errorf("tenant admin tag_mode total=%v want 1 (only its own tag)", got)
	}
	if got := v1ChannelNames(t, w); len(got) != 1 || got[0] != "tagmode-own" {
		t.Errorf("tenant admin tag_mode page = %v, want exactly [tagmode-own] — "+
			"an unscoped page of tags spends the window on another tenant's tag", got)
	}

	c, w = v1Ctx(http.MethodGet, "/api/channel/?p=1&page_size=50&tag_mode=true", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllChannels(c)
	data = v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 2 {
		t.Errorf("root tag_mode total=%v want 2 (global view)", got)
	}
	if got := v1ChannelNames(t, w); len(got) != 2 {
		t.Errorf("root tag_mode page = %v, want both tenants' channels", got)
	}
}

// ─── Model catalogue metadata (cycle-12 L5 repair) ──────────────────────────
//
// GET /api/models/, /search and /:id answer from the global models table, but
// the per-row data enrichModels attaches came from the channels/abilities
// tables with no tenant predicate: bound_channels published every tenant's
// channel NAME (operators name channels after the account) and enable_groups
// published every tenant's private group names — the same two disclosures
// this cycle removed from the price list.

const (
	metaCommonModel     = "meta-common-model"
	metaVictimOnlyModel = "meta-victim-only-model"
	metaOrphanModel     = "meta-orphan-metadata-model"
	metaVictimChannel   = "meta-victim-channel-SECRET"
	metaSharedChannel   = "meta-shared-channel"
	metaVictimGroup     = "victim-vip"
)

// seedV1ModelMetaFixture seeds one shared and one victim-tenant channel that
// serve the SAME model (so bound_channels/enable_groups have something to
// leak), one model only the victim serves, plus the models-table rows the
// enrichment hangs off. It also drops the process-wide pricing cache, which
// the enrichment reads.
func seedV1ModelMetaFixture(t *testing.T, ctx *V2TestContext) {
	t.Helper()
	if err := ctx.DB.AutoMigrate(&repo.Model{}); err != nil {
		t.Fatalf("migrate models table: %v", err)
	}
	seedV1DiscoveryChannel(t, ctx, "default", metaSharedChannel, metaCommonModel, "default")
	seedV1DiscoveryChannel(t, ctx, idorVictimTenant, metaVictimChannel, metaCommonModel, metaVictimGroup)
	seedV1DiscoveryChannel(t, ctx, idorVictimTenant, "meta-victim-private", metaVictimOnlyModel, metaVictimGroup)
	for _, name := range []string{metaCommonModel, metaVictimOnlyModel, metaOrphanModel} {
		if err := ctx.DB.Create(&repo.Model{
			ModelName: name, NameRule: repo.NameRuleExact, Status: 1,
			CreatedTime: common.GetTimestamp(),
		}).Error; err != nil {
			t.Fatalf("seed models-table row %q: %v", name, err)
		}
	}
	repo.InvalidatePricingCache()
	t.Cleanup(repo.InvalidatePricingCache)
}

// v1ModelMetaItem finds one model row in a GET /api/models/ page.
func v1ModelMetaItem(t *testing.T, w *httptest.ResponseRecorder, modelName string) map[string]interface{} {
	t.Helper()
	body := v1Body(t, w)
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data missing/wrong type, body: %s", w.Body.String())
	}
	items, _ := data["items"].([]interface{})
	for _, it := range items {
		row, _ := it.(map[string]interface{})
		if row != nil && row["model_name"] == modelName {
			return row
		}
	}
	t.Fatalf("model %q missing from the page, body: %s", modelName, w.Body.String())
	return nil
}

func v1BoundChannelNames(row map[string]interface{}) []string {
	raw, _ := row["bound_channels"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		ch, _ := v.(map[string]interface{})
		if ch == nil {
			continue
		}
		if name, ok := ch["name"].(string); ok {
			out = append(out, name)
		}
	}
	return out
}

func v1RowStrings(row map[string]interface{}, key string) []string {
	raw, _ := row[key].([]interface{})
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// GET /api/models/ — bound_channels and enable_groups narrowed to the
// caller's own channels; root keeps the platform-wide answer.
func TestV1ModelsMeta_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedV1ModelMetaFixture(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/models/?p=1&page_size=50", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetAllModelsMeta(c)
	row := v1ModelMetaItem(t, w, metaCommonModel)
	channels := v1BoundChannelNames(row)
	if !v1ListHas(channels, metaSharedChannel) {
		t.Errorf("tenant admin lost the shared channel from bound_channels: %v", channels)
	}
	if v1ListHas(channels, metaVictimChannel) {
		t.Errorf("GET /api/models/ leaked another tenant's channel name: %v", channels)
	}
	groups := v1RowStrings(row, "enable_groups")
	if !v1ListHas(groups, "default") {
		t.Errorf("tenant admin lost its own group from enable_groups: %v", groups)
	}
	if v1ListHas(groups, metaVictimGroup) {
		t.Errorf("GET /api/models/ leaked another tenant's group name: %v", groups)
	}

	c, w = v1Ctx(http.MethodGet, "/api/models/?p=1&page_size=50", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetAllModelsMeta(c)
	row = v1ModelMetaItem(t, w, metaCommonModel)
	if channels = v1BoundChannelNames(row); !v1ListHas(channels, metaVictimChannel) {
		t.Errorf("root must keep the platform-wide bound_channels: %v", channels)
	}
	if groups = v1RowStrings(row, "enable_groups"); !v1ListHas(groups, metaVictimGroup) {
		t.Errorf("root must keep the platform-wide enable_groups: %v", groups)
	}
}

// GET /api/models/pricing_info — a model only another tenant's channels serve
// is dropped for a non-root caller; platform metadata with no channel at all
// stays, and root sees everything.
func TestV1ModelsPricingInfo_ListTenantScoped(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	seedV1ModelMetaFixture(t, ctx)

	names := func(w *httptest.ResponseRecorder) []string {
		rows, _ := v1Body(t, w)["data"].([]interface{})
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			row, _ := r.(map[string]interface{})
			if row == nil {
				continue
			}
			if n, ok := row["model_name"].(string); ok {
				out = append(out, n)
			}
		}
		return out
	}

	c, w := v1Ctx(http.MethodGet, "/api/models/pricing_info", nil, common.RoleAdminUser, ctx.TenantID, ctx.AdminUser.Id)
	GetModelsPricingInfo(c)
	got := names(w)
	for _, want := range []string{metaCommonModel, metaOrphanModel} {
		if !v1ListHas(got, want) {
			t.Errorf("tenant admin pricing_info missing %q: %v", want, got)
		}
	}
	if v1ListHas(got, metaVictimOnlyModel) {
		t.Errorf("pricing_info leaked a model only another tenant's channels serve: %v", got)
	}

	c, w = v1Ctx(http.MethodGet, "/api/models/pricing_info", nil, common.RoleRootUser, ctx.TenantID, ctx.RootUser.Id)
	GetModelsPricingInfo(c)
	if got = names(w); !v1ListHas(got, metaVictimOnlyModel) {
		t.Errorf("root pricing_info must list every models-table row: %v", got)
	}
}

// ─── Fail-closed: a non-root session with no tenant on it ───────────────────
//
// repo.abilityTenantScope and repo.getChannelsByTagScoped both define the
// empty tenant id as "apply no filter" — the contract their tenant-blind
// callers rely on. handler.tenantScopeForDiscovery therefore narrows a
// non-root caller with a blank session tenant to "default" (the shared pool)
// instead of passing "" through. Deleting that two-line fallback is the
// cheapest way to make a stubborn discovery test pass, so each of the five
// scoped endpoints pins it here, the same way the task and midjourney lists
// above do.

// GET /api/user/models with no tenant on the session.
func TestV1UserModels_ListTenantScoped_EmptyTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	shared, own, victim := seedV1DiscoveryTrio(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/user/models", nil, common.RoleAdminUser, "", ctx.AdminUser.Id)
	GetUserModels(c)
	got := v1StringList(t, w)
	if !v1ListHas(got, shared) {
		t.Errorf("blank-tenant admin must still see the shared catalogue, missing %q: %v", shared, got)
	}
	for _, leaked := range []string{own, victim} {
		if v1ListHas(got, leaked) {
			t.Errorf("blank-tenant /api/user/models leaked tenant-owned model %q (fail-open): %v", leaked, got)
		}
	}
}

// GET /api/channel/models_enabled with no tenant on the session.
func TestV1ModelsEnabled_ListTenantScoped_EmptyTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	shared, own, victim := seedV1DiscoveryTrio(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/channel/models_enabled", nil, common.RoleAdminUser, "", ctx.AdminUser.Id)
	EnabledListModels(c)
	got := v1StringList(t, w)
	if !v1ListHas(got, shared) {
		t.Errorf("blank-tenant admin must still see the shared catalogue, missing %q: %v", shared, got)
	}
	for _, leaked := range []string{own, victim} {
		if v1ListHas(got, leaked) {
			t.Errorf("blank-tenant models_enabled leaked tenant-owned model %q (fail-open): %v", leaked, got)
		}
	}
}

// GET /api/models/missing with no tenant on the session.
func TestV1MissingModels_ListTenantScoped_EmptyTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	if err := ctx.DB.AutoMigrate(&repo.Model{}); err != nil {
		t.Fatalf("migrate models table: %v", err)
	}
	shared, own, victim := seedV1DiscoveryTrio(t, ctx)

	c, w := v1Ctx(http.MethodGet, "/api/models/missing", nil, common.RoleAdminUser, "", ctx.AdminUser.Id)
	GetMissingModels(c)
	if v1Body(t, w)["success"] != true {
		t.Fatalf("blank-tenant missing-models call failed: %s", w.Body.String())
	}
	got := v1StringList(t, w)
	if !v1ListHas(got, shared) {
		t.Errorf("blank-tenant admin must still see the shared catalogue, missing %q: %v", shared, got)
	}
	for _, leaked := range []string{own, victim} {
		if v1ListHas(got, leaked) {
			t.Errorf("blank-tenant missing-models leaked tenant-owned model %q (fail-open): %v", leaked, got)
		}
	}
}

// GET /api/channel/tag/models with no tenant on the session.
func TestV1ChannelTagModels_ListTenantScoped_EmptyTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	const victimTag = "victim-only-tag-blank"
	victim := seedV1TaggedChannel(t, ctx, idorVictimTenant, "tagmodels-victim-blank", victimTag, common.ChannelStatusEnabled)
	victim.Models = "victim-secret-model"
	if err := ctx.DB.Save(victim).Error; err != nil {
		t.Fatalf("set victim models: %v", err)
	}

	c, w := v1Ctx(http.MethodGet, "/api/channel/tag/models?tag="+victimTag, nil, common.RoleAdminUser, "", ctx.AdminUser.Id)
	GetTagModels(c)
	if got, _ := v1Body(t, w)["data"].(string); got != "" {
		t.Errorf("blank-tenant admin read another tenant's tag model list (fail-open): %q", got)
	}
}

// GET /api/channel/?tag_mode=true with no tenant on the session. This half
// never went through tenantScopeForDiscovery — GetAllChannels keeps using the
// raw session tenant so its page agrees with its exact-match row filter — so
// what this pins is that the raw value is itself fail-closed.
func TestV1ChannelTagMode_ListTenantScoped_EmptyTenantFailsClosed(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	seedV1TaggedChannel(t, ctx, ctx.TenantID, "tagmode-own-blank", "own-tag-blank", common.ChannelStatusEnabled)
	seedV1TaggedChannel(t, ctx, idorVictimTenant, "tagmode-victim-blank", "victim-tag-blank", common.ChannelStatusEnabled)

	c, w := v1Ctx(http.MethodGet, "/api/channel/?p=1&page_size=50&tag_mode=true", nil, common.RoleAdminUser, "", ctx.AdminUser.Id)
	GetAllChannels(c)
	data := v1Body(t, w)["data"].(map[string]interface{})
	if got := data["total"].(float64); got != 0 {
		t.Errorf("blank-tenant admin tag_mode total=%v want 0 (fail-closed)", got)
	}
	if got := v1ChannelNames(t, w); len(got) != 0 {
		t.Errorf("blank-tenant admin tag_mode page = %v, want empty (fail-closed)", got)
	}
}
