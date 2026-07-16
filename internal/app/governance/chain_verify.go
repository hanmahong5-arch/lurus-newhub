package governance

// chain_verify.go — read-side of the audit tamper-evidence hash chain
// (migration 024). Recomputes per-row hashes and per-tenant prev-hash linkage
// over an id-ordered window of audit_events.
//
// Trust model recap (write side lives in entity.AuditEvent.BeforeCreate):
//   - row_hash mismatch  → the row's chain-covered content was altered after
//     insert (definite tamper signal → first_break).
//   - prev_hash linkage mismatch → a chained row between two others vanished
//     or the head was manipulated. Retention pruning (lifecycle audit_cleanup)
//     legitimately deletes expired rows, which severs linkage without touching
//     content — so link breaks are reported SEPARATELY from content breaks and
//     need operator correlation with retention logs before being treated as
//     tamper evidence.
//   - empty row_hash → legacy row (pre-024, never backfilled) or a fail-open
//     fallback insert; counted as legacy_rows, never as a break.

import (
	"errors"

	"gorm.io/gorm"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

const (
	// chainVerifyDefaultLimit / chainVerifyMaxLimit bound one verification
	// call so the endpoint cannot trigger an unbounded full-table scan;
	// callers page with next_cursor.
	chainVerifyDefaultLimit = 1000
	chainVerifyMaxLimit     = 10000
)

// ChainBreak pinpoints the first offending row of a break class.
type ChainBreak struct {
	ID       int64  `json:"id"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// ChainVerifyParams narrows the verification window.
type ChainVerifyParams struct {
	// TenantID limits verification to one tenant's chain ("" = all tenants;
	// chains are per-tenant either way).
	TenantID string
	// AfterID is the id cursor: only rows with id > AfterID are examined.
	AfterID int64
	// Limit caps rows examined per call (default 1000, max 10000).
	Limit int
	// StartTime/EndTime (unix seconds, 0 = unbounded) filter on the event
	// timestamp. A time window selects a non-contiguous slice of the chain,
	// so prev-hash LINK checks are disabled in time-filtered mode (content
	// row-hash checks still run); see ChainVerifyResult.LinkChecksSkipped.
	StartTime int64
	EndTime   int64
}

// ChainVerifyResult is the verification report for one window.
type ChainVerifyResult struct {
	// Checked counts chained rows whose row_hash was recomputed and compared.
	Checked int `json:"checked"`
	// LegacyRows counts empty-hash rows (pre-chain or fail-open fallback).
	LegacyRows int `json:"legacy_rows"`
	// HashBreaks counts content-hash mismatches; FirstBreak is the first.
	HashBreaks int         `json:"hash_breaks"`
	FirstBreak *ChainBreak `json:"first_break"`
	// LinkBreaks counts prev-hash linkage mismatches; FirstLinkBreak is the
	// first. See package comment for the retention-prune caveat.
	LinkBreaks     int         `json:"link_breaks"`
	FirstLinkBreak *ChainBreak `json:"first_link_break"`
	// LinkChecksSkipped is true in time-filtered mode.
	LinkChecksSkipped bool `json:"link_checks_skipped"`
	// NextCursor is non-zero when more rows remain; pass it back as AfterID.
	NextCursor int64 `json:"next_cursor"`
}

// VerifyAuditChain re-derives the hash chain over one id-ordered window.
// Read-only; safe to run against the live table (bounded by Limit).
func VerifyAuditChain(db *gorm.DB, p ChainVerifyParams) (*ChainVerifyResult, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = chainVerifyDefaultLimit
	}
	if limit > chainVerifyMaxLimit {
		limit = chainVerifyMaxLimit
	}

	q := db.Model(&entity.AuditEvent{}).Where("id > ?", p.AfterID)
	if p.TenantID != "" {
		q = q.Where("tenant_id = ?", p.TenantID)
	}
	timeFiltered := p.StartTime > 0 || p.EndTime > 0
	if p.StartTime > 0 {
		q = q.Where("timestamp >= ?", p.StartTime)
	}
	if p.EndTime > 0 {
		q = q.Where("timestamp <= ?", p.EndTime)
	}

	var rows []*entity.AuditEvent
	// limit+1 detects whether another page remains without a COUNT scan.
	if err := q.Order("id ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	res := &ChainVerifyResult{LinkChecksSkipped: timeFiltered}
	if len(rows) > limit {
		rows = rows[:limit]
		res.NextCursor = rows[len(rows)-1].ID
	}

	// lastHash tracks, per tenant, the row_hash of the most recent chained
	// row seen; seeded from the table for the first chained row of each
	// tenant in this window so paging does not lose cross-page linkage.
	lastHash := map[string]string{}
	seeded := map[string]bool{}

	for _, e := range rows {
		if e.RowHash == "" {
			// Legacy / fallback rows never advanced the chain head, so they
			// neither consume nor reset link state.
			res.LegacyRows++
			continue
		}

		if !timeFiltered {
			if _, ok := lastHash[e.TenantID]; !ok && !seeded[e.TenantID] {
				seeded[e.TenantID] = true
				prevHash, found, err := priorChainedHash(db, e.TenantID, e.ID)
				if err != nil {
					return nil, err
				}
				if found {
					lastHash[e.TenantID] = prevHash
				}
				// Not found → this is the tenant's first chained row still
				// present (genesis, or every predecessor was retention-pruned).
				// Expected prev_hash is unknowable → skip the link check once.
			}
			if want, ok := lastHash[e.TenantID]; ok && e.PrevHash != want {
				res.LinkBreaks++
				if res.FirstLinkBreak == nil {
					res.FirstLinkBreak = &ChainBreak{ID: e.ID, Expected: want, Actual: e.PrevHash}
				}
			}
			lastHash[e.TenantID] = e.RowHash
		}

		res.Checked++
		if expected := entity.ComputeAuditRowHash(e); expected != e.RowHash {
			res.HashBreaks++
			if res.FirstBreak == nil {
				res.FirstBreak = &ChainBreak{ID: e.ID, Expected: expected, Actual: e.RowHash}
			}
		}
	}
	return res, nil
}

// priorChainedHash returns the row_hash of the newest chained row of the
// tenant strictly before beforeID (found=false when none exists).
func priorChainedHash(db *gorm.DB, tenantID string, beforeID int64) (string, bool, error) {
	var prev entity.AuditEvent
	err := db.Model(&entity.AuditEvent{}).
		Where("tenant_id = ? AND id < ? AND row_hash <> ''", tenantID, beforeID).
		Order("id DESC").
		Take(&prev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return prev.RowHash, true, nil
}
