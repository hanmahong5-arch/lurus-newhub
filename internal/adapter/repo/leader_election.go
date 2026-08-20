package repo

import (
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"gorm.io/gorm/clause"
)

// TryAcquireOrRenew attempts to acquire or renew the named leader lease for
// holderId. ttl and now are unix-second values; now is injected so the
// election semantics are fully deterministic under test. It returns true when
// the caller holds a valid lease after the call.
//
// Concurrency contract: the renew/takeover path is a single conditional
// UPDATE, so the database serializes competing writers — at most one of them
// can match the (holder-is-me OR lease-expired) predicate and win per row. The
// first-ever acquire is an INSERT under the name primary key, made idempotent
// with ON CONFLICT DO NOTHING, so a racing loser is told it lost by
// RowsAffected == 0 rather than by an error. The net invariant: at any
// wall-clock instant at most one holder owns a non-expired lease for a name.
func TryAcquireOrRenew(name, holderId string, ttl, now int64) (bool, error) {
	if name == "" || holderId == "" {
		return false, fmt.Errorf("leader election: name and holderId are required")
	}
	if ttl <= 0 {
		return false, fmt.Errorf("leader election: ttl must be positive, got %d", ttl)
	}
	expiresAt := now + ttl

	// Conditional renew/takeover: win iff we already hold it or the current
	// lease has expired. RowsAffected == 1 means we (re)acquired it.
	res := DB.Model(&entity.LeaderElection{}).
		Where("name = ? AND (holder_id = ? OR expires_at < ?)", name, holderId, now).
		Updates(map[string]interface{}{
			"holder_id":  holderId,
			"renewed_at": now,
			"expires_at": expiresAt,
		})
	if res.Error != nil {
		return false, fmt.Errorf("leader election renew: %w", res.Error)
	}
	if res.RowsAffected == 1 {
		return true, nil
	}

	// No row was updated: either the lease row does not exist yet, or another
	// holder owns a still-valid lease. Insert with ON CONFLICT DO NOTHING so the
	// second case resolves to RowsAffected == 0 instead of a duplicate-key error.
	//
	// That distinction is not cosmetic. Losing the election is the steady state
	// for every replica but one, on every renewal tick (TTL 30s / 3 = one every
	// 10s), so raising it as a SQL error had GORM's logger print it at ERROR
	// level forever. Measured on R6 over a 10-minute window, 2026-08-20: each of
	// the two follower pods emitted 122 lines of it — 2 lines per tick, the
	// error and the echoed INSERT — which was 29% and 25% of everything those
	// pods logged at all. Nothing was wrong; the cluster had simply elected a
	// leader, 8 640 times a day, per follower.
	lease := &entity.LeaderElection{
		Name:       name,
		HolderId:   holderId,
		AcquiredAt: now,
		RenewedAt:  now,
		ExpiresAt:  expiresAt,
	}
	ins := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(lease)
	if ins.Error != nil {
		return false, fmt.Errorf("leader election acquire: %w", ins.Error)
	}
	return ins.RowsAffected == 1, nil
}

// ReleaseLease relinquishes the named lease if this holder still owns it, by
// expiring it immediately. A gracefully shutting-down leader calls this so the
// successor takes over at once instead of waiting out the full TTL. It is a
// no-op when another holder already owns the lease.
func ReleaseLease(name, holderId string) error {
	res := DB.Model(&entity.LeaderElection{}).
		Where("name = ? AND holder_id = ?", name, holderId).
		Update("expires_at", 0)
	if res.Error != nil {
		return fmt.Errorf("leader election release: %w", res.Error)
	}
	return nil
}
