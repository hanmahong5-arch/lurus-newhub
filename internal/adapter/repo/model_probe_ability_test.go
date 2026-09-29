package repo

import (
	"sync/atomic"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// model_probe_ability_test.go — the DB-fallback (no memory cache) routing
// path: entity.ModelHealth.AutoDisabled must be reflected into the abilities
// table (SetChannelModelAbilityEnabled), and AddAbilities/UpdateAbilities
// must not resurrect an auto-disabled pair as enabled when a channel is
// re-saved.

var modelProbeAbilityDBCounter atomic.Int64

// setupAbilityProbeDB gives the test a fresh sqlite DB with channels,
// abilities and model_health migrated. Deliberately independent of any
// other test file's setup helper in this package.
func setupAbilityProbeDB(t *testing.T, migrateModelHealth bool) {
	t.Helper()
	n := modelProbeAbilityDBCounter.Add(1)
	dsn := "file:abilityprobe" + itoaAbility(n) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}, &Ability{}); err != nil {
		t.Fatalf("migrate channel/ability: %v", err)
	}
	if migrateModelHealth {
		if err := db.AutoMigrate(&entity.ModelHealth{}); err != nil {
			t.Fatalf("migrate model_health: %v", err)
		}
	}
	prev := DB
	DB = db
	t.Cleanup(func() { DB = prev })
}

func itoaAbility(n int64) string {
	// small helper to avoid pulling in strconv just for a counter suffix
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestSetChannelModelAbilityEnabled_UpdatesMatchingRows(t *testing.T) {
	setupAbilityProbeDB(t, true)

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1,m2", Group: "free,paid"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := ch.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}

	if err := SetChannelModelAbilityEnabled(ch.Id, "m1", false); err != nil {
		t.Fatal(err)
	}

	var rows []Ability
	DB.Where("channel_id = ?", ch.Id).Order("\"group\"").Find(&rows)
	for _, r := range rows {
		if r.Model == "m1" && r.Enabled {
			t.Errorf("m1/%s still enabled after disable", r.Group)
		}
		if r.Model == "m2" && !r.Enabled {
			t.Errorf("m2/%s must be untouched (still enabled)", r.Group)
		}
	}
}

func TestSetChannelModelAbilityEnabled_EnablingSkipsWhenChannelDisabled(t *testing.T) {
	setupAbilityProbeDB(t, true)

	ch := &Channel{Status: 2 /* disabled */, Models: "m1", Group: "free"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	// Seed a disabled ability row directly (channel is disabled so
	// AddAbilities would already write Enabled=false, but make the starting
	// state explicit).
	if err := DB.Create(&Ability{ChannelId: ch.Id, Model: "m1", Group: "free", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}

	if err := SetChannelModelAbilityEnabled(ch.Id, "m1", true); err != nil {
		t.Fatal(err)
	}

	var row Ability
	if err := DB.Where("channel_id = ? AND model = ?", ch.Id, "m1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Enabled {
		t.Error("enabling a model's ability rows must not enable them when the channel itself is not enabled")
	}
}

func TestSetChannelModelAbilityEnabled_EnablingWorksWhenChannelEnabled(t *testing.T) {
	setupAbilityProbeDB(t, true)

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1", Group: "free"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&Ability{ChannelId: ch.Id, Model: "m1", Group: "free", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}

	if err := SetChannelModelAbilityEnabled(ch.Id, "m1", true); err != nil {
		t.Fatal(err)
	}

	var row Ability
	if err := DB.Where("channel_id = ? AND model = ?", ch.Id, "m1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !row.Enabled {
		t.Error("enabling must succeed when the owning channel is enabled")
	}
}

func TestAddAbilities_SkipsAutoDisabledPair(t *testing.T) {
	setupAbilityProbeDB(t, true)

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1,m2", Group: "free"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&entity.ModelHealth{ChannelId: ch.Id, Model: "m1", AutoDisabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := ch.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}

	var m1, m2 Ability
	DB.Where("channel_id = ? AND model = ?", ch.Id, "m1").First(&m1)
	DB.Where("channel_id = ? AND model = ?", ch.Id, "m2").First(&m2)
	if m1.Enabled {
		t.Error("m1 is auto-disabled, AddAbilities must not enable it")
	}
	if !m2.Enabled {
		t.Error("m2 has no auto-disabled row, must be enabled (channel itself is enabled)")
	}
}

func TestUpdateAbilities_KeepsAutoDisabledPairDisabled(t *testing.T) {
	setupAbilityProbeDB(t, true)

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1,m2", Group: "free"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := ch.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	if err := DB.Create(&entity.ModelHealth{ChannelId: ch.Id, Model: "m1", AutoDisabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	// UpdateAbilities is what a channel edit/save calls — it must re-derive
	// abilities without resurrecting the auto-disabled pair.
	if err := ch.UpdateAbilities(nil); err != nil {
		t.Fatal(err)
	}

	var m1, m2 Ability
	DB.Where("channel_id = ? AND model = ?", ch.Id, "m1").First(&m1)
	DB.Where("channel_id = ? AND model = ?", ch.Id, "m2").First(&m2)
	if m1.Enabled {
		t.Error("m1 is auto-disabled, UpdateAbilities must not re-enable it")
	}
	if !m2.Enabled {
		t.Error("m2 must stay enabled")
	}
}

func TestAddAbilities_FailsOpenWhenModelHealthLookupErrors(t *testing.T) {
	// model_health table deliberately NOT migrated, so the auto-disabled
	// lookup query errors — AddAbilities must fail open (behave exactly as
	// if there were no auto-disabled pairs) rather than erroring the whole
	// channel save.
	setupAbilityProbeDB(t, false)

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1", Group: "free"}
	if err := DB.Create(ch).Error; err != nil {
		t.Fatal(err)
	}

	if err := ch.AddAbilities(nil); err != nil {
		t.Fatalf("AddAbilities must fail open, got error: %v", err)
	}

	var row Ability
	if err := DB.Where("channel_id = ? AND model = ?", ch.Id, "m1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !row.Enabled {
		t.Error("fail-open: with no auto-disabled data available, ability must be enabled per normal channel status")
	}
}

func TestAddAbilities_UsesTheProvidedTxNotThePackageDB(t *testing.T) {
	// Regression: AddAbilities(tx) is called by bootstrap/test fixtures
	// BEFORE the package-level DB is ever published (repo.DB is nil at
	// that point) — a real caller is
	// relay_router_responses_compact_test.go's respCompactRouterFixture.
	// autoDisabledModelsForChannel must read through the tx it was given,
	// not through the package DB var, or this panics on a nil DB instead
	// of erroring.
	n := modelProbeAbilityDBCounter.Add(1)
	dsn := "file:abilitytxprobe" + itoaAbility(n) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}, &Ability{}, &entity.ModelHealth{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	prevDB := DB
	DB = nil // the exact precondition that caused the panic
	t.Cleanup(func() { DB = prevDB })

	ch := &Channel{Status: common.ChannelStatusEnabled, Models: "m1", Group: "free"}
	if err := db.Create(ch).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entity.ModelHealth{ChannelId: ch.Id, Model: "m1", AutoDisabled: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := ch.AddAbilities(db); err != nil {
		t.Fatalf("AddAbilities(tx) with a nil package DB must not error (and must not panic): %v", err)
	}

	var row Ability
	if err := db.Where("channel_id = ? AND model = ?", ch.Id, "m1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Enabled {
		t.Error("must still read auto_disabled through the provided tx, not silently skip it because DB is nil")
	}
}
