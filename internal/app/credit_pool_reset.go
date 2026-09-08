// Package app — scheduled tenant credit-pool reset.
//
// ADR 2026-05-18 (tenant-credit-pool) §3.1 gives every pool a reset_period
// (daily/weekly/monthly/none) and a next_reset_at column, but nothing ever
// read them: the schema existed, and no caller ran a reset pass against it.
// repo.ResetDuePools — the CAS-safe query this file's ResetDuePools drives —
// is new, added alongside this file, not a pre-existing dead path. A pool
// configured with a ceiling and a reset schedule just drained to zero and
// stayed there — the schedule was decorative.
//
// This file is the operator-facing wiring for that schema:
//
//   - ResetDuePools runs one pass at CREDIT_POOL_RESET_MODE (env, default
//     "observe"). Wired into the existing stranded-topup reconcile ticker's
//     leader branch (credit_pool_reconcile.go) via the resetDuePoolsSeam var,
//     so it rides the same 300s interval with no cmd/server change.
//   - ResetDuePoolsWithMode is the rehearsal entry point for
//     POST /internal/admin/reset-due-pools?mode=observe|enforce — the query
//     param overrides the environment for that one call, so an operator can
//     preview or force a pass without waiting for the next tick.
//
// enforce mode is the only mode that writes anything; see
// repo.ResetDuePools for the refill-to-ceiling / overdraft-forgiveness
// semantics and doc/runbook/pool-threshold-alert.md for the operational
// decision that semantic requires before it is turned on in production.
package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// CreditPoolResetModeObserve/Enforce are the accepted values for
// CREDIT_POOL_RESET_MODE and the rehearsal endpoint's ?mode= query param.
// For CREDIT_POOL_RESET_MODE, anything else (including unset/empty) is
// treated as observe — a scheduled reset must be explicitly turned on
// before it writes a single row. For the rehearsal endpoint's ?mode= query
// param specifically, an omitted mode falls back to the environment
// default the same way, but a mode that IS supplied and is neither of
// these two literal strings is rejected with 400 by the handler
// (internal/adapter/handler/internal_maintenance.go InternalResetDuePools)
// before it ever reaches this package — it does NOT fall through to
// observe.
const (
	CreditPoolResetModeObserve = "observe"
	CreditPoolResetModeEnforce = "enforce"
)

// creditPoolResetModeEnv reads CREDIT_POOL_RESET_MODE fresh on every call (no
// caching) so a config change takes effect on the next tick without a
// restart-timed race.
func creditPoolResetModeEnv() string {
	if os.Getenv("CREDIT_POOL_RESET_MODE") == CreditPoolResetModeEnforce {
		return CreditPoolResetModeEnforce
	}
	return CreditPoolResetModeObserve
}

// normalizeCreditPoolResetMode maps anything other than the literal enforce
// string to observe. It is a defensive fallback, not the endpoint's actual
// input validation: the HTTP handler (InternalResetDuePools) already
// rejects an unrecognised ?mode= value with 400 before calling
// ResetDuePoolsWithMode, so in practice this function only ever sees "",
// "observe", or "enforce" from that path. A direct caller that skips the
// handler (e.g. a test, or ResetDuePools itself via creditPoolResetModeEnv)
// can still pass an arbitrary string, and for that case only — not for the
// HTTP endpoint's typo case — the fail-closed-to-observe behaviour applies.
func normalizeCreditPoolResetMode(mode string) string {
	if mode == CreditPoolResetModeEnforce {
		return CreditPoolResetModeEnforce
	}
	return CreditPoolResetModeObserve
}

// ResetDuePools runs one scheduled credit-pool reset pass using
// CREDIT_POOL_RESET_MODE from the environment. This is the ticker entry
// point (see resetDuePoolsSeam in credit_pool_reconcile.go); the rehearsal
// endpoint uses ResetDuePoolsWithMode instead so a query param can override
// the environment for a single call.
func ResetDuePools(ctx context.Context) ([]repo.PoolResetResult, error) {
	return resetDuePoolsWithMode(ctx, creditPoolResetModeEnv())
}

// CreditPoolResetModeFromEnv exposes the current CREDIT_POOL_RESET_MODE
// resolution (observe unless the env var is exactly "enforce") so a caller
// that needs to REPORT the mode a query-param-less ResetDuePools call will
// run under — e.g. the rehearsal handler's response/log line — does not have
// to duplicate the parsing rule.
func CreditPoolResetModeFromEnv() string {
	return creditPoolResetModeEnv()
}

// ResetDuePoolsWithMode runs one pass at the given mode (normalized to
// observe unless it is exactly "enforce"), regardless of the environment.
// Used by POST /internal/admin/reset-due-pools so an operator can rehearse
// (or force) a pass on demand.
func ResetDuePoolsWithMode(ctx context.Context, mode string) ([]repo.PoolResetResult, error) {
	return resetDuePoolsWithMode(ctx, normalizeCreditPoolResetMode(mode))
}

func resetDuePoolsWithMode(ctx context.Context, mode string) ([]repo.PoolResetResult, error) {
	enforce := mode == CreditPoolResetModeEnforce
	now := time.Now().UTC()

	results, err := repo.ResetDuePools(ctx, now, enforce)
	for _, r := range results {
		metrics.CreditPoolResetTotal.WithLabelValues(r.TenantID, r.Action).Inc()
		if !enforce || r.Action != repo.PoolResetActionReset {
			continue
		}
		metrics.CreditPoolBalance.WithLabelValues(r.TenantID).Set(float64(r.MaxBalance))

		nextResetAt := ""
		if r.NextResetAt != nil {
			nextResetAt = r.NextResetAt.UTC().Format(time.RFC3339)
		}
		governance.RecordAuditEvent(governance.NewDetachedAuditEvent(
			r.TenantID, governance.ActorSystem, 0,
			governance.ActionBillingPoolReset, governance.ResourceTenant, int(r.PoolID),
			fmt.Sprintf(`{"pool_id":%d,"delta":%d,"next_reset_at":%q}`, r.PoolID, r.Delta, nextResetAt)))
	}
	if err != nil {
		return results, fmt.Errorf("reset due pools: %w", err)
	}
	return results, nil
}
