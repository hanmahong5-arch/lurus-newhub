package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	apptotp "github.com/LurusTech/lurus-hub/internal/app/totp"
	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	pqtotp "github.com/pquerna/otp/totp"
	"time"
)

// setupStepUpAuditDB mirrors setupTotpFlowDB but also migrates the audit
// tables and wires a real (SQLite-backed) governance.AuditWriter, so
// RecordAuditEvent calls made by UniversalVerify land in rows this test can
// poll for — see pollAuditRow / pinnedAuditWriter (v2_pricing_write_test.go,
// same package).
func setupStepUpAuditDB(t *testing.T, userId int) func() {
	t.Helper()
	cleanup := setupTotpFlowDB(t, userId)
	if err := repo.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("automigrate audit tables: %v", err)
	}
	governance.SetAuditWriter(&pinnedAuditWriter{db: repo.DB})
	return cleanup
}

// TestUniversalVerify_NoEnrollment_WritesCredentialFreeAuditRow proves the
// unconditional half of L3: a user with no TOTP enrollment who steps up via
// method "session" — presenting no credential beyond an existing session —
// gets an audit row naming them, even with the enforcement flag off
// (default). Deleting the RecordAuditEvent call in the no-enrollment branch
// turns this red.
func TestUniversalVerify_NoEnrollment_WritesCredentialFreeAuditRow(t *testing.T) {
	cleanup := setupStepUpAuditDB(t, 3)
	defer cleanup()
	r := buildTotpFlowRouter(3)

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusOK || !env.Success {
		t.Fatalf("verify: status=%d success=%v body=%s", w.Code, env.Success, w.Body.String())
	}

	ev := pollAuditRow(t, governance.ActionAuthStepUpNoCredential, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionAuthStepUpNoCredential)
	}
	if ev.ActorID != 3 {
		t.Errorf("ActorID = %d, want 3", ev.ActorID)
	}
	if ev.Resource != governance.ResourceUser {
		t.Errorf("Resource = %q, want %q", ev.Resource, governance.ResourceUser)
	}
	if ev.ResourceID != 3 {
		t.Errorf("ResourceID = %d, want 3", ev.ResourceID)
	}
}

// TestUniversalVerify_NoEnrollment_FlagOn_403EnrollmentRequired proves part 2:
// with SECURE_VERIFICATION_REQUIRE_ENROLLMENT=true, a no-enrollment user
// cannot obtain step-up verification, AND the session key is not set — a
// handler that verified-then-403'd would still pass a status-code-only
// assertion, so this drives the real status endpoint afterward instead of
// inspecting the session store directly.
func TestUniversalVerify_NoEnrollment_FlagOn_403EnrollmentRequired(t *testing.T) {
	cleanup := setupStepUpAuditDB(t, 4)
	defer cleanup()
	t.Setenv("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", "true")
	r := buildTotpFlowRouter(4)

	w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("verify: status=%d want 403; body=%s", w.Code, w.Body.String())
	}
	if env.Code != "STEP_UP_ENROLLMENT_REQUIRED" {
		t.Errorf("code = %q, want STEP_UP_ENROLLMENT_REQUIRED", env.Code)
	}

	// The session key must not have been set: replay whatever cookies came
	// back (there may be none, or an unauthenticated session cookie) against
	// the real status endpoint and confirm it reports unverified.
	statusW, statusEnv := doJSON(t, r, http.MethodGet, "/api/verify/status", "", w.Result().Cookies())
	if statusW.Code != http.StatusOK {
		t.Fatalf("status: status=%d body=%s", statusW.Code, statusW.Body.String())
	}
	var data VerificationStatusResponse
	if err := json.Unmarshal(statusEnv.Data, &data); err != nil {
		t.Fatalf("unmarshal status data: %v", err)
	}
	if data.Verified {
		t.Error("Verified = true, want false — the flag-on 403 branch must not set the session key")
	}
}

// TestUniversalVerify_Enrolled_UnchangedUnderBothFlagStates proves L3 does
// not touch the enrolled-TOTP path: a correct code passes under the flag's
// default (off) and its enforce (on) state identically, and method:"session"
// is still rejected (403 TOTP_REQUIRED, not the no-enrollment codes) either
// way.
func TestUniversalVerify_Enrolled_UnchangedUnderBothFlagStates(t *testing.T) {
	for _, flagOn := range []bool{false, true} {
		flagOn := flagOn
		t.Run(map[bool]string{false: "flag_off", true: "flag_on"}[flagOn], func(t *testing.T) {
			cleanup := setupStepUpAuditDB(t, 5)
			defer cleanup()
			if flagOn {
				t.Setenv("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", "true")
			}
			r := buildTotpFlowRouter(5)

			secret, _, err := apptotp.GenerateEnrollment("enrolled-user")
			if err != nil {
				t.Fatalf("generate enrollment: %v", err)
			}
			enc, err := apptotp.EncryptSecret(secret)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			if err := repo.UpsertUserTOTP(&entity.UserTOTP{
				UserId:          5,
				SecretEncrypted: enc,
				Enabled:         true,
				CreatedAt:       common.GetTimestamp(),
				ConfirmedAt:     common.GetTimestamp(),
			}); err != nil {
				t.Fatalf("seed enrollment: %v", err)
			}

			// method "session" is still rejected — never falls through to the
			// no-enrollment codes regardless of the flag.
			w, env := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
			if w.Code != http.StatusForbidden || env.Code != "TOTP_REQUIRED" {
				t.Fatalf("session-method on enrolled user: status=%d code=%q, want 403 TOTP_REQUIRED", w.Code, env.Code)
			}

			good, err := pqtotp.GenerateCode(secret, time.Now())
			if err != nil {
				t.Fatalf("generate code: %v", err)
			}
			w2, env2 := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"totp","code":"`+good+`"}`, nil)
			if w2.Code != http.StatusOK || !env2.Success {
				t.Fatalf("totp verify: status=%d success=%v body=%s", w2.Code, env2.Success, w2.Body.String())
			}
		})
	}
}

// TestVerificationStatus_ReportsEnrollmentRequired proves part 3 (honesty):
// the status endpoint reports the actual policy rather than letting the
// console assume the legacy one.
func TestVerificationStatus_ReportsEnrollmentRequired(t *testing.T) {
	for _, flagOn := range []bool{false, true} {
		flagOn := flagOn
		t.Run(map[bool]string{false: "flag_off", true: "flag_on"}[flagOn], func(t *testing.T) {
			cleanup := setupStepUpAuditDB(t, 6)
			defer cleanup()
			if flagOn {
				t.Setenv("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", "true")
			}
			r := buildTotpFlowRouter(6)

			w, env := doJSON(t, r, http.MethodGet, "/api/verify/status", "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status: status=%d body=%s", w.Code, w.Body.String())
			}
			var data VerificationStatusResponse
			if err := json.Unmarshal(env.Data, &data); err != nil {
				t.Fatalf("unmarshal status data: %v", err)
			}
			if data.EnrollmentRequired != flagOn {
				t.Errorf("EnrollmentRequired = %v, want %v", data.EnrollmentRequired, flagOn)
			}
		})
	}
}
