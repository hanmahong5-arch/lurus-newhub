package repo

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// cycle-13 L7: Edit()'s updates map silently dropped daily_quota/base_group/
// fallback_group entirely — EditUserModal.jsx submits all three on every
// save and the handler answered success while nothing was written. This
// exercises the real write path (Edit(), against the sqlite fixture used
// throughout this package — setupSQLiteDB/seedUser, same pattern as
// TestUserRepo_Edit in sqlite_repo_extra_test.go) and a fresh read
// afterward, not a hand-built shape: a seeded user starts at the zero
// values for all three, Edit() is called with new non-zero values, and a
// SEPARATE read (not the in-memory struct Edit() mutated) must see them.
func TestUserEdit_PersistsDailyQuotaBaseGroupFallbackGroup(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()

	// seedUser leaves DailyQuota/BaseGroup/FallbackGroup at their zero
	// values — Edit() must move all three away from those, not merely
	// leave them alone by coincidence.
	seed := seedUser(t, "editpersist", "editpersist@test.local", common.RoleCommonUser, common.UserStatusEnabled, "default")

	edit := &User{
		Id:            seed.Id,
		Username:      seed.Username,
		DisplayName:   seed.DisplayName,
		Group:         seed.Group,
		Quota:         seed.Quota,
		Remark:        seed.Remark,
		DailyQuota:    250000,
		BaseGroup:     "vip",
		FallbackGroup: "default",
	}
	if err := edit.Edit(); err != nil {
		t.Fatalf("Edit() failed: %v", err)
	}

	// A fresh read, not the `edit` struct Edit() already mutated in place —
	// the whole point is proving the database row changed.
	var got User
	if err := DB.First(&got, "id = ?", seed.Id).Error; err != nil {
		t.Fatalf("re-read after Edit() failed: %v", err)
	}
	if got.DailyQuota != 250000 {
		t.Errorf("DailyQuota after Edit() = %d, want 250000", got.DailyQuota)
	}
	if got.BaseGroup != "vip" {
		t.Errorf("BaseGroup after Edit() = %q, want %q", got.BaseGroup, "vip")
	}
	if got.FallbackGroup != "default" {
		t.Errorf("FallbackGroup after Edit() = %q, want %q", got.FallbackGroup, "default")
	}
}
