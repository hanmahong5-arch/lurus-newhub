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
