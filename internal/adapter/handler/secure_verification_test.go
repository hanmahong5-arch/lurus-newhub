package handler

import (
	"encoding/json"
	"net/http"
	"strings"
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
	// The pinned writer above holds repo.DB, which the caller's deferred
	// cleanup() closes when the test returns. Without this, the global
	// governance writer keeps pointing at a closed handle until some later
	// test happens to call SetAuditWriter again, and any audit write in
	// between (including a delayed async one from this test) logs "sql:
	// database is closed" noise. Reset to an in-memory recorder (same type
	// v2_models_write_test.go uses for the same purpose) once this test is
	// fully done.
	t.Cleanup(func() { governance.SetAuditWriter(&modelsWriteAuditRecorder{}) })
	return cleanup
}

// TestUniversalVerify_NoEnrollment_WritesCredentialFreeAuditRow proves the
// half that does not depend on the flag: a user with no TOTP enrollment who steps up via
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

	// The refusal itself is audited (same ActionAuthFailed precedent as the
	// TOTP-throttle refusal above it), so an operator who enables
	// enforcement can see blocked step-up attempts, not just granted ones.
	ev := pollAuditRow(t, governance.ActionAuthFailed, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionAuthFailed)
	}
	if ev.ActorID != 4 {
		t.Errorf("ActorID = %d, want 4", ev.ActorID)
	}
	if !strings.Contains(ev.Details, `"reason":"enrollment_required"`) {
		t.Errorf("Details = %q, want reason=enrollment_required", ev.Details)
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
// console assume the legacy one — for BOTH the unverified branch (no
// session stamped yet) and the verified=true branch the console actually
// reads right after a successful step-up.
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
			if data.Verified {
				t.Fatalf("unverified probe: Verified = true, want false")
			}
			if data.EnrollmentRequired != flagOn {
				t.Errorf("unverified: EnrollmentRequired = %v, want %v", data.EnrollmentRequired, flagOn)
			}

			// Now stamp a session. The no-enrollment branch only grants
			// while the flag is OFF, so momentarily clear it to obtain the
			// session (a real client would have enrolled a factor instead
			// of the flag ever having been on in the first place — this is
			// purely to get a verified session into the store for the
			// probe below); then restore the flag to the state under test
			// and re-read status: the flag is read fresh per call, so this
			// proves the verified=true response (the one the console reads
			// right after a successful step-up) also reports the real
			// policy instead of a stale zero value.
			t.Setenv("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", "false")
			verifyW, verifyEnv := doJSON(t, r, http.MethodPost, "/api/verify", `{"method":"session"}`, nil)
			if verifyW.Code != http.StatusOK || !verifyEnv.Success {
				t.Fatalf("stamp session: status=%d success=%v body=%s", verifyW.Code, verifyEnv.Success, verifyW.Body.String())
			}
			cookies := verifyW.Result().Cookies()

			if flagOn {
				t.Setenv("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", "true")
			} else {
				t.Setenv("SECURE_VERIFICATION_REQUIRE_ENROLLMENT", "false")
			}

			w2, env2 := doJSON(t, r, http.MethodGet, "/api/verify/status", "", cookies)
			if w2.Code != http.StatusOK {
				t.Fatalf("status (verified): status=%d body=%s", w2.Code, w2.Body.String())
			}
			var data2 VerificationStatusResponse
			if err := json.Unmarshal(env2.Data, &data2); err != nil {
				t.Fatalf("unmarshal status data (verified): %v", err)
			}
			if !data2.Verified {
				t.Fatalf("verified probe: Verified = false, want true")
			}
			if data2.EnrollmentRequired != flagOn {
				t.Errorf("verified: EnrollmentRequired = %v, want %v", data2.EnrollmentRequired, flagOn)
			}
		})
	}
}
