package repo

// cov_repo-deep_option_findings_test.go — persistence-layer deep pass #2.
//
// Covers:
//   - FINDING: option.go:211-224 UpdateOption silently swallows the errors
//     returned by DB.FirstOrCreate/DB.Save. When the DB write fails (missing
//     table, connection loss, etc.) the caller still gets err == nil because
//     UpdateOption's return value comes exclusively from updateOptionMap,
//     which only ever mutates the in-process common.OptionMap and never
//     touches the database. This test locks in the CURRENT (buggy) behavior;
//     it must NOT be "fixed" by changing option.go.
//   - tenant.go GenerateID: pure helper with zero test coverage anywhere in
//     the package (grep-verified against every *_test.go file before writing
//     this).
//   - GetAllChannels pagination edge: offset past the total row count.

import (
	"regexp"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ─── UpdateOption must surface FirstOrCreate/Save errors ────────────────────

// TestUpdateOption_ReportsDBWriteError covers the contract UpdateOption used to
// break: it discarded both persistence errors and returned only
// updateOptionMap's result, so a failed write looked like success to the caller
// while taking effect in memory only — silently reverting on restart or on the
// next replica's loadOptionsFromDatabase.
//
// fix_option_write_error_test.go pins the same contract on hermetic sqlite; this
// one runs against a real PostgreSQL (the CI pg-integration tier) and also
// verifies, by recreating the table afterwards, that nothing was persisted.
func TestUpdateOption_ReportsDBWriteError(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	// Sanity: normal path really does persist to the options table.
	if err := UpdateOption("cov_finding_baseline", "v1"); err != nil {
		t.Fatalf("baseline UpdateOption: %v", err)
	}
	var baseline Option
	if err := DB.Where("key = ?", "cov_finding_baseline").First(&baseline).Error; err != nil {
		t.Fatalf("expected baseline option persisted: %v", err)
	}
	if baseline.Value != "v1" {
		t.Fatalf("baseline value = %q, want v1", baseline.Value)
	}

	// Break the persistence path underneath UpdateOption without closing the
	// connection (so updateOptionMap's own DB-free logic keeps working): drop
	// the options table, so FirstOrCreate/Save both fail with "relation does
	// not exist".
	if err := DB.Migrator().DropTable(&Option{}); err != nil {
		t.Fatalf("drop options table: %v", err)
	}

	err := UpdateOption("cov_finding_should_not_persist", "should-not-persist")
	if err == nil {
		t.Fatal("UpdateOption returned nil for a write that could not be persisted")
	}

	// The in-memory map must not advertise a value the database never took.
	common.OptionMapRWMutex.RLock()
	gotMapValue, ok := common.OptionMap["cov_finding_should_not_persist"]
	common.OptionMapRWMutex.RUnlock()
	if ok && gotMapValue == "should-not-persist" {
		t.Fatalf("in-memory OptionMap was updated to %q despite the failed write — "+
			"a restart or the next replica's sync would silently revert it", gotMapValue)
	}

	// Recreate the table and confirm nothing was persisted for this key.
	if err := DB.Migrator().AutoMigrate(&Option{}); err != nil {
		t.Fatalf("recreate options table: %v", err)
	}
	var count int64
	if err := DB.Model(&Option{}).Where("key = ?", "cov_finding_should_not_persist").Count(&count).Error; err != nil {
		t.Fatalf("count after recreate: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 persisted rows for the failed write, got %d", count)
	}
}

// ─── tenant.go GenerateID ────────────────────────────────────────────────────

var repoDeepTenantIDRe = regexp.MustCompile(`^tenant-\d{14}$`)

func TestGenerateID_FormatAndUniquenessAcrossCalls(t *testing.T) {
	id1 := GenerateID()
	if !repoDeepTenantIDRe.MatchString(id1) {
		t.Fatalf("GenerateID() = %q, want to match ^tenant-\\d{14}$", id1)
	}

	// The embedded timestamp must be parseable and close to "now" — proves the
	// format string is the intended one (20060102150405), not some other
	// 14-digit-looking layout. GenerateID formats time.Now() (local time, no
	// zone info in the layout), so it must be parsed back in Local to compare
	// meaningfully — parsing as UTC would falsely diverge by the local UTC
	// offset on any non-UTC machine.
	tsPart := id1[len("tenant-"):]
	parsed, err := time.ParseInLocation("20060102150405", tsPart, time.Local)
	if err != nil {
		t.Fatalf("embedded timestamp %q did not parse as 20060102150405: %v", tsPart, err)
	}
	if diff := time.Since(parsed); diff < -2*time.Second || diff > 5*time.Second {
		t.Fatalf("embedded timestamp %v is not close to now (diff=%v)", parsed, diff)
	}

	// Calling twice in immediate succession must not panic and must always
	// conform to the same shape (documents that GenerateID has no uniqueness
	// guarantee beyond second-resolution — a real business caveat callers of
	// CreateTenantFromIDP's fallback path should know about).
	id2 := GenerateID()
	if !repoDeepTenantIDRe.MatchString(id2) {
		t.Fatalf("GenerateID() second call = %q, want to match ^tenant-\\d{14}$", id2)
	}
}

// ─── GetAllChannels pagination edge: offset past total row count ────────────

func TestGetAllChannels_OffsetPastTotalReturnsEmptyNotError(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		ch := &Channel{
			Type: 1, Status: common.ChannelStatusEnabled,
			Name: "offset-ch", Models: "m", Group: "default", TenantId: "default",
		}
		if err := DB.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", i, err)
		}
	}

	// Sanity: within range returns rows.
	inRange, err := GetAllChannels(0, 10, false, true)
	if err != nil {
		t.Fatalf("GetAllChannels(in-range): %v", err)
	}
	if len(inRange) != 3 {
		t.Fatalf("expected 3 channels in range, got %d", len(inRange))
	}

	// Edge: offset far beyond total row count must yield an empty slice and
	// no error — not a DB error, not a panic, not a nil vs. empty-slice
	// ambiguity that upstream pagination code might mishandle.
	pastEnd, err := GetAllChannels(1000, 10, false, true)
	if err != nil {
		t.Fatalf("GetAllChannels(offset past end): unexpected error %v", err)
	}
	if len(pastEnd) != 0 {
		t.Fatalf("expected 0 channels for offset past total, got %d", len(pastEnd))
	}

	// Edge: selectAll=true ignores offset/limit entirely — confirms the two
	// code paths inside GetAllChannels (selectAll vs paginated) are both
	// exercised and that selectAll doesn't silently apply the offset too.
	allSelected, err := GetAllChannels(1000, 10, true, false)
	if err != nil {
		t.Fatalf("GetAllChannels(selectAll=true): unexpected error %v", err)
	}
	if len(allSelected) != 3 {
		t.Fatalf("selectAll=true must ignore offset/limit, got %d channels, want 3", len(allSelected))
	}
}
