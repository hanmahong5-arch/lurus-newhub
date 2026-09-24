package common

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
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
//
// BOTH flags are pointers, and that is load-bearing rather than stylistic. A
// 200 whose body omits a key, renames it, or nests it under a new envelope
// decodes to the Go zero value; applying that zero value unconditionally
// turns the flag OFF on every replica inside one poll cycle — or seconds
// after boot, because StartBillingConfigPoller polls once before its first
// tick and can therefore override the manifest's env seed. With unified
// billing off the billing outbox stops draining entirely, so "the platform
// answered 200 with a body we did not recognise" used to be a silent,
// fleet-wide money-path switch.
//
// nil means "the platform did not state a value", which is NOT the same as
// "the platform said false". Only a stated value may change anything —
// deploy-order safe in both directions during a rollout.
type billingConfigResponse struct {
	UnifiedBillingEnabled *bool `json:"unified_billing_enabled"`
	LocalLedgerAdvisory   *bool `json:"local_ledger_advisory"`
}

// billingConfigEffective holds the summary line describing what the last
// completed poll actually applied, e.g.
// "unified=true(platform) advisory=false(kept)". Stored as a string so the
// value and its provenance travel together. atomic.Value because the poller
// goroutine writes it while any reader (an operator endpoint, a test) may
// read it.
var billingConfigEffective atomic.Value // string

// BillingConfigEffective reports what the running process currently believes
// the platform billing flags are AND where each value came from:
// "platform" (the last poll stated it) or "kept" (the last poll did not state
// it, so the previous value survived). Before any poll has completed it
// reports the env-seeded values with source "env".
//
// This exists because "an absent key keeps the current value" is otherwise
// only inferable from prose: on the wire and in the flag itself, "platform
// said false" and "platform never mentioned it" look identical.
//
// BLIND SPOT: it describes the LAST COMPLETED POLL only. A poll that failed
// (transport error, non-200, unparseable body) does not reach the store, so
// the summary keeps describing the last poll that did — correct for the flag
// values, silent about the fact that the platform has since stopped
// answering. That signal lives in the WARN lines fetchAndApplyBillingConfig
// writes on each failure, not here. It also says nothing about whether the
// flags were reseeded from env afterwards (RefreshIdentityClientEnv).
func BillingConfigEffective() string {
	if s, _ := billingConfigEffective.Load().(string); s != "" {
		return s
	}
	return fmt.Sprintf("unified=%v(env) advisory=%v(env)", BillingUnifiedEnabled(), LocalLedgerAdvisory())
}

// applyBillingConfigFlag applies one pointer-valued flag and returns the
// provenance tag for the summary line: "platform" when the body stated a
// value, "kept" when it did not.
func applyBillingConfigFlag(stated *bool, set func(bool)) string {
	if stated == nil {
		return "kept"
	}
	set(*stated)
	return "platform"
}

// StartBillingConfigPoller launches a background goroutine that polls the platform
// billing-config endpoint every 30 seconds and applies the flags the response
// actually STATES (a key the body does not mention keeps its current value —
// see billingConfigResponse; BillingConfigEffective reports which is which).
// It is a no-op (returns immediately without spawning) when
// IDENTITY_SERVICE_URL is empty — in that case the env-seeded default from init()
// remains in effect.
//
// On any error (network, non-200, parse failure) the goroutine logs a WARN and
// leaves the current flag value unchanged (fail-keep, never flip on error).
//
// The goroutine stops when ctx is cancelled. The returned channel is closed once
// the goroutine has actually exited (immediately in the no-op case), so a caller
// that swaps process-global state afterwards — a test replacing the slog writer,
// a shutdown path — can wait for it instead of racing a poll that is still
// logging. The server ignores the channel: its lifetime is the context's.
func StartBillingConfigPoller(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if IdentityServiceURL == "" {
		close(done)
		return done
	}

	go func() {
		defer close(done)
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
	return done
}

// fetchAndApplyBillingConfig performs one HTTP GET to the platform
// billing-config endpoint and applies the flags the body STATES. Any failure —
// and any key the body does not state — leaves the corresponding flag
// unchanged.
//
// Single-caller by design: in production only the poller goroutine calls this,
// which is why the load/compare/store of billingConfigEffective at the end is
// not one atomic operation. Two callers racing would at worst log the same
// summary twice; no flag value can be corrupted, since each Set* is itself
// atomic.
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

	unifiedSource := applyBillingConfigFlag(cfg.UnifiedBillingEnabled, SetBillingUnifiedEnabled)
	advisorySource := applyBillingConfigFlag(cfg.LocalLedgerAdvisory, SetLocalLedgerAdvisory)

	// Log the effective values ONLY when they change. The poller runs every
	// 30 seconds on every replica, so an unconditional line would bury the
	// one poll that mattered; a line that never appears leaves an operator
	// with no way to tell "the platform turned it off" from "the platform
	// stopped mentioning it".
	summary := fmt.Sprintf("unified=%v(%s) advisory=%v(%s)",
		BillingUnifiedEnabled(), unifiedSource, LocalLedgerAdvisory(), advisorySource)
	if prev, _ := billingConfigEffective.Load().(string); prev != summary {
		SysLog("billing_config_poll: effective " + summary)
	}
	billingConfigEffective.Store(summary)
}
