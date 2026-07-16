package common

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	// billingConfigPollInterval is how often the poller re-fetches the billing config from the platform.
	billingConfigPollInterval = 30 * time.Second
	// billingConfigPath is the platform endpoint that returns runtime billing flags.
	billingConfigPath = "/internal/v1/billing-config"
	// billingConfigRequestTimeout caps each individual poll HTTP call.
	billingConfigRequestTimeout = 5 * time.Second
)

// billingConfigResponse mirrors the fields we read from the platform billing-config endpoint.
type billingConfigResponse struct {
	UnifiedBillingEnabled bool `json:"unified_billing_enabled"`
	// LocalLedgerAdvisory is a pointer so a platform that predates the field
	// (absent key) keeps the current value instead of force-resetting to false
	// every poll — deploy-order safe during the Track A rollout.
	LocalLedgerAdvisory *bool `json:"local_ledger_advisory"`
}

// StartBillingConfigPoller launches a background goroutine that polls the platform
// billing-config endpoint every 30 seconds and calls SetBillingUnifiedEnabled with
// the result.  It is a no-op (returns immediately without spawning) when
// IDENTITY_SERVICE_URL is empty — in that case the env-seeded default from init()
// remains in effect.
//
// On any error (network, non-200, parse failure) the goroutine logs a WARN and
// leaves the current flag value unchanged (fail-keep, never flip on error).
//
// The goroutine stops cleanly when ctx is cancelled.
func StartBillingConfigPoller(ctx context.Context) {
	if IdentityServiceURL == "" {
		return
	}

	go func() {
		// Poll once immediately at startup before the first tick.
		fetchAndApplyBillingConfig(ctx)

		ticker := time.NewTicker(billingConfigPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fetchAndApplyBillingConfig(ctx)
			}
		}
	}()
}

// fetchAndApplyBillingConfig performs one HTTP GET to the platform billing-config
// endpoint and updates the atomic flag.  Any failure leaves the flag unchanged.
func fetchAndApplyBillingConfig(ctx context.Context) {
	pollCtx, cancel := context.WithTimeout(ctx, billingConfigRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(pollCtx, http.MethodGet,
		IdentityServiceURL+billingConfigPath, nil)
	if err != nil {
		SysLog(fmt.Sprintf("billing_config_poll: build request failed: %v", err))
		return
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("billing_config_poll: WARN request failed (keeping current value): %v", err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		SysLog(fmt.Sprintf("billing_config_poll: WARN non-200 status %d (keeping current value)", resp.StatusCode))
		return
	}

	var cfg billingConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		SysLog(fmt.Sprintf("billing_config_poll: WARN parse failed (keeping current value): %v", err))
		return
	}

	SetBillingUnifiedEnabled(cfg.UnifiedBillingEnabled)
	if cfg.LocalLedgerAdvisory != nil {
		SetLocalLedgerAdvisory(*cfg.LocalLedgerAdvisory)
	}
}
