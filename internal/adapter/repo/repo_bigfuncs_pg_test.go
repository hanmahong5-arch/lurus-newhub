package repo

// repo_bigfuncs_pg_test.go — real-Postgres coverage for the largest still-
// uncovered non-Redis, non-bootstrap functions:
//   - pricing.go updatePricing (via GetPricing/GetVendors) — the pricing table
//     builder that joins abilities+channels+models+vendors
//   - log.go RecordErrorLog / RecordConsumeLog — the relay log chokepoints
//   - token.go ListAutoRotateTokens / RotateKeyWithTimestamp
//   - channel.go BatchSetChannelTag
//   - redemption.go GetAllRedemptions
//   - user_mapping.go CreateUserFromIDPClaims (email-fallback + auto-create)
//   - utils.go batchUpdate (+ updateUserUsedQuota / updateUserRequestCount)
//
// Assertions are on concrete persisted state.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

func TestGetPricing_BuildsFromSeededData_PG(t *testing.T) {
	SetupTestDB(t)
	if err := DB.AutoMigrate(&Model{}, &Vendor{}); err != nil {
		t.Fatalf("migrate model/vendor: %v", err)
	}

	// Seed a vendor, a channel, an ability that links model→group→channel, and
	// a model meta row (status=1, enabled).
	vendor := &Vendor{Name: "acme-vendor", Description: "d", Icon: "i"}
	if err := DB.Create(vendor).Error; err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
	ch := &Channel{Id: 5001, Type: 1, Status: common.ChannelStatusEnabled, Name: "c1", Models: "gpt-4o", Group: "default", TenantId: "default"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := DB.Create(&Ability{Group: "default", Model: "gpt-4o", ChannelId: 5001, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability: %v", err)
	}
	if err := DB.Create(&Model{ModelName: "gpt-4o", Status: 1, NameRule: NameRuleExact, VendorID: vendor.Id, Description: "flagship", Icon: "gpt.png", Tags: "chat"}).Error; err != nil {
		t.Fatalf("seed model meta: %v", err)
	}

	// Force a rebuild: the pricing cache is process-global.
	updatePricingLock.Lock()
	lastGetPricingTime = time.Time{}
	pricingMap = nil
	updatePricingLock.Unlock()

	pricing := GetPricing()
	var found *Pricing
	for i := range pricing {
		if pricing[i].ModelName == "gpt-4o" {
			found = &pricing[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("gpt-4o not present in pricing map (built from %d entries)", len(pricing))
	}
	if len(found.EnableGroup) == 0 || found.EnableGroup[0] != "default" {
		t.Errorf("EnableGroup wrong: %+v", found.EnableGroup)
	}
	if found.Description != "flagship" || found.VendorID != vendor.Id {
		t.Errorf("model meta not merged into pricing: %+v", found)
	}

	// GetVendors must include the seeded vendor (built in the same pass).
	vendors := GetVendors()
	seen := false
	for _, v := range vendors {
		if v.ID == vendor.Id && v.Name == "acme-vendor" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("seeded vendor missing from GetVendors: %+v", vendors)
	}

	// A disabled model meta (status != 1) must be dropped from pricing. Status
	// is set to 2 (not 0): the column carries a gorm `default:1` tag, so a 0
	// zero-value would be rewritten to 1 (enabled) on insert.
	if err := DB.Create(&Model{ModelName: "gpt-disabled", Status: 2, NameRule: NameRuleExact}).Error; err != nil {
		t.Fatalf("seed disabled model: %v", err)
	}
	if err := DB.Create(&Channel{Id: 5002, Type: 1, Status: common.ChannelStatusEnabled, Name: "c2", Models: "gpt-disabled", Group: "default", TenantId: "default"}).Error; err != nil {
		t.Fatalf("seed channel2: %v", err)
	}
	if err := DB.Create(&Ability{Group: "default", Model: "gpt-disabled", ChannelId: 5002, Enabled: true}).Error; err != nil {
		t.Fatalf("seed ability2: %v", err)
	}
	updatePricingLock.Lock()
	lastGetPricingTime = time.Time{}
	pricingMap = nil
	updatePricingLock.Unlock()
	for _, p := range GetPricing() {
		if p.ModelName == "gpt-disabled" {
			t.Error("disabled model (status!=1) must not appear in pricing")
		}
	}
}

func newLogCtx(username, tenantID string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	if username != "" {
		c.Set("username", username)
	}
	if tenantID != "" {
		c.Set("tenant_id", tenantID)
	}
	return c, w
}

func TestRecordErrorLog_PersistsErrorRow_PG(t *testing.T) {
	SetupTestDB(t)
	c, _ := newLogCtx("erruser", "tenant-e")

	RecordErrorLog(c, 42, 7, "gpt-4", "tok", "upstream 500", 3, 2, false, "default", map[string]interface{}{"k": "v"})

	var lg Log
	if err := LOG_DB.Where("type = ? AND user_id = ?", LogTypeError, 42).First(&lg).Error; err != nil {
		t.Fatalf("error log not written: %v", err)
	}
	if lg.Content != "upstream 500" || lg.TenantId != "tenant-e" || lg.ModelName != "gpt-4" || lg.ChannelId != 7 {
		t.Fatalf("error log fields wrong: %+v", lg)
	}
}

func TestRecordConsumeLog_GuardsAndPersists_PG(t *testing.T) {
	SetupTestDB(t)

	prevConsume := common.LogConsumeEnabled
	defer func() { common.LogConsumeEnabled = prevConsume }()

	params := RecordConsumeLogParams{
		ChannelId: 9, PromptTokens: 10, CompletionTokens: 20, ModelName: "gpt-4",
		TokenName: "t", Quota: 100, ChannelType: 1, Content: "ok",
	}

	t.Run("disabled → metrics only, no log row", func(t *testing.T) {
		common.LogConsumeEnabled = false
		c, _ := newLogCtx("u", "tenant-c")
		RecordConsumeLog(c, 1, params)
		var cnt int64
		LOG_DB.Model(&Log{}).Where("type = ? AND user_id = ?", LogTypeConsume, 1).Count(&cnt)
		if cnt != 0 {
			t.Fatalf("consume log written despite LogConsumeEnabled=false, count=%d", cnt)
		}
	})

	t.Run("detail level none → skip write", func(t *testing.T) {
		common.LogConsumeEnabled = true
		c, _ := newLogCtx("u", "tenant-c")
		p := params
		p.LogDetailLevel = "none"
		RecordConsumeLog(c, 2, p)
		var cnt int64
		LOG_DB.Model(&Log{}).Where("type = ? AND user_id = ?", LogTypeConsume, 2).Count(&cnt)
		if cnt != 0 {
			t.Fatalf("consume log written despite detail level none, count=%d", cnt)
		}
	})

	t.Run("enabled → persists consume row", func(t *testing.T) {
		common.LogConsumeEnabled = true
		c, _ := newLogCtx("consumer", "tenant-c")
		RecordConsumeLog(c, 3, params)
		var lg Log
		if err := LOG_DB.Where("type = ? AND user_id = ?", LogTypeConsume, 3).First(&lg).Error; err != nil {
			t.Fatalf("consume log not written: %v", err)
		}
		if lg.Quota != 100 || lg.PromptTokens != 10 || lg.CompletionTokens != 20 || lg.TenantId != "tenant-c" {
			t.Fatalf("consume log fields wrong: %+v", lg)
		}
	})
}

func TestTokenAutoRotate_PG(t *testing.T) {
	SetupTestDB(t)
	_, normal, _ := SeedTestUsers(t)

	// One auto-rotate-enabled enabled token, one disabled-status auto-rotate,
	// one non-auto-rotate → only the first is listed.
	rotTok := &Token{UserId: normal.Id, TenantId: "default", Key: "sk-rot-" + common.GetRandomString(20), Status: common.TokenStatusEnabled, Name: "rot", CreatedTime: common.GetTimestamp(), ExpiredTime: -1, AutoRotateDays: 30, Group: "default"}
	if err := DB.Create(rotTok).Error; err != nil {
		t.Fatalf("seed rot token: %v", err)
	}
	if err := DB.Create(&Token{UserId: normal.Id, TenantId: "default", Key: "sk-dis-" + common.GetRandomString(20), Status: common.TokenStatusDisabled, Name: "dis", CreatedTime: common.GetTimestamp(), ExpiredTime: -1, AutoRotateDays: 30, Group: "default"}).Error; err != nil {
		t.Fatalf("seed dis token: %v", err)
	}
	if err := DB.Create(&Token{UserId: normal.Id, TenantId: "default", Key: "sk-noauto-" + common.GetRandomString(16), Status: common.TokenStatusEnabled, Name: "noauto", CreatedTime: common.GetTimestamp(), ExpiredTime: -1, AutoRotateDays: 0, Group: "default"}).Error; err != nil {
		t.Fatalf("seed noauto token: %v", err)
	}

	list, err := ListAutoRotateTokens()
	if err != nil {
		t.Fatalf("ListAutoRotateTokens: %v", err)
	}
	if len(list) != 1 || list[0].Name != "rot" {
		t.Fatalf("want exactly the enabled auto-rotate token, got %d: %+v", len(list), list)
	}

	// Rotate its key with a timestamp; both key and rotated_at must persist.
	newKey := "sk-new-" + common.GetRandomString(20)
	if err := rotTok.RotateKeyWithTimestamp(newKey, 1234567); err != nil {
		t.Fatalf("RotateKeyWithTimestamp: %v", err)
	}
	var reloaded Token
	if err := DB.First(&reloaded, rotTok.Id).Error; err != nil {
		t.Fatalf("reload rotated token: %v", err)
	}
	// Token.Key is char(48), so PG right-pads the stored value with spaces.
	if strings.TrimSpace(reloaded.Key) != newKey || reloaded.RotatedAt != 1234567 {
		t.Fatalf("rotation not persisted: key=%q rotated_at=%d", reloaded.Key, reloaded.RotatedAt)
	}
}

func TestBatchSetChannelTag_PG(t *testing.T) {
	SetupTestDB(t)
	c1 := &Channel{Id: 6001, Type: 1, Status: common.ChannelStatusEnabled, Name: "a", Models: "m", Group: "default", TenantId: "default"}
	c2 := &Channel{Id: 6002, Type: 1, Status: common.ChannelStatusEnabled, Name: "b", Models: "m", Group: "default", TenantId: "default"}
	for _, ch := range []*Channel{c1, c2} {
		if err := DB.Create(ch).Error; err != nil {
			t.Fatalf("seed channel: %v", err)
		}
	}
	tag := "prod"
	if err := BatchSetChannelTag([]int{6001, 6002}, &tag); err != nil {
		t.Fatalf("BatchSetChannelTag: %v", err)
	}
	var got []Channel
	if err := DB.Where("id in (?)", []int{6001, 6002}).Find(&got).Error; err != nil {
		t.Fatalf("reload channels: %v", err)
	}
	for _, ch := range got {
		if ch.Tag == nil || *ch.Tag != "prod" {
			t.Fatalf("channel %d tag not set: %+v", ch.Id, ch.Tag)
		}
	}
}

func TestGetAllRedemptions_Pagination_PG(t *testing.T) {
	SetupTestDB(t)
	for i := 0; i < 5; i++ {
		seedRedemptionRow(t, 1, "default", "code", 100)
	}
	rows, total, err := GetAllRedemptions(0, 3)
	if err != nil {
		t.Fatalf("GetAllRedemptions: %v", err)
	}
	if total != 5 {
		t.Fatalf("want total 5, got %d", total)
	}
	if len(rows) != 3 {
		t.Fatalf("want page size 3, got %d", len(rows))
	}
	// Ordered id DESC → first row has the largest id.
	if rows[0].Id < rows[1].Id {
		t.Errorf("expected id DESC ordering, got %d then %d", rows[0].Id, rows[1].Id)
	}
}

func TestCreateUserFromIDPClaims_PG(t *testing.T) {
	SetupTestDB(t)
	seedTenant(t, "default", "default", "Default")

	t.Run("email fallback links existing user", func(t *testing.T) {
		existing := seedUser(t, "legacy", "legacy@corp.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
		// EmailVerified was left at its zero value when this test was written
		// because nothing read the field. The fallback now requires a verified
		// address; this subtest covers the legitimate same-tenant,
		// non-privileged link, so it asserts the verified case. The refusals are
		// locked in user_mapping_email_fallback_test.go.
		claims := &OIDCUserClaims{Sub: "oidc-sub-legacy", Email: "legacy@corp.com", EmailVerified: true, Name: "Legacy User", PreferredUsername: "legacy"}
		user, mapping, err := CreateUserFromIDPClaims(claims, "default")
		if err != nil {
			t.Fatalf("email fallback: %v", err)
		}
		if user.Id != existing.Id {
			t.Fatalf("email fallback linked wrong user: got %d want %d", user.Id, existing.Id)
		}
		if mapping == nil || mapping.LurusUserID != existing.Id {
			t.Fatalf("mapping not created for existing user: %+v", mapping)
		}
	})

	t.Run("auto-create new user", func(t *testing.T) {
		claims := &OIDCUserClaims{Sub: "oidc-sub-new", Email: "brand-new@corp.com", Name: "New User", PreferredUsername: "newbie"}
		user, mapping, err := CreateUserFromIDPClaims(claims, "default")
		if err != nil {
			t.Fatalf("auto-create: %v", err)
		}
		if user.Id == 0 || user.Email != "brand-new@corp.com" {
			t.Fatalf("auto-created user wrong: %+v", user)
		}
		if mapping == nil || mapping.IDPSubject != "oidc-sub-new" {
			t.Fatalf("mapping wrong for auto-created user: %+v", mapping)
		}
		// It must be persisted and findable.
		var reloaded User
		if err := DB.First(&reloaded, user.Id).Error; err != nil {
			t.Fatalf("auto-created user not persisted: %v", err)
		}
	})
}

func TestBatchUpdate_FlushesToDB_PG(t *testing.T) {
	SetupTestDB(t)
	_, normal, _ := SeedTestUsers(t)
	startQuota := normal.Quota

	tok := &Token{UserId: normal.Id, TenantId: "default", Key: "sk-bu-" + common.GetRandomString(20), Status: common.TokenStatusEnabled, Name: "bu", CreatedTime: common.GetTimestamp(), ExpiredTime: -1, RemainQuota: 1000, Group: "default"}
	if err := DB.Create(tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	ch := &Channel{Id: 7001, Type: 1, Status: common.ChannelStatusEnabled, Name: "bu", Models: "m", Group: "default", TenantId: "default"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Enqueue one record of every batch type, then flush.
	addNewRecord(BatchUpdateTypeUserQuota, normal.Id, 500)
	addNewRecord(BatchUpdateTypeUserQuota, normal.Id, 250) // same key accumulates → +750
	addNewRecord(BatchUpdateTypeTokenQuota, tok.Id, 300)
	addNewRecord(BatchUpdateTypeUsedQuota, normal.Id, 40)
	addNewRecord(BatchUpdateTypeRequestCount, normal.Id, 2)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, 7001, 99)

	batchUpdate()

	var u User
	if err := DB.First(&u, normal.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if u.Quota != startQuota+750 {
		t.Errorf("user quota batch update wrong: got %d want %d", u.Quota, startQuota+750)
	}
	if u.UsedQuota != 40 {
		t.Errorf("used quota batch update wrong: got %d want 40", u.UsedQuota)
	}
	if u.RequestCount != 2 {
		t.Errorf("request count batch update wrong: got %d want 2", u.RequestCount)
	}

	var reTok Token
	if err := DB.First(&reTok, tok.Id).Error; err != nil {
		t.Fatalf("reload token: %v", err)
	}
	if reTok.RemainQuota != 1300 {
		t.Errorf("token quota batch update wrong: got %d want 1300", reTok.RemainQuota)
	}

	var reCh Channel
	if err := DB.First(&reCh, 7001).Error; err != nil {
		t.Fatalf("reload channel: %v", err)
	}
	if reCh.UsedQuota != 99 {
		t.Errorf("channel used quota batch update wrong: got %d want 99", reCh.UsedQuota)
	}
}
