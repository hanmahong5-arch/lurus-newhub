package repo

// token_rotation_cas_test.go — RotateKeyWithTimestampCAS race semantics.
// Hermetic sqlite tier: the CAS is a plain conditional UPDATE, so its
// first-writer-wins behaviour is backend-independent.

import (
	"errors"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestToken_RotateKeyCAS_FirstWriterWins: two rotators observed the same
// baseline; the first conditional UPDATE lands, the second must lose with
// ErrRotationRaceLost and must not overwrite the winner's key.
func TestToken_RotateKeyCAS_FirstWriterWins(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "cas_race_u", "casrace@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 1000, -1)

	// Both rotators load the token at the same baseline (rotated_at = 0).
	var first, second Token
	if err := DB.First(&first, tok.Id).Error; err != nil {
		t.Fatalf("load first: %v", err)
	}
	if err := DB.First(&second, tok.Id).Error; err != nil {
		t.Fatalf("load second: %v", err)
	}

	winnerKey := "cas-winner-key-0000000000000000000000000"
	loserKey := "cas-loser-key-00000000000000000000000000"

	if err := first.RotateKeyWithTimestampCAS(winnerKey, first.RotatedAt, 1000); err != nil {
		t.Fatalf("first rotator must win: %v", err)
	}
	err := second.RotateKeyWithTimestampCAS(loserKey, second.RotatedAt, 2000)
	if !errors.Is(err, ErrRotationRaceLost) {
		t.Fatalf("second rotator must lose with ErrRotationRaceLost, got: %v", err)
	}

	var reloaded Token
	if err := DB.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Key != winnerKey {
		t.Errorf("persisted key must be the winner's: got %q", reloaded.Key)
	}
	if reloaded.RotatedAt != 1000 {
		t.Errorf("persisted rotated_at must be the winner's timestamp 1000, got %d", reloaded.RotatedAt)
	}
	// The loser's in-memory state must be untouched (callers skip audit/email
	// off it).
	if second.Key == loserKey {
		t.Error("loser must not mutate its in-memory key on a lost race")
	}
}

// TestToken_RotateKeyCAS_StaleBaselineLoses: a rotator whose observed
// rotated_at no longer matches the persisted value must lose without
// touching the row.
func TestToken_RotateKeyCAS_StaleBaselineLoses(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "cas_stale_u", "casstale@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 1000, -1)

	if err := DB.Model(&Token{}).Where("id = ?", tok.Id).Update("rotated_at", 500).Error; err != nil {
		t.Fatalf("advance rotated_at: %v", err)
	}
	originalKey := tok.Key

	err := tok.RotateKeyWithTimestampCAS("cas-stale-key-00000000000000000000000000", 0, 3000)
	if !errors.Is(err, ErrRotationRaceLost) {
		t.Fatalf("stale baseline must lose with ErrRotationRaceLost, got: %v", err)
	}

	var reloaded Token
	if err := DB.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Key != originalKey {
		t.Errorf("key must be unchanged after a lost race: got %q", reloaded.Key)
	}
	if reloaded.RotatedAt != 500 {
		t.Errorf("rotated_at must be unchanged (500), got %d", reloaded.RotatedAt)
	}
}

// TestToken_RotateKeyCAS_MatchingBaselineSucceeds: the happy path — the
// persisted rotated_at equals the observed baseline, the swap lands and the
// in-memory token is advanced.
func TestToken_RotateKeyCAS_MatchingBaselineSucceeds(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	u := seedUser(t, "cas_ok_u", "casok@test.com", common.RoleCommonUser, common.UserStatusEnabled, "default")
	tok := seedToken(t, u.Id, common.TokenStatusEnabled, true, 1000, -1)

	newKey := "cas-happy-key-00000000000000000000000000"
	if err := tok.RotateKeyWithTimestampCAS(newKey, tok.RotatedAt, 4000); err != nil {
		t.Fatalf("matching baseline must succeed: %v", err)
	}
	if tok.Key != newKey || tok.RotatedAt != 4000 {
		t.Errorf("in-memory token must advance: key=%q rotated_at=%d", tok.Key, tok.RotatedAt)
	}

	var reloaded Token
	if err := DB.First(&reloaded, tok.Id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Key != newKey || reloaded.RotatedAt != 4000 {
		t.Errorf("persisted token must advance: key=%q rotated_at=%d", reloaded.Key, reloaded.RotatedAt)
	}
}
