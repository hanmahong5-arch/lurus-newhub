package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"gorm.io/gorm"
)

// Re-export so handler / app layers can use repo.* without importing entity
// (matches TenantCreditPool / CreditPoolFundEvent convention).
type ProvisionedRedemptionBatch = entity.ProvisionedRedemptionBatch

// ErrRedemptionBatchExists is returned by ProvisionRedemptionBatchIdempotent
// when the event_id was already processed. The caller should treat this as a
// successful replay and return the previously-generated codes with HTTP 200.
var ErrRedemptionBatchExists = errors.New("redemption batch already provisioned (idempotent replay)")

// ErrRedemptionBatchNotFound is returned by RevokeProvisionedRedemptionBatch
// when no batch with the given event_id exists for the tenant.
var ErrRedemptionBatchNotFound = errors.New("provisioned redemption batch not found")

// ProvisionRedemptionBatchIdempotent atomically mints `count` redemption codes
// for a tenant, recorded as one distributor batch keyed by eventID.
// Idempotency is enforced via UNIQUE(event_id) on provisioned_redemption_batches
// (migration 027), mirroring FundPoolIdempotent:
//
//   - If event_id was already processed, the existing batch row is returned
//     alongside ErrRedemptionBatchExists with the ORIGINAL codes decoded from
//     the row's Codes column — the caller returns 200 replayed=true, and no
//     second set of codes is minted.
//   - Otherwise a single transaction inserts the batch row (codes stored as a
//     JSON array so replay readback never depends on querying redemptions
//     back) and then the `count` redemption rows. Any failure rolls back the
//     whole batch. A concurrent replay losing the UNIQUE race is re-fetched
//     and surfaced as ErrRedemptionBatchExists so the caller still returns 200.
//
// Codes are generated with common.GetUUID() (32 chars — the same generator as
// the admin AddRedemption path, and the length RedeemCodeV2 validates).
// Redemption rows are named "<namePrefix>-<n>" (1-based), carry
// quota=quotaPerCode, expiredTime (unix seconds, 0 = never) and are owned by
// creatorUserID (the internal API key's CreatedBy, 0 for platform keys).
//
// Returns (batch, codes, nil) on first write,
// (existingBatch, originalCodes, ErrRedemptionBatchExists) on replay.
func ProvisionRedemptionBatchIdempotent(
	ctx context.Context,
	tenantID string, eventID string,
	count int, quotaPerCode int64,
	namePrefix string, expiredTime int64,
	source string, creatorUserID int,
) (*ProvisionedRedemptionBatch, []string, error) {
	if eventID == "" {
		return nil, nil, fmt.Errorf("event_id is required for idempotent batch issuance: provide the platform event ID")
	}
	if count <= 0 {
		return nil, nil, fmt.Errorf("count must be positive, got %d", count)
	}
	if quotaPerCode <= 0 {
		return nil, nil, fmt.Errorf("quota_per_code must be positive, got %d", quotaPerCode)
	}

	// Fast-path: check if already processed before generating anything.
	// The unique constraint is the authoritative guard; this pre-check merely
	// avoids burning code generation + inserts on obvious replays.
	var existing ProvisionedRedemptionBatch
	if err := DB.WithContext(ctx).Where("event_id = ?", eventID).First(&existing).Error; err == nil {
		codes, decodeErr := decodeBatchCodes(existing.Codes)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		return &existing, codes, ErrRedemptionBatchExists
	}

	// Generate the codes up front so the batch row can store them — the batch
	// row is the replay source of truth, not the redemptions table.
	codes := make([]string, count)
	for i := range codes {
		codes[i] = common.GetUUID()
	}
	codesJSON, err := json.Marshal(codes)
	if err != nil {
		return nil, nil, fmt.Errorf("encode batch codes: %w", err)
	}

	now := common.GetTimestamp()
	batch := &ProvisionedRedemptionBatch{
		EventID:      eventID,
		TenantID:     tenantID,
		BatchSize:    count,
		QuotaPerCode: quotaPerCode,
		Source:       source,
		Codes:        string(codesJSON),
		CreatedAt:    time.Now(),
	}

	// WithTenantID feeds the TenantPlugin's beforeCreate hook: `redemptions`
	// is on the plugin's auto-scoped table list, and a Create without a tenant
	// ID in context errors out ("tenant_id is required for create operations").
	// The batch table is not on that list, so the scoped handle is harmless
	// there. The caller ctx IS threaded — WithTenantID derives from
	// Statement.Context, seeded by WithContext(ctx) below.
	scoped := WithTenantID(DB.WithContext(ctx), tenantID) //nolint:contextcheck // ctx enters via WithContext; the helper reads Statement.Context
	txErr := scoped.Transaction(func(tx *gorm.DB) error {
		if insertErr := tx.Create(batch).Error; insertErr != nil {
			// Unique-constraint violation from a concurrent replay: surface the
			// sentinel; the winner's row is re-fetched outside the rolled-back tx.
			if isUniqueViolation(insertErr) {
				return ErrRedemptionBatchExists
			}
			return fmt.Errorf("redemption batch insert: %w", insertErr)
		}

		for i, key := range codes {
			redemption := &Redemption{
				UserId:      creatorUserID,
				TenantId:    tenantID,
				Key:         key,
				Status:      common.RedemptionCodeStatusEnabled,
				Name:        fmt.Sprintf("%s-%d", namePrefix, i+1),
				Quota:       int(quotaPerCode),
				CreatedTime: now,
				ExpiredTime: expiredTime,
			}
			if insertErr := tx.Create(redemption).Error; insertErr != nil {
				return fmt.Errorf("redemption insert %d/%d: %w", i+1, count, insertErr)
			}
		}
		return nil
	})

	if txErr != nil {
		if errors.Is(txErr, ErrRedemptionBatchExists) {
			var race ProvisionedRedemptionBatch
			if fetchErr := DB.WithContext(ctx).Where("event_id = ?", eventID).First(&race).Error; fetchErr != nil {
				return nil, nil, fmt.Errorf("redemption batch replay re-fetch: %w", fetchErr)
			}
			raceCodes, decodeErr := decodeBatchCodes(race.Codes)
			if decodeErr != nil {
				return nil, nil, decodeErr
			}
			return &race, raceCodes, ErrRedemptionBatchExists
		}
		return nil, nil, txErr
	}

	return batch, codes, nil
}

// RevokeProvisionedRedemptionBatch disables every still-unused code of the
// batch identified by eventID (status enabled → disabled; used codes are
// untouched). Naturally idempotent: revoking an already-revoked batch matches
// zero enabled rows and returns 0. Returns ErrRedemptionBatchNotFound when the
// tenant has no batch with that event_id.
func RevokeProvisionedRedemptionBatch(ctx context.Context, tenantID string, eventID string) (int64, error) {
	if eventID == "" {
		return 0, fmt.Errorf("event_id is required: identify the batch to revoke")
	}

	var batch ProvisionedRedemptionBatch
	if err := DB.WithContext(ctx).
		Where("event_id = ? AND tenant_id = ?", eventID, tenantID).
		First(&batch).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrRedemptionBatchNotFound
		}
		return 0, fmt.Errorf("load redemption batch: %w", err)
	}

	codes, err := decodeBatchCodes(batch.Codes)
	if err != nil {
		return 0, err
	}
	if len(codes) == 0 {
		return 0, nil
	}

	// PG-only runtime; the SQLite test tier also accepts double-quoted
	// identifiers (same quoting as repo.Redeem).
	keyCol := `"key"`
	result := DB.WithContext(ctx).Model(&Redemption{}).
		Where("tenant_id = ?", tenantID).
		Where(keyCol+" IN ?", codes).
		Where("status = ?", common.RedemptionCodeStatusEnabled).
		Update("status", common.RedemptionCodeStatusDisabled)
	if result.Error != nil {
		return 0, fmt.Errorf("disable batch redemptions: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// decodeBatchCodes unmarshals the JSON array stored in
// provisioned_redemption_batches.codes.
func decodeBatchCodes(raw string) ([]string, error) {
	var codes []string
	if err := json.Unmarshal([]byte(raw), &codes); err != nil {
		return nil, fmt.Errorf("decode batch codes: %w", err)
	}
	return codes, nil
}
