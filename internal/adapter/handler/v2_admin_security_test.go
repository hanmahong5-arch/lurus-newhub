package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	apptotp "github.com/LurusTech/lurus-hub/internal/app/totp"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupAdminSecurityDB mirrors setupTotpFlowDB's hermetic-SQLite convention
// for the admin TOTP stats + force-disable surface.
func setupAdminSecurityDB(t *testing.T) func() {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:adminsec%d?mode=memory&cache=shared", testDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{
		&repo.User{}, &repo.Log{}, &repo.Option{},
		&entity.UserTOTP{}, &entity.UserTOTPBackupCode{},
		&entity.AuditEvent{}, &entity.AuditChainHead{},
	} {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("automigrate %T: %v", tbl, err)
		}
	}

	prevDB, prevLogDB := repo.DB, repo.LOG_DB
	prevSQLite, prevPG, prevRedis := common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled
	prevLogConsume := common.LogConsumeEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.LogConsumeEnabled = false
	apptotp.ResetStateForTest()
	// Real audit-writer wiring, pinned to THIS test's db — see
	// pinnedAuditWriter's doc comment in v2_pricing_write_test.go for why a
	// closure-captured *gorm.DB and not the mutable repo.DB package global.
	governance.SetAuditWriter(&pinnedAuditWriter{db: db})

	return func() {
		repo.DB, repo.LOG_DB = prevDB, prevLogDB
		common.UsingSQLite, common.UsingPostgreSQL, common.RedisEnabled = prevSQLite, prevPG, prevRedis
		common.LogConsumeEnabled = prevLogConsume
		apptotp.ResetStateForTest()
		if sqlDB, e := db.DB(); e == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

// buildAdminSecurityRouter wires the real handlers behind a stub auth that
// fixes the acting admin's id (mirrors production RootJWTAuth setting "id"
// for the session-authenticated branch — see admin_jwt_auth.go's authHelper
// fallback), with a real cookie session store so SecureVerificationRequired's
// step-up stamp works exactly like production.
func buildAdminSecurityRouter(actorID int) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("admin-security-test-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		c.Set("id", actorID)
		c.Next()
	})
	r.POST("/api/verify", UniversalVerify)
	r.GET("/api/v2/admin/security/totp-stats", GetAdminTotpStatsV2)
	r.POST("/api/v2/admin/security/users/:id/totp/force-disable",
		middleware.SecureVerificationRequired(), ForceDisableTotpV2)
	return r
}

// stepUpAsActor stamps the session (via UniversalVerify's legacy
// method:"session" branch — the actor here has no TOTP of their own
// enrolled) and returns the resulting cookies.
func stepUpAsActor(t *testing.T, r *gin.Engine) []*http.Cookie {
	t.Helper()
	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("actor step-up: status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("actor step-up did not stamp a session cookie")
	}
	return cookies
}

func seedUserRow(t *testing.T, db *gorm.DB, id int, tenantID, username string) {
	t.Helper()
	if err := db.Create(&repo.User{
		Id: id, TenantId: tenantID, Username: username,
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: username + "@test.local",
	}).Error; err != nil {
		t.Fatalf("seed user %d: %v", id, err)
	}
}

func seedEnrolledTotp(t *testing.T, userID int, backupCodesTotal, backupCodesUnused int) {
	t.Helper()
	if err := repo.UpsertUserTOTP(&entity.UserTOTP{
		UserId: userID, SecretEncrypted: "enc-fixture", Enabled: true,
		CreatedAt: common.GetTimestamp(), ConfirmedAt: common.GetTimestamp(),
	}); err != nil {
		t.Fatalf("seed totp for user %d: %v", userID, err)
	}
	if backupCodesTotal == 0 {
		return
	}
	rows := make([]entity.UserTOTPBackupCode, 0, backupCodesTotal)
	for i := 0; i < backupCodesTotal; i++ {
		usedAt := int64(0)
		if i >= backupCodesUnused {
			usedAt = common.GetTimestamp()
		}
		rows = append(rows, entity.UserTOTPBackupCode{
			UserId: userID, CodeHash: fmt.Sprintf("hash-%d-%d", userID, i),
			CreatedAt: common.GetTimestamp(), UsedAt: usedAt,
		})
	}
	if err := repo.ReplaceUserTOTPBackupCodes(userID, rows); err != nil {
		t.Fatalf("seed backup codes for user %d: %v", userID, err)
	}
}

// TestAdminTotpStats_TenantScoped seeds two tenants with distinct
// enrollment/backup-code states and asserts the tenant_id filter returns
// only the requested tenant's numbers.
func TestAdminTotpStats_TenantScoped(t *testing.T) {
	cleanup := setupAdminSecurityDB(t)
	defer cleanup()
	r := buildAdminSecurityRouter(1)

	// Tenant "alpha": 3 users — one enrolled+has unused codes, one enrolled
	// with all codes exhausted, one pending (enrolled=false).
	seedUserRow(t, repo.DB, 101, "alpha", "alpha-fresh")
	seedEnrolledTotp(t, 101, 3, 3) // no_codes_issued? no — has unused codes, neither bucket
	seedUserRow(t, repo.DB, 102, "alpha", "alpha-exhausted")
	seedEnrolledTotp(t, 102, 2, 0) // exhausted
	seedUserRow(t, repo.DB, 103, "alpha", "alpha-pending")
	if err := repo.UpsertUserTOTP(&entity.UserTOTP{
		UserId: 103, SecretEncrypted: "enc", Enabled: false, CreatedAt: common.GetTimestamp(),
	}); err != nil {
		t.Fatalf("seed pending totp: %v", err)
	}
	seedUserRow(t, repo.DB, 104, "alpha", "alpha-nomfa")

	// Three soft-deleted, still-enrolled users must not inflate Enrolled past
	// a TotalUsers denominator that (via GORM's automatic soft-delete scope
	// on the Model(&entity.User{}) count) already excludes them — the
	// join-based totpTenantFilteredQuery must filter u.deleted_at IS NULL
	// explicitly, the same way. Mutation: removing that filter adds users
	// 105/106/107 to Enrolled (101, 102, 105, 106, 107 = 5) while TotalUsers
	// stays 4 (101-104, soft-deleted users excluded regardless), so
	// adoption_pct becomes 125% — the AdoptionPct>100 assertion below only
	// fires with three soft-deleted enrolled users, not one: with a single
	// one (Enrolled=3/TotalUsers=4=75%) that assertion cannot go red.
	for _, id := range []int{105, 106, 107} {
		seedUserRow(t, repo.DB, id, "alpha", fmt.Sprintf("alpha-deleted-%d", id))
		seedEnrolledTotp(t, id, 1, 1)
		if err := repo.DB.Delete(&repo.User{Id: id}).Error; err != nil {
			t.Fatalf("soft-delete user %d: %v", id, err)
		}
	}

	// Tenant "beta": 1 user, enrolled, no backup codes ever issued.
	seedUserRow(t, repo.DB, 201, "beta", "beta-nocode")
	seedEnrolledTotp(t, 201, 0, 0)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/security/totp-stats?tenant_id=alpha", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("totp-stats alpha: status=%d body=%s", w.Code, w.Body.String())
	}
	var env apiEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var stats struct {
		Enrolled             int64   `json:"enrolled"`
		Pending              int64   `json:"pending"`
		TotalUsers           int64   `json:"total_users"`
		AdoptionPct          float64 `json:"adoption_pct"`
		BackupCodesExhausted int64   `json:"backup_codes_exhausted"`
		NoCodesIssued        int64   `json:"no_codes_issued"`
	}
	if err := json.Unmarshal(env.Data, &stats); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if stats.TotalUsers != 4 {
		t.Errorf("alpha total_users = %d, want 4 (beta user must not leak in)", stats.TotalUsers)
	}
	if stats.Enrolled != 2 {
		t.Errorf("alpha enrolled = %d, want 2", stats.Enrolled)
	}
	if stats.Pending != 1 {
		t.Errorf("alpha pending = %d, want 1", stats.Pending)
	}
	if stats.BackupCodesExhausted != 1 {
		t.Errorf("alpha backup_codes_exhausted = %d, want 1 (user 102)", stats.BackupCodesExhausted)
	}
	if stats.NoCodesIssued != 0 {
		t.Errorf("alpha no_codes_issued = %d, want 0 (user 101 has unused codes, not zero-issued)", stats.NoCodesIssued)
	}
	if stats.AdoptionPct > 100 {
		t.Errorf("alpha adoption_pct = %v, must not exceed 100 (soft-deleted users 105-107 must not count in enrolled)", stats.AdoptionPct)
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/admin/security/totp-stats?tenant_id=beta", nil)
	r.ServeHTTP(w2, req2)
	var env2 apiEnvelope
	_ = json.Unmarshal(w2.Body.Bytes(), &env2)
	var stats2 struct {
		TotalUsers    int64 `json:"total_users"`
		Enrolled      int64 `json:"enrolled"`
		NoCodesIssued int64 `json:"no_codes_issued"`
	}
	_ = json.Unmarshal(env2.Data, &stats2)
	if stats2.TotalUsers != 1 || stats2.Enrolled != 1 || stats2.NoCodesIssued != 1 {
		t.Errorf("beta stats = %+v, want {total_users:1 enrolled:1 no_codes_issued:1}", stats2)
	}
}

// TestAdminTotpForceDisable_RequiresStepUp: the acting root must have
// stepped up in their own session — no step-up stamp, no access, mirroring
// TotpDisable's own gate (mutation: removing SecureVerificationRequired from
// the route turns this green too).
func TestAdminTotpForceDisable_RequiresStepUp(t *testing.T) {
	cleanup := setupAdminSecurityDB(t)
	defer cleanup()
	r := buildAdminSecurityRouter(1)
	seedUserRow(t, repo.DB, 301, "default", "victim")
	seedEnrolledTotp(t, 301, 1, 1)

	w, env := doJSON(t, r, http.MethodPost, "/api/v2/admin/security/users/301/totp/force-disable",
		`{"reason":"lost device"}`, nil)
	if w.Code != http.StatusForbidden || env.Code != "VERIFICATION_REQUIRED" {
		t.Fatalf("force-disable without step-up: status=%d code=%q body=%s", w.Code, env.Code, w.Body.String())
	}

	// The enrollment must still exist — the gate ran before the handler.
	rec, err := repo.GetUserTOTP(301)
	if err != nil || rec == nil {
		t.Fatalf("enrollment must survive a rejected force-disable: rec=%v err=%v", rec, err)
	}
}

// TestAdminTotpForceDisable_RemovesRowAndCodes_AuditsAndNotifies drives the
// full success path: step up as root, force-disable, and assert the TOTP
// row + all backup codes are gone, an auth.totp_admin_disabled audit row
// exists with resource_id = target, and the target was notified via the
// real app.NotifyUser hook (spied through the notifyUserFn seam).
func TestAdminTotpForceDisable_RemovesRowAndCodes_AuditsAndNotifies(t *testing.T) {
	cleanup := setupAdminSecurityDB(t)
	defer cleanup()
	const actorID = 1
	const targetID = 401
	r := buildAdminSecurityRouter(actorID)
	seedUserRow(t, repo.DB, actorID, "default", "root")
	seedUserRow(t, repo.DB, targetID, "default", "victim")
	seedEnrolledTotp(t, targetID, 3, 2)

	cookies := stepUpAsActor(t, r)

	type notifyCall struct {
		userID int
		email  string
		notify dto.Notify
	}
	var captured *notifyCall
	prevNotify := notifyUserFn
	notifyUserFn = func(_ context.Context, userID int, email string, _ dto.UserSetting, notify dto.Notify) error {
		captured = &notifyCall{userID: userID, email: email, notify: notify}
		return nil
	}
	defer func() { notifyUserFn = prevNotify }()

	w, env := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v2/admin/security/users/%d/totp/force-disable", targetID),
		`{"reason":"user lost their phone, verified over support ticket #4242"}`, cookies)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("force-disable: status=%d body=%s", w.Code, w.Body.String())
	}

	rec, err := repo.GetUserTOTP(targetID)
	if err != nil {
		t.Fatalf("GetUserTOTP after force-disable: %v", err)
	}
	if rec != nil {
		t.Fatalf("TOTP row must be gone after force-disable, got %+v", rec)
	}
	remaining, err := repo.CountUnusedUserTOTPBackupCodes(targetID)
	if err != nil {
		t.Fatalf("CountUnusedUserTOTPBackupCodes: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("unused backup codes after force-disable = %d, want 0", remaining)
	}
	var totalCodes int64
	repo.DB.Model(&entity.UserTOTPBackupCode{}).Where("user_id = ?", targetID).Count(&totalCodes)
	if totalCodes != 0 {
		t.Fatalf("backup code rows after force-disable = %d, want 0 (used codes must also be purged)", totalCodes)
	}

	if captured == nil {
		t.Fatal("app.NotifyUser (via the notifyUserFn seam) was never called — the target user was not notified")
	}
	if captured.userID != targetID {
		t.Errorf("notify target = %d, want %d", captured.userID, targetID)
	}

	// The handler must surface whether the notify attempt errored — both in
	// the response (so the console can tell support) and in the audit
	// detail (so a later reviewer can tell, without re-deriving it from the
	// notifyUserFn spy the way this test does). The spy above returns nil,
	// so this must be true.
	var respData struct {
		Notified bool `json:"notified"`
	}
	if err := json.Unmarshal(env.Data, &respData); err != nil {
		t.Fatalf("unmarshal response data: %v", err)
	}
	if !respData.Notified {
		t.Error("response data.notified = false, want true (notifyUserFn returned nil)")
	}

	// RecordAuditEvent persists via gopool.Go (async) — poll like
	// TestV2PricingWrite_AuditRow does, not a single immediate read.
	ev := pollAuditRow(t, governance.ActionTotpAdminDisabled, 2*time.Second)
	if ev == nil {
		t.Fatal("no auth.totp_admin_disabled audit row appeared within the poll window")
	}
	if ev.ResourceID != targetID {
		t.Errorf("audit resource_id = %d, want %d", ev.ResourceID, targetID)
	}
	if ev.ActorID != actorID {
		t.Errorf("audit actor_id = %d, want %d (the acting root, not the target)", ev.ActorID, actorID)
	}
	if !strings.Contains(ev.Details, `"notified":true`) {
		t.Errorf("audit details = %q, want it to carry notified:true", ev.Details)
	}
	if !strings.Contains(ev.Details, "lost their phone") {
		t.Errorf("audit details = %q, want it to carry the reason", ev.Details)
	}
}

// TestAdminTotpForceDisable_NoNotifyTarget_NotFalsePositive is the lock for
// the L6 repair finding: a target with no email/webhook/bark/gotify
// configured makes app.NotifyUser skip the send and return nil (not an
// error) — before this repair the handler could not tell that apart from a
// real send and reported notified:true anyway. Mutation: dropping the
// app.HasNotifyTarget check (i.e. going back to "notified := notifyErr ==
// nil") makes this red.
func TestAdminTotpForceDisable_NoNotifyTarget_NotFalsePositive(t *testing.T) {
	cleanup := setupAdminSecurityDB(t)
	defer cleanup()
	const actorID = 1
	const targetID = 402
	r := buildAdminSecurityRouter(actorID)
	seedUserRow(t, repo.DB, actorID, "default", "root")
	// No Email set (unlike seedUserRow's fixture) and no webhook/bark/gotify
	// in Setting, so NotifyType defaults to email and HasNotifyTarget must
	// see nothing to send to.
	if err := repo.DB.Create(&repo.User{
		Id: targetID, TenantId: "default", Username: "no-notify-target",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
	}).Error; err != nil {
		t.Fatalf("seed target user: %v", err)
	}
	seedEnrolledTotp(t, targetID, 2, 1)

	cookies := stepUpAsActor(t, r)

	// Use the real app.NotifyUser (not the spy) so the no-target skip path
	// (user_notify.go's "user has no email, skip sending email" branch)
	// actually runs and returns nil. constant.NotifyLimitCount defaults to
	// the Go zero value (0) outside common.SysInit's env-driven assignment,
	// which would make CheckNotificationLimit itself deny the very first
	// send attempt ("notification limit exceeded") — a false-negative reason
	// for notified=false that has nothing to do with the fix under test, so
	// raise the limit for this test only.
	prevLimit := constant.NotifyLimitCount
	constant.NotifyLimitCount = 100
	defer func() { constant.NotifyLimitCount = prevLimit }()
	prevNotify := notifyUserFn
	notifyUserFn = app.NotifyUser
	defer func() { notifyUserFn = prevNotify }()

	w, env := doJSON(t, r, http.MethodPost, fmt.Sprintf("/api/v2/admin/security/users/%d/totp/force-disable", targetID),
		`{"reason":"user lost their phone, no contact method on file"}`, cookies)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("force-disable: status=%d body=%s", w.Code, w.Body.String())
	}

	var respData struct {
		Notified bool `json:"notified"`
	}
	if err := json.Unmarshal(env.Data, &respData); err != nil {
		t.Fatalf("unmarshal response data: %v", err)
	}
	if respData.Notified {
		t.Error("response data.notified = true, want false: NotifyUser skipped (no email/webhook/bark/gotify configured), nothing was sent")
	}

	ev := pollAuditRow(t, governance.ActionTotpAdminDisabled, 2*time.Second)
	if ev == nil {
		t.Fatal("no auth.totp_admin_disabled audit row appeared within the poll window")
	}
	if !strings.Contains(ev.Details, `"notified":false`) {
		t.Errorf("audit details = %q, want it to carry notified:false (no target existed to notify)", ev.Details)
	}
}

// TestAdminTotpForceDisable_404NoEnrollment: the target has no TOTP row at
// all — force-disable must not silently succeed on nothing.
func TestAdminTotpForceDisable_404NoEnrollment(t *testing.T) {
	cleanup := setupAdminSecurityDB(t)
	defer cleanup()
	r := buildAdminSecurityRouter(1)
	seedUserRow(t, repo.DB, 1, "default", "root")
	seedUserRow(t, repo.DB, 501, "default", "never-enrolled")

	cookies := stepUpAsActor(t, r)
	w, env := doJSON(t, r, http.MethodPost, "/api/v2/admin/security/users/501/totp/force-disable",
		`{"reason":"support ticket"}`, cookies)
	if w.Code != http.StatusNotFound {
		t.Fatalf("force-disable with no enrollment: status=%d body=%s (want 404)", w.Code, w.Body.String())
	}
	if env.Success {
		t.Fatalf("force-disable with no enrollment must not report success: %s", w.Body.String())
	}
}

// TestAdminTotpForceDisable_CJKReasonUpTo200Runes locks the byte-vs-rune
// finding: the console textarea's maxLength={200} counts characters, so a
// 200-character (not byte) CJK reason must be accepted. Mutation: reverting
// the handler's check from utf8.RuneCountInString(req.Reason) > 200 back to
// len(req.Reason) > 200 makes this 400 (each CJK rune is 3 bytes, so 200
// runes = 600 bytes).
func TestAdminTotpForceDisable_CJKReasonUpTo200Runes(t *testing.T) {
	cleanup := setupAdminSecurityDB(t)
	defer cleanup()
	r := buildAdminSecurityRouter(1)
	seedUserRow(t, repo.DB, 1, "default", "root")
	seedUserRow(t, repo.DB, 601, "default", "cjk-reason-target")
	seedEnrolledTotp(t, 601, 1, 1)

	cookies := stepUpAsActor(t, r)

	reasonRune := "用"
	reason := strings.Repeat(reasonRune, 200)
	if utf8.RuneCountInString(reason) != 200 {
		t.Fatalf("fixture broken: reason has %d runes, want 200", utf8.RuneCountInString(reason))
	}
	body, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	w, env := doJSON(t, r, http.MethodPost, "/api/v2/admin/security/users/601/totp/force-disable",
		string(body), cookies)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("force-disable with a 200-rune CJK reason: status=%d body=%s (want 200 success)", w.Code, w.Body.String())
	}
}
