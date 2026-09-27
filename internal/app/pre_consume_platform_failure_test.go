package app

// pre_consume_platform_failure_test.go — the four shapes a platform pre-auth
// failure can take and the response each one owes the customer. Before
// cycle 18 L2 platformPreAuthorize dropped the error on the floor and
// answered every shape the same way: 402, "insufficient balance or billing
// service unavailable", a top-up link, no error-log row. A funded customer
// hit by a platform timeout was told to top up, their SDK did not retry
// (402 is terminal), and relay_errors_total filed the outage under
// insufficient_quota. The breaker only opens after 3 consecutive failures
// (BillingBreakerDefaultThreshold) and the degrade path is only admissible
// once it is open, so the first two timeouts of every outage took this exact
// path.
//
// Harness: l3_pre_consume_topup_url_test.go's (setupServiceTestDB,
// seedPoolTables, seedTestUser, the preAuthorizeWithBreaker seam), breaker
// pinned closed so TryDegradedPreAuth cannot admit any of the cases.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// preAuthFailing runs PreConsumeQuota against a platform whose pre-auth call
// fails with preAuthErr, on a closed breaker, for a user whose local ledger
// would otherwise admit the request.
func preAuthFailing(t *testing.T, preAuthErr error) *types.NewAPIError {
	t.Helper()
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)

	prevUnified := common.BillingUnifiedEnabled()
	common.SetBillingUnifiedEnabled(true)
	t.Cleanup(func() { common.SetBillingUnifiedEnabled(prevUnified) })

	common.BillingBreakerSuccess()
	t.Cleanup(common.BillingBreakerSuccess)

	prevPreAuth := preAuthorizeWithBreaker
	preAuthorizeWithBreaker = func(context.Context, int64, float64, string, string, string, int) (*common.PreAuthResult, error) {
		return nil, preAuthErr
	}
	t.Cleanup(func() { preAuthorizeWithBreaker = prevPreAuth })

	userId := seedTestUser(t, db, 50_000)
	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/relay", nil)
	c.Set("tenant_id", "l2-preauth-shape")

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		IdentityAccountID: 424242,
		TokenUnlimited:    true,
		OriginModelName:   "gpt-4",
	}
	apiErr := PreConsumeQuota(c, 1_000, relayInfo)
	if apiErr == nil {
		t.Fatalf("PreConsumeQuota admitted the request although pre-auth failed with %v", preAuthErr)
	}
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d after a failed pre-auth, want 0", relayInfo.PlatformPreAuthID)
	}
	return apiErr
}

func errorMetadata(t *testing.T, apiErr *types.NewAPIError) map[string]any {
	t.Helper()
	raw := apiErr.ToOpenAIError().Metadata
	if len(raw) == 0 {
		return map[string]any{}
	}
	meta := map[string]any{}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal metadata %s: %v", raw, err)
	}
	return meta
}

func TestPreConsumeQuota_PlatformPreAuthFailure_ShapeByCause(t *testing.T) {
	cases := []struct {
		name            string
		err             error
		wantStatus      int
		wantCode        string
		wantMessagePart string
		wantTopupURL    bool
		wantErrorLog    bool
		wantRetryAfter  bool
	}{
		{
			name:         "insufficient balance is the customer's 402 with the top-up remedy",
			err:          common.ErrInsufficientBalance,
			wantStatus:   http.StatusPaymentRequired,
			wantCode:     "insufficient_user_quota",
			wantTopupURL: true,
			wantErrorLog: false,
		},
		{
			name:            "platform verdict on the account is a 402 that names the platform's reason",
			err:             &common.PlatformRejectedError{Op: "pre-authorize", Status: 400, Reason: "wallet_frozen"},
			wantStatus:      http.StatusPaymentRequired,
			wantMessagePart: "wallet_frozen",
			wantTopupURL:    false,
			wantErrorLog:    true,
		},
		{
			name:           "platform timeout is our outage, 503 with Retry-After",
			err:            context.DeadlineExceeded,
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "billing_unavailable",
			wantErrorLog:   true,
			wantRetryAfter: true,
		},
		{
			name:           "breaker open is our outage, 503 with Retry-After",
			err:            fmt.Errorf("billing service temporarily unavailable (circuit open, retry in 15s)"),
			wantStatus:     http.StatusServiceUnavailable,
			wantCode:       "billing_unavailable",
			wantErrorLog:   true,
			wantRetryAfter: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := preAuthFailing(t, tc.err)

			if apiErr.StatusCode != tc.wantStatus {
				t.Errorf("StatusCode = %d, want %d (message %q)", apiErr.StatusCode, tc.wantStatus, apiErr.Error())
			}
			if tc.wantCode != "" && string(apiErr.GetErrorCode()) != tc.wantCode {
				t.Errorf("errorCode = %q, want %q", apiErr.GetErrorCode(), tc.wantCode)
			}
			if tc.wantMessagePart != "" && !strings.Contains(apiErr.Error(), tc.wantMessagePart) {
				t.Errorf("message %q does not name the platform's reason %q", apiErr.Error(), tc.wantMessagePart)
			}
			if !types.IsSkipRetryError(apiErr) {
				t.Errorf("SkipRetry not set — another channel cannot fix a billing verdict or outage")
			}
			if got := types.IsRecordErrorLog(apiErr); got != tc.wantErrorLog {
				t.Errorf("IsRecordErrorLog = %v, want %v", got, tc.wantErrorLog)
			}
			_, hasTopup := errorMetadata(t, apiErr)["topup_url"]
			if hasTopup != tc.wantTopupURL {
				t.Errorf("topup_url present = %v, want %v (metadata %s)", hasTopup, tc.wantTopupURL, apiErr.ToOpenAIError().Metadata)
			}
			if tc.wantStatus == http.StatusServiceUnavailable {
				if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
					t.Errorf("an outage must not carry the customer-out-of-money code")
				}
				if bucket := types.RelayErrorType(apiErr); bucket == "insufficient_quota" {
					t.Errorf("RelayErrorType = %q — the dashboard would file a billing outage under customers running out of money", bucket)
				}
			}
			if tc.wantRetryAfter && apiErr.RetryAfterUnix <= 0 {
				t.Errorf("RetryAfterUnix = %d, want > 0 so the relay renders Retry-After", apiErr.RetryAfterUnix)
			}
		})
	}
}

// TestPlatformPreAuthIDClearedOnlyAtTheHoldsTwoExits is the source gate for
// the de-duplication: a pre-auth hold ends in exactly two ways — abandoned
// (released, or parked for release) or settled (or parked for settlement) —
// and the id is cleared only inside the helper that performs each. Every
// other site used to hand-copy `releasePlatformPreAuth(relayInfo);
// relayInfo.PlatformPreAuthID = 0`, and one of them (ReturnPreConsumedQuota)
// disagreed with the helper's own comment about whether to clear at all.
func TestPlatformPreAuthIDClearedOnlyAtTheHoldsTwoExits(t *testing.T) {
	allowed := map[string]bool{"abandonPreAuth": true, "settleOrPark": true}
	clearRe := regexp.MustCompile(`\bPlatformPreAuthID\s*=\s*0\b`)
	funcRe := regexp.MustCompile(`^func (?:\([^)]*\) )?(\w+)`)

	root := filepath.Join("..", "..", "internal")
	var offenders []string
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if !clearRe.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			fn := "<no enclosing func>"
			for j := i; j >= 0; j-- {
				if m := funcRe.FindStringSubmatch(lines[j]); m != nil {
					fn = m[1]
					break
				}
			}
			seen[fn] = true
			if !allowed[fn] {
				offenders = append(offenders, fmt.Sprintf("%s:%d in %s", filepath.ToSlash(path), i+1, fn))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("PlatformPreAuthID cleared outside abandonPreAuth/settleOrPark — call the helper instead:\n  %s",
			strings.Join(offenders, "\n  "))
	}
	for fn := range allowed {
		if !seen[fn] {
			t.Errorf("%s no longer clears PlatformPreAuthID — the gate is measuring nothing", fn)
		}
	}
}
