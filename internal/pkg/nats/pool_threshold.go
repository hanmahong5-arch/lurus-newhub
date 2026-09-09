package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// poolDedupTTL is the lifetime of the Redis SETNX dedup lock.
// Matches the quota_threshold pattern (24h) intent but applied at pool
// granularity with 1h TTL per ADR 2026-05-18 §9 Q5 — pool alerts re-fire
// per hour while balance remains below threshold, giving ops a visible
// heartbeat without spamming.
const poolDedupTTL = 1 * time.Hour

// poolSchemaDedupWindow is the minimum interval between two alert fires
// for the same pool, enforced via the tenant_credit_pools.alert_fired_at
// column. Matches poolDedupTTL — both layers must agree, since either can
// suppress publication on its own. The DB column is the authoritative
// record for ops dashboards (Redis is best-effort and can be down).
const poolSchemaDedupWindow = 1 * time.Hour

// PoolThresholdPayload is the wire payload published on SubjectPoolThreshold.
// Schema is denormalised on purpose: consumers (notification module, future
// Reseller dashboard) should not have to call back into newhub to render
// the alert. fired_at is the publisher-side wall clock; consumers use it
// for ordering not for billing.
type PoolThresholdPayload struct {
	TenantID       string    `json:"tenant_id"`
	PoolID         int64     `json:"pool_id"`
	CurrentBalance int64     `json:"current_balance"`
	MaxBalance     int64     `json:"max_balance"`
	ThresholdPct   int       `json:"threshold_pct"`
	FiredAt        time.Time `json:"fired_at"`
}

// poolRedisDeduper is the minimal Redis SETNX interface needed for pool
// threshold dedup. Mirrors the redisDeduper interface in
// internal/app/quota_threshold.go to keep mocking patterns uniform.
type poolRedisDeduper interface {
	SetNXBool(ctx context.Context, key string, expiration time.Duration) (bool, error)
}

// poolDB is the minimal DB surface the pool-threshold publisher needs:
// one read of alert_fired_at, one update to set it. Decoupled from
// *gorm.DB so the test layer can stub without spinning a real DB.
type poolDB interface {
	// LastAlertFiredAt returns the alert_fired_at timestamp for the given
	// pool, or nil if the column is NULL / row is missing. Returns
	// (nil, gorm.ErrRecordNotFound) when the pool row doesn't exist —
	// callers should fall back to "no prior alert" semantics.
	LastAlertFiredAt(ctx context.Context, poolID int64) (*time.Time, error)
	// MarkAlertFired atomically sets alert_fired_at = NOW() for the pool.
	MarkAlertFired(ctx context.Context, poolID int64, now time.Time) error
}

// poolPublisher is the minimal publish interface — matches thresholdPublisher
// in quota_threshold.go for test parity.
type poolPublisher interface {
	Publish(ctx context.Context, subject string, payload any) error
}

// gormPoolDB is the production implementation of poolDB, backed by repo.DB.
// Kept tiny on purpose: this package mustn't know about the wider repo API.
type gormPoolDB struct{ db *gorm.DB }

func (g *gormPoolDB) LastAlertFiredAt(ctx context.Context, poolID int64) (*time.Time, error) {
	var row struct {
		AlertFiredAt *time.Time `gorm:"column:alert_fired_at"`
	}
	err := g.db.WithContext(ctx).
		Table("tenant_credit_pools").
		Select("alert_fired_at").
		Where("id = ?", poolID).
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return nil, err
	}
	return row.AlertFiredAt, nil
}

func (g *gormPoolDB) MarkAlertFired(ctx context.Context, poolID int64, now time.Time) error {
	return g.db.WithContext(ctx).
		Table("tenant_credit_pools").
		Where("id = ?", poolID).
		Update("alert_fired_at", now).Error
}

// redisCommonDeduper wraps the common.RDB singleton to implement
// poolRedisDeduper. Returns nil when Redis is disabled so callers can
// treat dedup as "always pass" — schema-level dedup is the authoritative
// fallback (see poolSchemaDedupWindow).
type redisCommonDeduper struct{}

func (redisCommonDeduper) SetNXBool(ctx context.Context, key string, expiration time.Duration) (bool, error) {
	if !common.RedisEnabled || common.RDB == nil {
		// No Redis available — let publication proceed. Schema-level
		// dedup (alert_fired_at) will catch duplicates.
		return true, nil
	}
	return common.RDB.SetNX(ctx, key, "1", expiration).Result()
}

// SchemaDedupWindow exposes poolSchemaDedupWindow — the minimum interval a
// caller must wait since a pool's alert_fired_at before treating it as
// eligible to fire again. quota.go's precheck (before calling
// PublishPoolThreshold on every debit that crosses the threshold) uses this
// so it does not have to duplicate the constant; PublishPoolThreshold's own
// schema-dedup step (below) remains the authoritative check either way.
func SchemaDedupWindow() time.Duration {
	return poolSchemaDedupWindow
}

// PublishPoolThreshold runs the pool-threshold dedup + delivery pass for the
// tenant's credit pool, subject to two independent dedup layers:
//
//  1. Schema dedup: tenant_credit_pools.alert_fired_at < poolSchemaDedupWindow
//     ago → suppressed. Authoritative for ops visibility.
//  2. Redis SETNX dedup: pool:threshold:<tenantID>:<poolID> with 1h TTL.
//     Best-effort; protects against the (rare) race where two pods both
//     pass the schema check before either updates alert_fired_at.
//
// Unlike the original design, LLM_QUOTA_NATS_ENABLED being false (or the
// global Publisher not being wired) no longer short-circuits before either
// dedup layer runs: alert_fired_at is marked by this function whenever the
// two dedup layers let the crossing through, NATS or not — only the wire
// delivery itself (Step 4 in publishPoolThreshold) is skipped when there is
// no publisher. Returns fired=true and delivery="recorded_only" for that
// case; "nats" when the payload actually reached the wire.
//
// Two things this function does NOT do unconditionally, despite "fired"
// being true: this package does not write the billing.pool_threshold audit
// row — that is the caller's job (internal/app/quota.go
// maybeAlertPoolThreshold) — and metrics.CreditPoolAlertTotal is only
// incremented for the "recorded_only" (no publisher) and "nats" (publish
// succeeded) outcomes; a crossing where Publish is attempted and fails is
// reported to the caller as fired=true, delivery="recorded_only", err!=nil
// and does NOT increment this counter (see the "nats" vs error-counter note
// at Step 4 below). Also note: the caller has its own precheck
// (maybeAlertPoolThreshold, gated on the same poolSchemaDedupWindow via
// SchemaDedupWindow()) that can skip calling this function entirely while a
// pool's alert_fired_at is inside the window — "every crossing that dedup
// let through" therefore means dedup at both layers, caller precheck
// included, not just the two checks inside this function.
//
// Returns an error only for genuine infrastructure failures the caller can
// usefully report (DB unreachable, publish enqueue failed). Suppressions
// (either dedup layer, or the tenantID/poolID guard below) return
// fired=false, delivery="", err=nil — a no-op by design.
func PublishPoolThreshold(ctx context.Context, tenantID string, poolID int64, currentBalance, maxBalance int64, thresholdPct int) (fired bool, delivery string, err error) {
	if tenantID == "" || poolID <= 0 {
		return false, "", nil
	}

	// pub stays a nil poolPublisher interface (not a typed-nil *Publisher)
	// when NATS is disabled or the global Publisher hasn't been set — Step 4
	// below checks pub != nil, which only works correctly against a truly
	// nil interface value.
	var pub poolPublisher
	if Enabled() {
		if p := Get(); p != nil {
			pub = p
		}
	}

	return publishPoolThreshold(
		ctx,
		tenantID, poolID, currentBalance, maxBalance, thresholdPct,
		pub,
		redisCommonDeduper{},
		&gormPoolDB{db: repo.DB},
		time.Now().UTC(),
	)
}

// publishPoolThreshold is the testable core. Dependencies are injected so
// the test suite can substitute mocks without touching real Redis / NATS /
// gorm. now is also injected to keep behaviour deterministic across runs.
// pub may be nil (NATS disabled / no global Publisher) — Step 4 is skipped
// in that case, everything else runs unchanged.
//
// Order of operations is load-bearing (2026-05-19 Phase 2 self-audit
// reordered Steps 3/4 — see below):
//
//  1. Schema dedup check — cheapest path to no-op, also the durable record
//  2. Redis SETNX — short-window race guard between pods
//  3. Schema mark fired — write FIRST so dedup state is durable before
//     anything reaches the wire (or is skipped). If this fails we never
//     mark fired and the caller sees an error; the next call re-attempts
//     cleanly. "fired" from here on means "dedup let it through and the
//     schema mark landed", independent of whether the wire delivery below
//     also succeeds. When pub == nil, metrics.CreditPoolAlertTotal{delivery=
//     "recorded_only"} is incremented right here, since there is no further
//     step.
//  4. NATS publish — only when pub != nil, and only after the dedup state is
//     committed. metrics.CreditPoolAlertTotal{delivery="nats"} is
//     incremented only once Publish returns without error — a crossing that
//     was marked/audited but failed to reach the wire is never counted as
//     "nats" delivered. If publish fails, schema dedup has already locked
//     the slot for the window: ops sees an error (and the caller's
//     CreditPoolAlertHookErrorTotal is the honest delivery-failure signal)
//     and can replay manually, but we won't double-fire within the window.
//     When pub == nil this step is skipped entirely (delivery stays
//     "recorded_only") — NATS being off must not make the crossing
//     invisible.
//
// Errors from steps 1, 3, 4 propagate to the caller. Errors from step 2
// (Redis transient failure) are NOT fatal: the schema check has already
// said "ok to fire", so we proceed and rely on schema dedup for safety.
//
// Trade-off acknowledged: a publish failure now produces a "marked but
// not delivered" state for the dedup window. We prefer this to the
// alternative (publish succeeded, mark failed, Redis TTL expires, alert
// storm), which is the failure mode the audit closed.
func publishPoolThreshold(
	ctx context.Context,
	tenantID string,
	poolID int64,
	currentBalance, maxBalance int64,
	thresholdPct int,
	pub poolPublisher,
	rdb poolRedisDeduper,
	db poolDB,
	now time.Time,
) (fired bool, delivery string, err error) {
	// Step 1: schema-level dedup. Pool row missing → fall through (no
	// prior alert recorded, treat as ok to fire). Other DB errors abort.
	prev, lerr := db.LastAlertFiredAt(ctx, poolID)
	switch {
	case lerr == nil:
		if prev != nil && now.Sub(*prev) < poolSchemaDedupWindow {
			slog.Debug("pool threshold suppressed by schema dedup",
				"tenant_id", tenantID, "pool_id", poolID,
				"last_fired_at", prev, "window", poolSchemaDedupWindow)
			return false, "", nil
		}
	case errors.Is(lerr, gorm.ErrRecordNotFound):
		// No row — treat as never-fired. Continue.
	default:
		return false, "", fmt.Errorf("pool threshold dedup read: %w", lerr)
	}

	// Step 2: Redis SETNX race guard. Transient Redis failure does not
	// block publication — schema dedup is the safety net.
	if rdb != nil {
		key := poolDedupKey(tenantID, poolID)
		acquired, rerr := rdb.SetNXBool(ctx, key, poolDedupTTL)
		if rerr != nil {
			slog.Warn("pool threshold redis dedup failed; falling through to schema dedup",
				"tenant_id", tenantID, "pool_id", poolID, "key", key, "err", rerr)
		} else if !acquired {
			slog.Debug("pool threshold suppressed by redis dedup",
				"tenant_id", tenantID, "pool_id", poolID, "key", key)
			return false, "", nil
		}
	}

	// Step 3: durable schema mark FIRST (fail-closed). If this fails nothing
	// downstream (metric, publish) runs — caller sees the error and the next
	// call re-attempts. Trades a rare false-suppress (mark succeeded, publish
	// later failed inside the dedup window) for the more severe alert-storm
	// scenario the audit closed.
	if merr := db.MarkAlertFired(ctx, poolID, now); merr != nil {
		return false, "", fmt.Errorf("pool threshold mark fired: %w", merr)
	}

	fired = true

	if pub == nil {
		// NATS disabled or no global Publisher — the crossing is still
		// marked/audited (by the caller, via fired+delivery); only the wire
		// leg is skipped. Counted here since this is the only step left.
		delivery = "recorded_only"
		metrics.CreditPoolAlertTotal.WithLabelValues(tenantID, delivery).Inc()
		return fired, delivery, nil
	}

	// Step 4: publish to NATS. Use the canonical envelope shape used by
	// other LLM events for downstream notification consumers. If publish
	// fails, schema dedup has already locked the slot — caller may want to
	// replay manually, but we won't double-fire within the window. The
	// crossing is reported as "recorded_only" (that is what actually
	// happened — mark+audit landed, the wire leg did not) and is NOT
	// counted under credit_pool_alert_total{delivery="nats"}; the caller's
	// CreditPoolAlertHookErrorTotal is the honest signal for this case.
	payload := PoolThresholdPayload{
		TenantID:       tenantID,
		PoolID:         poolID,
		CurrentBalance: currentBalance,
		MaxBalance:     maxBalance,
		ThresholdPct:   thresholdPct,
		FiredAt:        now,
	}
	if perr := pub.Publish(ctx, SubjectPoolThreshold, payload); perr != nil {
		return fired, "recorded_only", fmt.Errorf("pool threshold publish: %w", perr)
	}

	delivery = "nats"
	metrics.CreditPoolAlertTotal.WithLabelValues(tenantID, delivery).Inc()

	slog.Info("pool threshold event published",
		"event_id", uuid.NewString(),
		"tenant_id", tenantID,
		"pool_id", poolID,
		"current_balance", currentBalance,
		"max_balance", maxBalance,
		"threshold_pct", thresholdPct)

	return fired, delivery, nil
}

// poolDedupKey returns the canonical Redis key for pool-threshold dedup.
// Format: pool:threshold:<tenantID>:<poolID>.
func poolDedupKey(tenantID string, poolID int64) string {
	return fmt.Sprintf("pool:threshold:%s:%d", tenantID, poolID)
}
