package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	apptotp "github.com/LurusTech/lurus-hub/internal/app/totp"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	pqtotp "github.com/pquerna/otp/totp"
	"gorm.io/gorm"
)

// setupTotpFlowDB mirrors setupSecureVerifyFlowDB: hermetic SQLite with an
// enabled user, plus the user_totps table so the lazy ensure never races the
// assertions. Resets the in-memory replay/failure state.
func setupTotpFlowDB(t *testing.T, userId int) func() {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:totpflow%d?mode=memory&cache=shared", testDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &repo.Log{}, &repo.Option{}, &entity.UserTOTP{}, &entity.UserTOTPBackupCode{}} {
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

	if err := db.Create(&repo.User{
		Id:       userId,
		Username: fmt.Sprintf("totpuser%d", userId),
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Email:    fmt.Sprintf("totp%d@test.local", userId),
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

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

// buildTotpFlowRouter mounts the real handlers behind a stub auth that fixes
// the user id, with a real cookie session store so the step-up stamp flows
// through cookies exactly like production.
func buildTotpFlowRouter(userId int) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	store := cookie.NewStore([]byte("totp-flow-secret"))
	r.Use(sessions.Sessions("session", store))
	r.Use(func(c *gin.Context) {
		c.Set("id", userId)
		c.Next()
	})
	r.POST("/api/verify", UniversalVerify)
	r.GET("/api/verify/status", GetVerificationStatus)
	r.GET("/api/user/totp/status", GetTotpStatus)
	r.POST("/api/user/totp/enroll", TotpEnroll)
	r.POST("/api/user/totp/confirm", TotpConfirm)
	r.POST("/api/user/totp/disable", middleware.SecureVerificationRequired(), TotpDisable)
	r.POST("/api/user/totp/backup-codes/regenerate", middleware.SecureVerificationRequired(), RegenerateTotpBackupCodes)
	return r
}

type apiEnvelope struct {
	Success      bool            `json:"success"`
	Message      string          `json:"message"`
	Code         string          `json:"code"`
	TotpEnrolled *bool           `json:"totp_enrolled"`
	Data         json.RawMessage `json:"data"`
}

func doJSON(t *testing.T, r *gin.Engine, method, path, body string, cookies []*http.Cookie) (*httptest.ResponseRecorder, apiEnvelope) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env apiEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w, env
}

// TestTotpStepUp_EndToEnd drives the full lifecycle: session works while
// unenrolled → enroll+confirm → session is refused → valid TOTP passes →
// replay refused → disable behind the TOTP-stamped session → session works again.
func TestTotpStepUp_EndToEnd(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 1)
	defer cleanup()
	r := buildTotpFlowRouter(1)

	// 1. Unenrolled: method=session passes, response advertises totp_enrolled=false.
	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("unenrolled session verify: status=%d body=%s", w.Code, w.Body.String())
	}
	var vd struct {
		TotpEnrolled bool `json:"totp_enrolled"`
	}
	_ = json.Unmarshal(env.Data, &vd)
	if vd.TotpEnrolled {
		t.Fatalf("unenrolled verify should report totp_enrolled=false: %s", w.Body.String())
	}

	// 2. Enroll: returns secret + otpauth URL; stored secret must be encrypted.
	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/enroll", `{}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("enroll: status=%d body=%s", w.Code, w.Body.String())
	}
	var enrollData struct {
		Secret     string `json:"secret"`
		OtpauthURL string `json:"otpauth_url"`
	}
	if err := json.Unmarshal(env.Data, &enrollData); err != nil || enrollData.Secret == "" {
		t.Fatalf("enroll data missing secret: %s", w.Body.String())
	}
	if !strings.HasPrefix(enrollData.OtpauthURL, "otpauth://totp/") {
		t.Fatalf("enroll otpauth url malformed: %q", enrollData.OtpauthURL)
	}
	rec, err := repo.GetUserTOTP(1)
	if err != nil || rec == nil {
		t.Fatalf("stored enrollment missing: rec=%v err=%v", rec, err)
	}
	if rec.Enabled {
		t.Fatal("enrollment must stay pending until confirm")
	}
	if strings.Contains(rec.SecretEncrypted, enrollData.Secret) {
		t.Fatal("secret stored in plaintext")
	}

	// 3a. Pending enrollment does not change the verify policy yet.
	w, _ = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pending enrollment must not break session verify: status=%d body=%s", w.Code, w.Body.String())
	}

	// 3b. Confirm with a wrong code → rejected, still pending.
	w, _ = doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"000000"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("confirm with wrong code: status=%d body=%s", w.Code, w.Body.String())
	}

	// 3c. Confirm with the real current code → active.
	confirmCode, err := pqtotp.GenerateCode(enrollData.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate confirm code: %v", err)
	}
	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"`+confirmCode+`"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("confirm: status=%d body=%s", w.Code, w.Body.String())
	}
	rec, _ = repo.GetUserTOTP(1)
	if rec == nil || !rec.Enabled {
		t.Fatalf("enrollment not active after confirm: %+v", rec)
	}

	// 4. Enrolled: method=session must now be refused with TOTP_REQUIRED.
	w, env = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusForbidden || env.Code != "TOTP_REQUIRED" {
		t.Fatalf("enrolled session verify: status=%d code=%q body=%s", w.Code, env.Code, w.Body.String())
	}

	// 5. Enrolled: a valid TOTP code passes and stamps the session.
	// Use the previous 30s step so the code differs from the confirm code
	// (Validate accepts ±1 step) — the confirm code is already spent.
	verifyCode, err := pqtotp.GenerateCode(enrollData.Secret, time.Now().Add(-30*time.Second))
	if err != nil {
		t.Fatalf("generate verify code: %v", err)
	}
	if verifyCode == confirmCode {
		t.Skip("clock landed on a step boundary collision; rerun-safe skip")
	}
	w, env = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"`+verifyCode+`"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("totp verify: status=%d body=%s", w.Code, w.Body.String())
	}
	verifiedCookies := w.Result().Cookies()
	if len(verifiedCookies) == 0 {
		t.Fatal("totp verify did not stamp the session")
	}

	// 6. Replaying the same code inside the window is refused.
	w, _ = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"`+verifyCode+`"}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("replayed code: status=%d body=%s (want 403)", w.Code, w.Body.String())
	}

	// 7. Status endpoint reflects enrollment.
	w, _ = doJSON(t, r, http.MethodGet, "/api/user/totp/status", "", nil)
	var statusEnv apiEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &statusEnv)
	var st struct {
		Enrolled bool `json:"enrolled"`
	}
	_ = json.Unmarshal(statusEnv.Data, &st)
	if w.Code != http.StatusOK || !st.Enrolled {
		t.Fatalf("totp status: status=%d body=%s", w.Code, w.Body.String())
	}

	// 8. Disable without a verified session → step-up middleware refuses.
	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/disable", `{}`, nil)
	if w.Code != http.StatusForbidden || env.Code != "VERIFICATION_REQUIRED" {
		t.Fatalf("disable without step-up: status=%d code=%q body=%s", w.Code, env.Code, w.Body.String())
	}

	// 9. Disable with the TOTP-verified session cookie → allowed.
	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/disable", `{}`, verifiedCookies)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("disable with step-up: status=%d body=%s", w.Code, w.Body.String())
	}
	rec, _ = repo.GetUserTOTP(1)
	if rec != nil {
		t.Fatalf("enrollment should be deleted after disable: %+v", rec)
	}

	// 9b. Disable must also purge the backup codes minted at confirm (step
	// 3c) — a factor that no longer exists must not leave recovery codes
	// behind for it. Mutation: deleting repo.DeleteUserTOTPBackupCodes's
	// call site in TotpDisable leaves this count at 10.
	var remainingBackupCodes int64
	if err := repo.DB.Model(&entity.UserTOTPBackupCode{}).
		Where("user_id = ?", 1).Count(&remainingBackupCodes).Error; err != nil {
		t.Fatalf("count backup codes after disable: %v", err)
	}
	if remainingBackupCodes != 0 {
		t.Fatalf("backup code rows after disable = %d, want 0", remainingBackupCodes)
	}

	// 10. Back to legacy behavior: session verify passes again.
	w, _ = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("post-disable session verify: status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestTotpVerify_FailureThrottle proves wrong codes are throttled per user:
// after 5 failures the endpoint returns 429 even for further wrong codes.
func TestTotpVerify_FailureThrottle(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 2)
	defer cleanup()
	r := buildTotpFlowRouter(2)

	secret, _, err := apptotp.GenerateEnrollment("throttle-user")
	if err != nil {
		t.Fatalf("generate enrollment: %v", err)
	}
	enc, err := apptotp.EncryptSecret(secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := repo.UpsertUserTOTP(&entity.UserTOTP{
		UserId:          2,
		SecretEncrypted: enc,
		Enabled:         true,
		CreatedAt:       common.GetTimestamp(),
		ConfirmedAt:     common.GetTimestamp(),
	}); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}

	for i := 0; i < 5; i++ {
		w, _ := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"000000"}`, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("wrong code attempt %d: status=%d body=%s (want 403)", i+1, w.Code, w.Body.String())
		}
	}
	w, _ := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"000000"}`, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled attempt: status=%d body=%s (want 429)", w.Code, w.Body.String())
	}

	// Even a CORRECT code is refused while throttled — the limiter fires
	// before validation, so brute-forcers gain nothing from continuing.
	good, err := pqtotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	w, _ = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"`+good+`"}`, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("correct code while throttled: status=%d (want 429)", w.Code)
	}
}

// totpDataEnvelope decodes the {backup_codes, enrolled, pending,
// backup_codes_remaining} shapes confirm/status/regenerate return, without
// asserting on fields a given response does not carry.
type totpDataEnvelope struct {
	BackupCodes          []string `json:"backup_codes"`
	Enrolled             bool     `json:"enrolled"`
	Pending              bool     `json:"pending"`
	BackupCodesRemaining *int64   `json:"backup_codes_remaining"`
}

// enrollAndConfirm drives TotpEnroll + TotpConfirm to an active enrollment
// and returns the TOTP secret (for generating further codes) plus the
// backup codes confirm minted.
func enrollAndConfirm(t *testing.T, r *gin.Engine) (secret string, backupCodes []string) {
	t.Helper()
	secret, backupCodes, _ = enrollAndConfirmWithCode(t, r)
	return secret, backupCodes
}

// enrollAndConfirmWithCode is enrollAndConfirm plus the code confirm
// consumed. A caller that needs to step up afterwards has to know that code:
// the server refuses a replay of it, so a step-up code that happens to equal
// it fails for a reason unrelated to what the caller is measuring, and the
// caller can say so instead of treating it as a product failure.
func enrollAndConfirmWithCode(t *testing.T, r *gin.Engine) (secret string, backupCodes []string, confirmCode string) {
	t.Helper()
	w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/enroll", `{}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("enroll: status=%d body=%s", w.Code, w.Body.String())
	}
	var enrollData struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(env.Data, &enrollData); err != nil || enrollData.Secret == "" {
		t.Fatalf("enroll data missing secret: %s", w.Body.String())
	}
	code, err := pqtotp.GenerateCode(enrollData.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate confirm code: %v", err)
	}
	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"`+code+`"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("confirm: status=%d body=%s", w.Code, w.Body.String())
	}
	var confirmData totpDataEnvelope
	if err := json.Unmarshal(env.Data, &confirmData); err != nil {
		t.Fatalf("unmarshal confirm data: %v", err)
	}
	return enrollData.Secret, confirmData.BackupCodes, code
}

// TestTotpConfirm_ReturnsBackupCodesOnce: confirm mints 10 codes and returns
// them exactly once; status never carries the codes, only a remaining count;
// the DB holds only hashes — none of the returned plaintext codes appear as
// a stored code_hash value.
func TestTotpConfirm_ReturnsBackupCodesOnce(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 10)
	defer cleanup()
	r := buildTotpFlowRouter(10)

	_, backupCodes := enrollAndConfirm(t, r)
	if len(backupCodes) != apptotp.BackupCodeCount {
		t.Fatalf("confirm returned %d backup codes, want %d", len(backupCodes), apptotp.BackupCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range backupCodes {
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("backup code %q not in XXXX-XXXX form", c)
		}
		if seen[c] {
			t.Fatalf("duplicate backup code in one issuance: %q", c)
		}
		seen[c] = true
	}

	// Status must report the remaining count, never the codes themselves.
	w, _ := doJSON(t, r, http.MethodGet, "/api/user/totp/status", "", nil)
	var statusEnv apiEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &statusEnv); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	var statusData totpDataEnvelope
	if err := json.Unmarshal(statusEnv.Data, &statusData); err != nil {
		t.Fatalf("unmarshal status data: %v", err)
	}
	if len(statusData.BackupCodes) != 0 {
		t.Fatalf("status must never return backup codes, got %v", statusData.BackupCodes)
	}
	if statusData.BackupCodesRemaining == nil || *statusData.BackupCodesRemaining != int64(apptotp.BackupCodeCount) {
		t.Fatalf("status backup_codes_remaining = %v, want %d", statusData.BackupCodesRemaining, apptotp.BackupCodeCount)
	}

	// DB holds only one-way hashes: none of the returned plaintext codes
	// appear verbatim as a stored code_hash value.
	var storedHashes []string
	if err := repo.DB.Model(&entity.UserTOTPBackupCode{}).
		Where("user_id = ?", 10).Pluck("code_hash", &storedHashes).Error; err != nil {
		t.Fatalf("query stored hashes: %v", err)
	}
	if len(storedHashes) != apptotp.BackupCodeCount {
		t.Fatalf("stored backup code rows = %d, want %d", len(storedHashes), apptotp.BackupCodeCount)
	}
	for _, stored := range storedHashes {
		for _, plain := range backupCodes {
			if stored == plain {
				t.Fatalf("plaintext backup code %q stored verbatim as code_hash", plain)
			}
		}
	}
}

// TestTotpConfirm_BackupCodeMintFailureLeavesEnrollmentPending locks the
// ordering fix: if minting backup codes fails, the enrollment must stay
// pending (Enabled=false), not go live with zero backup codes stored and no
// way to self-service recover. Mutation: swapping issueBackupCodesFn's call
// back to AFTER the rec.Enabled=true/UpsertUserTOTP write (the original
// order) makes the post-failure GetUserTOTP assertion below fail (rec.Enabled
// would be true).
func TestTotpConfirm_BackupCodeMintFailureLeavesEnrollmentPending(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 12)
	defer cleanup()
	r := buildTotpFlowRouter(12)

	w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/enroll", `{}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("enroll: status=%d body=%s", w.Code, w.Body.String())
	}
	var enrollData struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(env.Data, &enrollData); err != nil || enrollData.Secret == "" {
		t.Fatalf("enroll data missing secret: %s", w.Body.String())
	}

	code1, err := pqtotp.GenerateCode(enrollData.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	prevIssue := issueBackupCodesFn
	issueBackupCodesFn = func(int) ([]string, error) {
		return nil, fmt.Errorf("simulated backup-code mint failure")
	}
	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"`+code1+`"}`, nil)
	issueBackupCodesFn = prevIssue
	// common.ApiError responds 200/success:false (this codebase's error
	// convention — see internal/pkg/common/gin.go), not an HTTP 5xx.
	if w.Code != http.StatusOK || env.Success {
		t.Fatalf("confirm with a forced mint failure: status=%d body=%s (want 200/success:false)", w.Code, w.Body.String())
	}

	rec, err := repo.GetUserTOTP(12)
	if err != nil {
		t.Fatalf("GetUserTOTP after failed confirm: %v", err)
	}
	if rec == nil || rec.Enabled {
		t.Fatalf("enrollment must stay PENDING after a mint failure, got %+v", rec)
	}
	var codeRows int64
	repo.DB.Model(&entity.UserTOTPBackupCode{}).Where("user_id = ?", 12).Count(&codeRows)
	if codeRows != 0 {
		t.Fatalf("no backup code rows should exist after a mint failure, got %d", codeRows)
	}

	// The pending enrollment must still be confirmable with a fresh code
	// once minting works again — the failure above must not have wedged it.
	code2, err := pqtotp.GenerateCode(enrollData.Secret, time.Now().Add(-30*time.Second))
	if err != nil {
		t.Fatalf("generate second code: %v", err)
	}
	if code2 == code1 {
		t.Skip("clock landed on a step boundary collision; rerun-safe skip")
	}
	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/confirm", `{"code":"`+code2+`"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("retry confirm after mint failure: status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestUniversalVerify_BackupCodeConsumedOnce: a fresh backup code passes
// UniversalVerify and stamps the session exactly like a TOTP code; replaying
// the SAME code is refused (fail-closed anti-replay — mutation: removing
// "AND used_at=0" from the consuming UPDATE turns the second call green too).
func TestUniversalVerify_BackupCodeConsumedOnce(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 11)
	defer cleanup()
	r := buildTotpFlowRouter(11)

	_, backupCodes := enrollAndConfirm(t, r)
	code := backupCodes[0]

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp_backup","code":"`+code+`"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("first backup-code verify: status=%d body=%s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("backup-code verify did not stamp the session")
	}

	w, _ = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp_backup","code":"`+code+`"}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("replayed backup code: status=%d body=%s (want 403)", w.Code, w.Body.String())
	}

	// The remaining count dropped by exactly one.
	w, _ = doJSON(t, r, http.MethodGet, "/api/user/totp/status", "", nil)
	var statusEnv apiEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &statusEnv)
	var statusData totpDataEnvelope
	_ = json.Unmarshal(statusEnv.Data, &statusData)
	if statusData.BackupCodesRemaining == nil || *statusData.BackupCodesRemaining != int64(apptotp.BackupCodeCount-1) {
		t.Fatalf("backup_codes_remaining after one consume = %v, want %d", statusData.BackupCodesRemaining, apptotp.BackupCodeCount-1)
	}
}

// TestUniversalVerify_BackupCodeThrottled proves wrong backup codes consume
// the SAME per-user failure budget as wrong TOTP codes (§8 L6): 5 wrong
// backup-code attempts throttle the endpoint even for a subsequently
// presented, genuinely valid TOTP code.
func TestUniversalVerify_BackupCodeThrottled(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 12)
	defer cleanup()
	r := buildTotpFlowRouter(12)

	secret, _ := enrollAndConfirm(t, r)

	for i := 0; i < 5; i++ {
		w, _ := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp_backup","code":"ZZZZ-ZZZZ"}`, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("wrong backup code attempt %d: status=%d body=%s (want 403)", i+1, w.Code, w.Body.String())
		}
	}
	w, _ := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp_backup","code":"ZZZZ-ZZZZ"}`, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("throttled backup-code attempt: status=%d body=%s (want 429)", w.Code, w.Body.String())
	}

	good, err := pqtotp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	w, _ = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"`+good+`"}`, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("correct TOTP code while backup-throttled: status=%d (want 429)", w.Code)
	}
}

// TestBackupCodes_RegenerateInvalidatesUnused: regenerate requires step-up
// (like disable), returns a fresh set of codes, and every old code — even
// ones never consumed — stops working.
func TestBackupCodes_RegenerateInvalidatesUnused(t *testing.T) {
	cleanup := setupTotpFlowDB(t, 13)
	defer cleanup()
	r := buildTotpFlowRouter(13)

	secret, oldCodes, confirmCode := enrollAndConfirmWithCode(t, r)

	// Without step-up, regenerate is refused exactly like disable.
	w, env := doJSON(t, r, http.MethodPost, "/api/user/totp/backup-codes/regenerate", `{}`, nil)
	if w.Code != http.StatusForbidden || env.Code != "VERIFICATION_REQUIRED" {
		t.Fatalf("regenerate without step-up: status=%d code=%q body=%s", w.Code, env.Code, w.Body.String())
	}

	// Step up with a real TOTP code. The code is taken from the PREVIOUS
	// 30-second step because confirm just consumed the current one and the
	// server refuses a replay.
	verifyCode, err := pqtotp.GenerateCode(secret, time.Now().Add(-30*time.Second))
	if err != nil {
		t.Fatalf("generate verify code: %v", err)
	}
	// The only legitimate skip: the clock crossed a step boundary between
	// confirm and here, so "the previous step" IS the step confirm spent and
	// the replay guard would refuse it for a reason this test is not about.
	// Every OTHER non-200 is a real failure — the blanket skip this replaced
	// meant a step-up route that had stopped granting anything at all still
	// reported a green run.
	if verifyCode == confirmCode {
		t.Skip("verify code equals the code confirm already consumed (step boundary); rerun-safe skip")
	}
	w, env = doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"`+verifyCode+`"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("step-up with a fresh TOTP code: status=%d body=%s (want 200 — regenerate is gated on it)",
			w.Code, w.Body.String())
	}
	verifiedCookies := w.Result().Cookies()

	w, env = doJSON(t, r, http.MethodPost, "/api/user/totp/backup-codes/regenerate", `{}`, verifiedCookies)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("regenerate with step-up: status=%d body=%s", w.Code, w.Body.String())
	}
	var regenData totpDataEnvelope
	if err := json.Unmarshal(env.Data, &regenData); err != nil {
		t.Fatalf("unmarshal regenerate data: %v", err)
	}
	if len(regenData.BackupCodes) != apptotp.BackupCodeCount {
		t.Fatalf("regenerate returned %d codes, want %d", len(regenData.BackupCodes), apptotp.BackupCodeCount)
	}
	for _, nc := range regenData.BackupCodes {
		for _, oc := range oldCodes {
			if nc == oc {
				t.Fatalf("regenerate reissued an old code verbatim: %q", nc)
			}
		}
	}

	// Every old code — none of which was ever consumed — is now dead.
	for i, oc := range oldCodes {
		w, _ := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp_backup","code":"`+oc+`"}`, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("old code %d (%q) still works after regenerate: status=%d", i, oc, w.Code)
		}
		if i >= 3 {
			// Stay well under the 5-attempt failure throttle for this test's
			// purpose (proving invalidation, not re-testing the throttle).
			break
		}
	}
}
