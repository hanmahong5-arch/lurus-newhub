package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
)

// billingUnifiedEnabled is an atomic flag controlling the pre-authorize billing path.
// Use BillingUnifiedEnabled() to read and SetBillingUnifiedEnabled() to write.
// 1 = enabled, 0 = disabled (default).
var billingUnifiedEnabled int32

// BillingUnifiedEnabled reports whether the unified billing path (freeze/settle/release)
// is active. Safe for concurrent use without a lock.
func BillingUnifiedEnabled() bool {
	return atomic.LoadInt32(&billingUnifiedEnabled) == 1
}

// SetBillingUnifiedEnabled updates the unified-billing flag atomically.
// Called at init time (from env) and by the platform config poller.
func SetBillingUnifiedEnabled(v bool) {
	var n int32
	if v {
		n = 1
	}
	atomic.StoreInt32(&billingUnifiedEnabled, n)
}

// localLedgerAdvisory is an atomic flag demoting the LOCAL user-quota ledger
// to shadow bookkeeping for platform-governed requests (Track A single-ledger
// convergence). When true AND the request was admitted by the platform wallet
// (RelayInfo.PlatformGoverned), the local user-balance 402 gate is skipped and
// local pre/post-deduct failures log-and-continue instead of blocking the
// relay/settle — the platform wallet is the sole source of truth; local writes
// still happen so the shadow ledger keeps feeding drift reconciliation.
// Requests the platform never admitted (legacy tokens, unlinked users, flag-off
// windows) keep the full local gate — advisory NEVER opens the door for
// traffic the platform hasn't vouched for.
// Use LocalLedgerAdvisory() to read and SetLocalLedgerAdvisory() to write.
// 1 = advisory, 0 = enforcing (default).
var localLedgerAdvisory int32

// LocalLedgerAdvisory reports whether the local quota ledger is advisory
// (shadow) for platform-governed requests. Safe for concurrent use.
func LocalLedgerAdvisory() bool {
	return atomic.LoadInt32(&localLedgerAdvisory) == 1
}

// SetLocalLedgerAdvisory updates the local-ledger-advisory flag atomically.
// Called at init time (from env) and by the platform config poller, so an
// operator flip on the platform propagates fleet-wide within one poll cycle.
func SetLocalLedgerAdvisory(v bool) {
	var n int32
	if v {
		n = 1
	}
	atomic.StoreInt32(&localLedgerAdvisory, n)
}

func init() {
	// Safe default only: package init runs BEFORE main() loads .env, so a
	// .env-only BILLING_UNIFIED_ENABLED=true would not be visible here yet.
	// The authoritative read happens in RefreshIdentityClientEnv (identity_client.go),
	// called post-.env from InitResources.
	SetBillingUnifiedEnabled(os.Getenv("BILLING_UNIFIED_ENABLED") == "true")
	SetLocalLedgerAdvisory(os.Getenv("LOCAL_LEDGER_ADVISORY") == "true")
}

// PreAuthResult holds the response from a wallet pre-authorization call.
type PreAuthResult struct {
	PreAuthID int64   `json:"preauth_id"`
	Amount    float64 `json:"amount"`
	Status    string  `json:"status"`
	ExpiresAt string  `json:"expires_at"`
}

// PreAuthorize freezes an estimated amount in the wallet before LLM relay.
func PreAuthorize(ctx context.Context, accountID int64, amount float64, productID, referenceID, description string, ttlSeconds int) (*PreAuthResult, error) {
	if IdentityServiceURL == "" {
		return nil, fmt.Errorf("billing service not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"amount":       amount,
		"product_id":   productID,
		"reference_id": referenceID,
		"description":  description,
		"ttl_seconds":  ttlSeconds,
	})
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/wallet/pre-authorize", IdentityServiceURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("billing service unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)
	// Platform requires an Idempotency-Key on this money endpoint (else 400). The
	// relay request reference is the natural per-freeze dedupe key.
	if referenceID != "" {
		req.Header.Set("Idempotency-Key", referenceID)
	}

	resp, err := identityClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("billing service unavailable")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		reason := parseErrorResponse(resp.Body)
		if reason == "insufficient_balance" {
			return nil, fmt.Errorf("insufficient_balance")
		}
		SysLog(fmt.Sprintf("pre-authorize rejected: account=%d, status=%d, reason=%s", accountID, resp.StatusCode, reason))
		return nil, fmt.Errorf("insufficient_balance")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		SysLog(fmt.Sprintf("pre-authorize failed: account=%d, status=%d", accountID, resp.StatusCode))
		return nil, fmt.Errorf("billing service unavailable")
	}
	var result PreAuthResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("billing service error")
	}
	return &result, nil
}

// SettlePreAuthResult holds the response from a settle call.
type SettlePreAuthResult struct {
	PreAuthID    int64   `json:"preauth_id"`
	Status       string  `json:"status"`
	HeldAmount   float64 `json:"held_amount"`
	ActualAmount float64 `json:"actual_amount"`
}

// SettlePreAuth settles a pre-authorization with the actual consumed amount.
func SettlePreAuth(ctx context.Context, preAuthID int64, actualAmount float64) (*SettlePreAuthResult, error) {
	if IdentityServiceURL == "" {
		return nil, fmt.Errorf("billing service not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"actual_amount": actualAmount,
	})
	url := fmt.Sprintf("%s/internal/v1/wallet/pre-auth/%d/settle", IdentityServiceURL, preAuthID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("settle request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)
	// Settle is keyed on the pre-auth row id — a retried settle must not charge twice.
	req.Header.Set("Idempotency-Key", fmt.Sprintf("settle:%d", preAuthID))

	resp, err := identityClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("settle: billing service unreachable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		reason := parseErrorResponse(resp.Body)
		SysLog(fmt.Sprintf("settle failed: preauth=%d, status=%d, reason=%s", preAuthID, resp.StatusCode, reason))
		return nil, fmt.Errorf("settle failed: %s", reason)
	}
	var result SettlePreAuthResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("settle response decode failed")
	}
	return &result, nil
}

// ReleasePreAuth cancels a pre-authorization and unfreezes the held amount.
func ReleasePreAuth(ctx context.Context, preAuthID int64) error {
	if IdentityServiceURL == "" {
		return fmt.Errorf("billing service not configured")
	}
	url := fmt.Sprintf("%s/internal/v1/wallet/pre-auth/%d/release", IdentityServiceURL, preAuthID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("release request failed")
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)
	// Release is keyed on the pre-auth row id — a retried release is a safe no-op.
	req.Header.Set("Idempotency-Key", fmt.Sprintf("release:%d", preAuthID))

	resp, err := identityClient.Do(req)
	if err != nil {
		return fmt.Errorf("release: billing service unreachable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		reason := parseErrorResponse(resp.Body)
		SysLog(fmt.Sprintf("release failed: preauth=%d, status=%d, reason=%s", preAuthID, resp.StatusCode, reason))
		return fmt.Errorf("release failed: %s", reason)
	}
	return nil
}

// parseErrorResponse extracts the "error" field from a JSON error response body.
// Returns "unknown" if parsing fails. Consumes the reader.
func parseErrorResponse(body io.Reader) string {
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&errResp); err == nil && errResp.Error != "" {
		return errResp.Error
	}
	return "unknown"
}
