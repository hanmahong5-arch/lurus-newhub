package repo

// response_registry.go — persistence for the response_registry table
// (migration 036, cycle-8 L7, tasks-plugins-12): pins a POST /v1/responses
// id to the channel + upstream model that produced it, so GET/DELETE
// /v1/responses/:response_id (handler.RelayResponsesRetrieve/Delete) can
// route straight back to that channel. See entity.ResponseRegistry's doc
// comment for the full write/read/sweep lifecycle.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrResponseRegistryNotFound covers "no row with this response_id" — the
// handler answers the same 404 for this as for an ownership mismatch, so
// callers must not distinguish the two by inspecting this error's text.
var ErrResponseRegistryNotFound = errors.New("response registry row not found")

// responseRegistryDefaultTTLDays is used when RESPONSE_REGISTRY_TTL_DAYS is
// unset or not a positive integer.
const responseRegistryDefaultTTLDays = 30

// ResponseRegistryTTLSeconds resolves the registry row lifetime from
// RESPONSE_REGISTRY_TTL_DAYS (documented in .env.example), read fresh on
// every call — same "no caching" convention as SessionRegistryEnabled and
// CREDIT_POOL_RESET_MODE, so an operator's env change takes effect on the
// next request rather than the next restart.
func ResponseRegistryTTLSeconds() int64 {
	days := responseRegistryDefaultTTLDays
	if raw := os.Getenv("RESPONSE_REGISTRY_TTL_DAYS"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			days = n
		}
	}
	return int64(days) * 24 * 60 * 60
}

// UpsertResponseRegistry writes or replaces the registry row for
// row.ResponseId, keyed on the response_id PRIMARY KEY, so a caller that
// legitimately re-requests the same response_id (e.g. a retried POST that
// the vendor deduplicated) never sees a PRIMARY KEY collision error on the
// billed hot path this is called from.
func UpsertResponseRegistry(row *entity.ResponseRegistry) error {
	if row == nil || row.ResponseId == "" {
		return errors.New("response_id is required")
	}
	err := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "response_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"tenant_id", "user_id", "token_id", "channel_id", "upstream_model", "created_at", "expires_at",
		}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("upsert response registry: %w", err)
	}
	return nil
}

// GetResponseRegistry looks up a row by its vendor-minted response_id.
// Returns ErrResponseRegistryNotFound (never gorm.ErrRecordNotFound
// directly) so callers compare against one sentinel regardless of dialect.
func GetResponseRegistry(responseId string) (*entity.ResponseRegistry, error) {
	if responseId == "" {
		return nil, ErrResponseRegistryNotFound
	}
	var row entity.ResponseRegistry
	err := DB.Where("response_id = ?", responseId).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrResponseRegistryNotFound
		}
		return nil, fmt.Errorf("get response registry: %w", err)
	}
	return &row, nil
}

// DeleteResponseRegistry removes a row by response_id. Deleting an absent
// id is not an error — the caller (handler.RelayResponsesDelete) only calls
// this after a successful GetResponseRegistry, so "already gone" can only
// happen under a race with the sweep, which is harmless to no-op.
func DeleteResponseRegistry(responseId string) error {
	if responseId == "" {
		return nil
	}
	if err := DB.Where("response_id = ?", responseId).Delete(&entity.ResponseRegistry{}).Error; err != nil {
		return fmt.Errorf("delete response registry: %w", err)
	}
	return nil
}

// responseRegistrySweepBatchSize bounds the per-DELETE row count so a
// long-overdue sweep doesn't lock the table in one giant transaction.
// Matches auditCleanupBatchSize's convention.
const responseRegistrySweepBatchSize = 1000

// SweepExpiredResponseRegistry deletes rows whose expires_at has passed,
// batched at responseRegistrySweepBatchSize per DELETE. now is injectable
// for deterministic tests.
func SweepExpiredResponseRegistry(now time.Time) (int64, error) {
	var total int64
	cutoff := now.Unix()
	for {
		idQuery := DB.Model(&entity.ResponseRegistry{}).
			Select("response_id").
			Where("expires_at <= ?", cutoff).
			Order("response_id").
			Limit(responseRegistrySweepBatchSize)
		result := DB.Where("response_id IN (?)", idQuery).Delete(&entity.ResponseRegistry{})
		if result.Error != nil {
			return total, fmt.Errorf("sweep expired response registry: %w", result.Error)
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(responseRegistrySweepBatchSize) {
			break
		}
	}
	return total, nil
}

// HardDeleteUserResponseRegistry removes every response_registry row that
// names userID as its owner — the PIPL erasure cascade's hard-delete step
// (lifecycle/privacy_erasure.go executeErasure, same step as
// HardDeleteUserSessions/HardDeleteUserTokens). The row's TTL-based sweep
// (SweepExpiredResponseRegistry above) is a retention ceiling, not an
// erasure mechanism: a row written the same day as an erasure request would
// otherwise still hold user_id/token_id/vendor response ids for up to
// RESPONSE_REGISTRY_TTL_DAYS after the account was supposed to be erased
// (cycle-8 L7 repair round, finding B-F9 — user_sessions was folded into
// the same cascade step for the same "personal-adjacent-data" reasoning).
// Unscoped like HardDeleteUserSessions: this table has no soft-delete
// column at all, so Unscoped only documents intent, it changes nothing.
func HardDeleteUserResponseRegistry(ctx context.Context, userID int) (int64, error) {
	result := WithoutTenantIsolationCtx(ctx, DB).Unscoped().
		Where("user_id = ?", userID).
		Delete(&entity.ResponseRegistry{})
	if result.Error != nil {
		return 0, fmt.Errorf("hard delete response registry: %w", result.Error)
	}
	return result.RowsAffected, nil
}
