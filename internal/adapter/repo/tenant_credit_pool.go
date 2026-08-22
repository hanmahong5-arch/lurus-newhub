package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// Re-export entity types so handler / app layers can use repo.* without
// importing the entity package directly (matches Tenant / Token convention).
type TenantCreditPool = entity.TenantCreditPool
type TenantCreditPoolDraw = entity.TenantCreditPoolDraw
type CreditPoolFundEvent = entity.CreditPoolFundEvent

// Re-export pool-related constants from entity.
const (
	PoolResetNone    = entity.PoolResetNone
	PoolResetDaily   = entity.PoolResetDaily
	PoolResetWeekly  = entity.PoolResetWeekly
	PoolResetMonthly = entity.PoolResetMonthly

	PoolMaxBalanceUnlimited = entity.PoolMaxBalanceUnlimited

	PoolDrawReasonRelayDebit = entity.PoolDrawReasonRelayDebit
	PoolDrawReasonTopup      = entity.PoolDrawReasonTopup
	PoolDrawReasonReset      = entity.PoolDrawReasonReset
	PoolDrawReasonAdjustment = entity.PoolDrawReasonAdjustment
	PoolDrawReasonOverdraft  = entity.PoolDrawReasonOverdraft
)

// Direction constants (int16, separate const block to preserve type).
const (
	PoolDrawDirectionDebit  = entity.PoolDrawDirectionDebit
	PoolDrawDirectionCredit = entity.PoolDrawDirectionCredit
)

// ErrPoolExhausted is returned by DebitPool when the conditional UPDATE
// affected zero rows — current_balance < requested amount. The relay
// enforcement layer maps this to HTTP 402 (ADR §5 precedence step 4).
var ErrPoolExhausted = errors.New("tenant credit pool exhausted")

// ErrPoolNotFound is returned by GetTenantCreditPool when the tenant has no
// pool row. Callers MUST treat this as "unlimited" — it is not an error
// condition (ADR §5 edge case: no pool row = pool gate skipped).
var ErrPoolNotFound = errors.New("tenant credit pool not found")

// ErrPoolWouldExceedCeiling is returned by TopupPool when the requested
// topup amount would push current_balance past max_balance.
var ErrPoolWouldExceedCeiling = errors.New("topup would exceed pool max_balance ceiling")

// GetTenantCreditPool fetches the pool row for a tenant. Returns
// ErrPoolNotFound when no row exists — callers MUST interpret that as
// "unlimited" and bypass the pool gate (not as an error).
func GetTenantCreditPool(tenantID string) (*TenantCreditPool, error) {
	var pool TenantCreditPool
	err := DB.Where("tenant_id = ?", tenantID).First(&pool).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPoolNotFound
		}
		return nil, fmt.Errorf("get tenant credit pool: %w", err)
	}
	return &pool, nil
}

// CreateTenantCreditPool inserts a fresh pool row for a tenant. Used by the
// Reseller-facing POST /api/v2/admin/tenants/:id/credit-pool endpoint.
// Defaults: resetPeriod = "monthly" (ADR §9 Q1), alertThresholdPct = 80,
// initial CurrentBalance = 0 (Resellers topup separately).
func CreateTenantCreditPool(tenantID string, createdByUserID int, maxBalance int64, resetPeriod string, alertThresholdPct int) (*TenantCreditPool, error) {
	if resetPeriod == "" {
		resetPeriod = PoolResetMonthly
	}
	if alertThresholdPct <= 0 || alertThresholdPct > 100 {
		alertThresholdPct = 80
	}

	now := time.Now()
	pool := &TenantCreditPool{
		TenantID:          tenantID,
		CreatedByUserID:   createdByUserID,
		CurrentBalance:    0,
		MaxBalance:        maxBalance,
		ResetPeriod:       resetPeriod,
		LastResetAt:       now,
		NextResetAt:       nextResetAt(resetPeriod, now),
		AlertThresholdPct: alertThresholdPct,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := DB.Create(pool).Error; err != nil {
		return nil, fmt.Errorf("create tenant credit pool: %w", err)
	}
	return pool, nil
}

// DebitPool atomically deducts amount from the tenant pool and records a
// draw ledger row in the same transaction. Returns ErrPoolExhausted when
// the pool has insufficient balance.
//
// Atomicity comes from the conditional UPDATE (ADR §7 risk #1):
//
//	UPDATE tenant_credit_pools
//	   SET current_balance = current_balance - ?, updated_at = NOW()
//	 WHERE id = ? AND current_balance >= ?
//
// PostgreSQL READ COMMITTED is sufficient — the implicit row-level lock
// during UPDATE serializes concurrent debits. SERIALIZABLE not needed.
//
// Use DebitPoolInTx when the caller already owns a transaction. Note the
// post-consume quota path does NOT share a transaction with quota_consume —
// the user quota write may go through Redis/batch paths that have no DB tx.
// When this returns ErrPoolExhausted the post-consume caller falls back to
// OverdraftDebitPool so the debit is recorded as debt instead of dropped.
func DebitPool(poolID int64, tenantID string, amount int64, tokenID int, logID int64) error {
	if amount <= 0 {
		return fmt.Errorf("debit amount must be positive, got %d", amount)
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		return DebitPoolInTx(tx, poolID, tenantID, amount, tokenID, logID)
	})
}

// DebitPoolInTx is the transactional variant of DebitPool — it does the same
// conditional-UPDATE-plus-draw-insert but reuses the caller's transaction so
// the balance update and the draw ledger row commit or roll back together.
// It does NOT join the user quota_consume write into the same transaction
// (that write may be Redis-buffered or batched and has no DB tx to share).
//
// Callers MUST handle ErrPoolExhausted explicitly — there is no auto-retry.
// The post-consume path handles it via OverdraftDebitPool.
func DebitPoolInTx(tx *gorm.DB, poolID int64, tenantID string, amount int64, tokenID int, logID int64) error {
	if amount <= 0 {
		return fmt.Errorf("debit amount must be positive, got %d", amount)
	}

	result := tx.Model(&TenantCreditPool{}).
		Where("id = ? AND current_balance >= ?", poolID, amount).
		Updates(map[string]interface{}{
			"current_balance": gorm.Expr("current_balance - ?", amount),
			"updated_at":      time.Now(),
		})
	if result.Error != nil {
		return fmt.Errorf("debit pool update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrPoolExhausted
	}

	draw := &TenantCreditPoolDraw{
		PoolID:    poolID,
		TenantID:  tenantID,
		TokenID:   tokenID,
		LogID:     logID,
		Direction: PoolDrawDirectionDebit,
		Amount:    amount,
		Reason:    PoolDrawReasonRelayDebit,
		CreatedAt: time.Now(),
	}
	if err := tx.Create(draw).Error; err != nil {
		return fmt.Errorf("debit pool draw insert: %w", err)
	}
	return nil
}

// OverdraftDebitPool unconditionally deducts amount from the pool — the
// balance is allowed to go negative — and records a `relay_overdraft` draw
// row in the same transaction. Returns the post-debit balance.
//
// This is the P0-3 fix for the post-consume path: when DebitPool returns
// ErrPoolExhausted the upstream tokens are already burned and the user quota
// already charged, so dropping the pool debit (the old log-only behaviour)
// silently breaks `seed − Σdraws == balance`. Recording the debt keeps the
// conservation law unconditional: the negative balance keeps the relay gate
// closed (IsExhausted ⇒ balance <= 0) and the next topup repays it — no
// reconciliation job needed.
func OverdraftDebitPool(poolID int64, tenantID string, amount int64, tokenID int, logID int64) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("overdraft debit amount must be positive, got %d", amount)
	}

	var newBalance int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&TenantCreditPool{}).
			Where("id = ?", poolID).
			Updates(map[string]interface{}{
				"current_balance": gorm.Expr("current_balance - ?", amount),
				"updated_at":      time.Now(),
			})
		if result.Error != nil {
			return fmt.Errorf("overdraft debit pool update: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("overdraft debit: pool %d not found", poolID)
		}

		var pool TenantCreditPool
		if err := tx.Select("current_balance").Where("id = ?", poolID).First(&pool).Error; err != nil {
			return fmt.Errorf("overdraft debit readback: %w", err)
		}
		newBalance = pool.CurrentBalance

		draw := &TenantCreditPoolDraw{
			PoolID:    poolID,
			TenantID:  tenantID,
			TokenID:   tokenID,
			LogID:     logID,
			Direction: PoolDrawDirectionDebit,
			Amount:    amount,
			Reason:    PoolDrawReasonOverdraft,
			CreatedAt: time.Now(),
		}
		if err := tx.Create(draw).Error; err != nil {
			return fmt.Errorf("overdraft debit draw insert: %w", err)
		}
		return nil
	})
	return newBalance, err
}

// TopupPool atomically increments the pool balance and writes a credit draw
// row. Returns the new balance for the response payload.
//
// ADR §9 Q4 (Accepted): topup MUST be funded by the Reseller's platform
// wallet. The WalletDebit call lives in the handler, not the repo, so the
// BillingOutbox pattern can wrap both wallet debit and this pool increment
// in a single fail-safe flow. No admin-grant path exists by design.
//
// Returns ErrPoolWouldExceedCeiling when amount + current_balance > max_balance
// (unlimited pools always succeed).
//
// TODO(phase-2 wiring): handler at POST /api/v2/admin/tenants/:id/credit-pool/topup
// MUST call DebitWalletGRPC first, then this function, with outbox-managed
// rollback on partial failure.
func TopupPool(poolID int64, tenantID string, amount int64, actorUserID int, reason string) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("topup amount must be positive, got %d", amount)
	}
	if reason == "" {
		reason = PoolDrawReasonTopup
	}

	var newBalance int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&TenantCreditPool{}).
			Where(
				"id = ? AND (max_balance = ? OR current_balance + ? <= max_balance)",
				poolID, PoolMaxBalanceUnlimited, amount,
			).
			Updates(map[string]interface{}{
				"current_balance": gorm.Expr("current_balance + ?", amount),
				"updated_at":      time.Now(),
			})
		if result.Error != nil {
			return fmt.Errorf("topup pool update: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrPoolWouldExceedCeiling
		}

		var pool TenantCreditPool
		if err := tx.Select("current_balance").Where("id = ?", poolID).First(&pool).Error; err != nil {
			return fmt.Errorf("topup pool readback: %w", err)
		}
		newBalance = pool.CurrentBalance

		draw := &TenantCreditPoolDraw{
			PoolID:      poolID,
			TenantID:    tenantID,
			Direction:   PoolDrawDirectionCredit,
			Amount:      amount,
			Reason:      reason,
			ActorUserID: actorUserID,
			CreatedAt:   time.Now(),
		}
		if err := tx.Create(draw).Error; err != nil {
			return fmt.Errorf("topup pool draw insert: %w", err)
		}
		return nil
	})
	return newBalance, err
}

// ListPoolDraws returns paginated audit-ledger rows for a pool, ordered by
// created_at DESC. Used by GET /api/v2/admin/tenants/:id/credit-pool/usage.
// Limit is clamped to [1, 200] to bound query cost.
func ListPoolDraws(poolID int64, offset int, limit int) ([]*TenantCreditPoolDraw, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var draws []*TenantCreditPoolDraw
	var total int64

	if err := DB.Model(&TenantCreditPoolDraw{}).
		Where("pool_id = ?", poolID).
		Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count pool draws: %w", err)
	}

	if err := DB.Where("pool_id = ?", poolID).
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&draws).Error; err != nil {
		return nil, 0, fmt.Errorf("list pool draws: %w", err)
	}

	return draws, total, nil
}

// ErrFundEventExists is returned by FundPoolIdempotent when the event_id was
// already processed. The caller should treat this as a successful replay and
// return the previously-recorded new_balance to the caller without re-crediting.
var ErrFundEventExists = errors.New("fund event already processed (idempotent replay)")

// FundPoolIdempotent atomically credits a tenant pool funded by an external
// platform BillingOutbox event. Idempotency is enforced via a UNIQUE constraint
// on credit_pool_fund_events(tenant_id, event_id) — PER TENANT, not global
// (migration 031): a replay is "same tenant, same event_id"; a different
// tenant funding under the same event_id is an independent credit, not a replay.
//
//   - If (tenantID, event_id) was already processed, the existing
//     CreditPoolFundEvent is returned alongside ErrFundEventExists — caller
//     returns 200 with that row's data, not an error to the caller of the endpoint.
//   - Otherwise: TopupPool is called inside a transaction, then a fund event row
//     is inserted. If the insert fails with a unique-constraint violation (race
//     replay), the function re-fetches and returns the existing row with
//     ErrFundEventExists so the caller still returns 200.
//
// The function does NOT call DebitWalletGRPC — the wallet debit happened on the
// platform side before the BillingOutbox event was emitted. This is a pure
// credit to the local pool.
//
// amount must be > 0. Returns (fundEvent, nil) on first write,
// (existingEvent, ErrFundEventExists) on replay.
func FundPoolIdempotent(
	ctx context.Context,
	poolID int64, tenantID string, amount int64,
	eventID string, source string, actorUserID int,
) (*CreditPoolFundEvent, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("fund amount must be positive, got %d: pass a value > 0", amount)
	}
	if eventID == "" {
		return nil, fmt.Errorf("event_id is required for idempotent fund: provide the BillingOutbox event ID")
	}

	// Fast-path: check if already processed before entering the transaction.
	// The unique-constraint is the authoritative guard; this pre-check merely
	// avoids the TopupPool write on obvious replays. Scoped by (tenant_id,
	// event_id) — a different tenant reusing this event_id is NOT a replay.
	var existing CreditPoolFundEvent
	if err := DB.WithContext(ctx).Where("tenant_id = ? AND event_id = ?", tenantID, eventID).First(&existing).Error; err == nil {
		return &existing, ErrFundEventExists
	}

	var funded *CreditPoolFundEvent
	txErr := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Credit the pool balance (enforces ceiling via ErrPoolWouldExceedCeiling).
		newBalance, err := topupPoolInTx(tx, poolID, tenantID, amount, actorUserID, PoolDrawReasonTopup)
		if err != nil {
			return err
		}

		now := time.Now()
		event := &CreditPoolFundEvent{
			EventID:    eventID,
			TenantID:   tenantID,
			PoolID:     poolID,
			Amount:     amount,
			NewBalance: newBalance,
			Source:     source,
			CreatedAt:  now,
		}
		if insertErr := tx.Create(event).Error; insertErr != nil {
			// Unique-constraint violation from a concurrent replay: fetch the
			// winner's row and surface ErrFundEventExists to the caller.
			if isUniqueViolation(insertErr) {
				var race CreditPoolFundEvent
				if fetchErr := tx.Where("tenant_id = ? AND event_id = ?", tenantID, eventID).First(&race).Error; fetchErr != nil {
					return fmt.Errorf("fund event insert conflict and re-fetch failed: %w", fetchErr)
				}
				funded = &race
				// Signal replay to the outer scope via a sentinel that we handle below.
				return ErrFundEventExists
			}
			return fmt.Errorf("fund event insert: %w", insertErr)
		}

		funded = event
		return nil
	})

	if txErr != nil {
		if errors.Is(txErr, ErrFundEventExists) {
			// Re-fetch outside the rolled-back TX so we return a clean row.
			if funded != nil {
				return funded, ErrFundEventExists
			}
			var race CreditPoolFundEvent
			if err := DB.WithContext(ctx).Where("tenant_id = ? AND event_id = ?", tenantID, eventID).First(&race).Error; err != nil {
				return nil, fmt.Errorf("fund event replay re-fetch: %w", err)
			}
			return &race, ErrFundEventExists
		}
		return nil, txErr
	}

	return funded, nil
}

// topupPoolInTx is the transactional core of TopupPool extracted so that
// FundPoolIdempotent can participate in the same DB.Transaction block.
// Returns the new balance after crediting.
func topupPoolInTx(tx *gorm.DB, poolID int64, tenantID string, amount int64, actorUserID int, reason string) (int64, error) {
	result := tx.Model(&TenantCreditPool{}).
		Where(
			"id = ? AND (max_balance = ? OR current_balance + ? <= max_balance)",
			poolID, PoolMaxBalanceUnlimited, amount,
		).
		Updates(map[string]interface{}{
			"current_balance": gorm.Expr("current_balance + ?", amount),
			"updated_at":      time.Now(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("fund pool balance update: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return 0, ErrPoolWouldExceedCeiling
	}

	var pool TenantCreditPool
	if err := tx.Select("current_balance").Where("id = ?", poolID).First(&pool).Error; err != nil {
		return 0, fmt.Errorf("fund pool readback: %w", err)
	}

	draw := &TenantCreditPoolDraw{
		PoolID:      poolID,
		TenantID:    tenantID,
		Direction:   PoolDrawDirectionCredit,
		Amount:      amount,
		Reason:      reason,
		ActorUserID: actorUserID,
		CreatedAt:   time.Now(),
	}
	if err := tx.Create(draw).Error; err != nil {
		return 0, fmt.Errorf("fund pool draw insert: %w", err)
	}

	return pool.CurrentBalance, nil
}

// isUniqueViolation reports whether a DB error is a UNIQUE constraint violation
// across both PostgreSQL (error code 23505) and SQLite ("UNIQUE constraint").
// Avoids importing the pq driver directly — string matching is sufficient for
// this internal guard.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// PostgreSQL driver wraps with "ERROR: duplicate key value violates unique constraint"
	// SQLite driver: "UNIQUE constraint failed: ..."
	return strings.Contains(s, "duplicate key value") ||
		strings.Contains(s, "UNIQUE constraint failed") ||
		strings.Contains(s, "23505")
}

// nextResetAt computes the next scheduled reset timestamp for a pool.
// Returns nil for "none" (no scheduled reset — manual topup only).
//
// Daily   → next 00:00 UTC
// Weekly  → next Monday 00:00 UTC
// Monthly → 1st of next month 00:00 UTC (matches OpenRouter limit_reset semantics)
func nextResetAt(period string, from time.Time) *time.Time {
	utc := from.UTC()
	var next time.Time
	switch period {
	case PoolResetDaily:
		next = time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 0, 0, 0, time.UTC)
	case PoolResetWeekly:
		daysUntilMonday := (8 - int(utc.Weekday())) % 7
		if daysUntilMonday == 0 {
			daysUntilMonday = 7
		}
		next = time.Date(utc.Year(), utc.Month(), utc.Day()+daysUntilMonday, 0, 0, 0, 0, time.UTC)
	case PoolResetMonthly:
		next = time.Date(utc.Year(), utc.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	case PoolResetNone:
		return nil
	default:
		return nil
	}
	return &next
}
