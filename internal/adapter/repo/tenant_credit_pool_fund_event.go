package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// ErrFundEventExists is returned by FundPoolIdempotent when the event_id was
// already processed. The caller should treat this as a successful replay and
// return the previously-recorded new_balance to the caller without re-crediting.
var ErrFundEventExists = errors.New("fund event already processed (idempotent replay)")

// PoolFundEventIDMaxLen is the width of credit_pool_fund_events.event_id:
// VARCHAR(128) in migrations/019_create_credit_pool_fund_events.sql:16,
// migrations/021_pg_baseline_gaps.sql:53 and the GORM tag on
// entity.CreditPoolFundEvent.EventID.
//
// Exported because a caller has to be able to refuse an over-wide key BEFORE
// it moves money. This column is written inside the same transaction as the
// pool credit, and on the admin topup path that transaction opens only after
// the platform wallet has already been debited — so on PostgreSQL an
// over-long value is not a validation error, it is a 22001 that aborts a
// transaction behind a debit that already happened.
const PoolFundEventIDMaxLen = 128

// ValidateFundEventID rejects the three event_id values a PostgreSQL text
// column cannot store: wider than the column, containing a NUL byte, or not
// valid UTF-8. Each of them fails at INSERT time rather than at call time,
// which on a wallet-backed path means failing after the money moved.
//
// BLIND SPOT: this is a Go-side guard derived from the column declaration. It
// is not PostgreSQL's own check and cannot notice a future migration that
// narrows or widens the column — the constant above and the migration have to
// move together. The hermetic sqlite tier does not enforce varchar width at
// all, so only this function stands between an over-wide key and a 22001 in
// production.
func ValidateFundEventID(eventID string) error {
	if eventID == "" {
		return fmt.Errorf("event_id is required for idempotent fund: provide the BillingOutbox event ID")
	}
	if n := utf8.RuneCountInString(eventID); n > PoolFundEventIDMaxLen {
		return fmt.Errorf("event_id is %d characters; credit_pool_fund_events.event_id holds at most %d", n, PoolFundEventIDMaxLen)
	}
	if strings.ContainsRune(eventID, 0) {
		return fmt.Errorf("event_id contains a NUL byte, which PostgreSQL text columns reject outright")
	}
	if !utf8.ValidString(eventID) {
		return fmt.Errorf("event_id is not valid UTF-8; PostgreSQL rejects it as an invalid byte sequence for encoding UTF8")
	}
	return nil
}

// LookupFundEvent reads the credit_pool_fund_events row occupying
// (tenantID, eventID) — the slot the UNIQUE index from migration 031 makes
// exclusive — and reports whether one exists.
//
// It answers "what already holds this key", NOT "is this a replay": the table
// is shared by the platform BillingOutbox funding path, the stranded-debit
// state machine and the admin topup endpoint, and the caller-chosen keys of
// the last two live in the same namespace as the first. Deciding what a found
// row MEANS is the caller's job (see classifyPoolTopupKey in
// internal/adapter/handler/tenant_credit_pool.go).
//
// A missing row is (nil, false, nil) — not an error. A read failure is an
// error, and a caller about to move money must treat it as "unknown", never
// as "fresh".
func LookupFundEvent(ctx context.Context, tenantID, eventID string) (*CreditPoolFundEvent, bool, error) {
	if tenantID == "" || eventID == "" {
		return nil, false, nil
	}
	var evt CreditPoolFundEvent
	if err := DB.WithContext(ctx).
		Where("tenant_id = ? AND event_id = ?", tenantID, eventID).
		First(&evt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lookup fund event: %w", err)
	}
	return &evt, true, nil
}

// RecordTerminalFundEvent claims (tenantID, eventID) with a row that records
// an outcome rather than a credit: NewBalance is 0 because nothing was
// credited. Its purpose is to make the key unusable again.
//
// Idempotent via the same UNIQUE(tenant_id, event_id): if the slot is already
// taken, the outcome it needed to record is already recorded (or the slot was
// never free), so a unique violation is success, not failure. Any other error
// is returned — the caller must not assume the key was claimed.
func RecordTerminalFundEvent(ctx context.Context, poolID int64, tenantID, eventID string, amount int64, source string) error {
	if err := ValidateFundEventID(eventID); err != nil {
		return err
	}
	evt := &CreditPoolFundEvent{
		EventID:    eventID,
		TenantID:   tenantID,
		PoolID:     poolID,
		Amount:     amount,
		NewBalance: 0,
		Source:     source,
		CreatedAt:  time.Now(),
	}
	if err := DB.WithContext(ctx).Create(evt).Error; err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return fmt.Errorf("record terminal fund event: %w", err)
	}
	return nil
}

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
//
// The credit draw is written with the standard PoolDrawReasonTopup reason.
// Callers that carry an operator-supplied reason (the admin topup endpoint)
// use FundPoolIdempotentWithReason instead.
func FundPoolIdempotent(
	ctx context.Context,
	poolID int64, tenantID string, amount int64,
	eventID string, source string, actorUserID int,
) (*CreditPoolFundEvent, error) {
	return FundPoolIdempotentWithReason(ctx, poolID, tenantID, amount, eventID, source, actorUserID, PoolDrawReasonTopup)
}

// FundPoolIdempotentWithReason is FundPoolIdempotent with a caller-chosen draw
// reason — everything else (the (tenant_id, event_id) idempotency, the fast
// path, the in-transaction conflict arm, ErrFundEventExists) is identical,
// because it IS the same function.
//
// Split out for the admin topup endpoint (POST /api/v2/admin/tenants/:id/
// credit-pool/topup), whose body carries a free-text `reason` that used to
// reach the draw ledger through TopupPool. Routing that endpoint through the
// idempotent primitive must not silently drop the reason from the ledger, so
// it travels as a parameter instead.
//
// drawReason == "" falls back to PoolDrawReasonTopup. The value is truncated
// to poolDrawReasonMaxLen runes (the varchar(32) column width) for the same
// reason CreditPoolAdjustment truncates: PostgreSQL rejects an over-long value
// outright (22001), which on this path would abort the transaction AFTER the
// wallet was debited — a caller-chosen string long enough to turn a paid topup
// into a stranded debit.
func FundPoolIdempotentWithReason(
	ctx context.Context,
	poolID int64, tenantID string, amount int64,
	eventID string, source string, actorUserID int,
	drawReason string,
) (*CreditPoolFundEvent, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("fund amount must be positive, got %d: pass a value > 0", amount)
	}
	if err := ValidateFundEventID(eventID); err != nil {
		return nil, err
	}

	// Fast-path: check if already processed before entering the transaction.
	// The unique-constraint is the authoritative guard; this pre-check merely
	// avoids the TopupPool write on obvious replays. Scoped by (tenant_id,
	// event_id) — a different tenant reusing this event_id is NOT a replay.
	var existing CreditPoolFundEvent
	if err := DB.WithContext(ctx).Where("tenant_id = ? AND event_id = ?", tenantID, eventID).First(&existing).Error; err == nil {
		return &existing, ErrFundEventExists
	}

	if drawReason == "" {
		drawReason = PoolDrawReasonTopup
	}
	if runes := []rune(drawReason); len(runes) > poolDrawReasonMaxLen {
		drawReason = string(runes[:poolDrawReasonMaxLen])
	}

	var funded *CreditPoolFundEvent
	txErr := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Credit the pool balance (enforces ceiling via ErrPoolWouldExceedCeiling).
		newBalance, err := topupPoolInTx(tx, poolID, tenantID, amount, actorUserID, drawReason)
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
			// Unique-constraint violation from a concurrent replay. The
			// failed INSERT leaves the PG transaction ABORTED (any further
			// statement fails with SQLSTATE 25P02), so the winner's row
			// cannot be fetched here — signal replay via the sentinel and
			// let the re-fetch after rollback (below, outside the
			// transaction) return it.
			if isUniqueViolation(insertErr) {
				return ErrFundEventExists
			}
			return fmt.Errorf("fund event insert: %w", insertErr)
		}

		funded = event
		return nil
	})

	if txErr != nil {
		if errors.Is(txErr, ErrFundEventExists) {
			// The conflict was detected inside the now rolled-back
			// transaction, where the winner's row could NOT be read (the
			// failed INSERT had already aborted it — SQLSTATE 25P02 on any
			// further statement). Fetch it here, on a fresh implicit
			// transaction; the 23505 implies the winner committed, so this
			// read can only miss in the migration-031 residue state (legacy
			// GLOBAL unique still pinned by an external dependency), which
			// 031 already RAISEs a WARNING about.
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
