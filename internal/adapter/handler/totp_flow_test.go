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
	for _, tbl := range []interface{}{&repo.User{}, &repo.Log{}, &repo.Option{}, &entity.UserTOTP{}} {
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
