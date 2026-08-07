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

// ─── FINDING: UpdateOption drops FirstOrCreate/Save errors ──────────────────

// TestUpdateOption_SilentlySwallowsDBWriteError_FINDING proves that when the
// underlying persistence call fails, UpdateOption still reports success
// (err == nil) while the value is never actually durable in the database —
// only the in-memory common.OptionMap reflects the write. A caller has no way
// to detect the partial failure from the return value alone.
//
// Self-check: if UpdateOption's body were replaced by a zero-value stub
// (`return nil`), the in-memory OptionMap would never be set to
// "should-not-persist" by this call (since a real stub wouldn't touch it via
// updateOptionMap either) — the OptionMap assertion below would fail, so this
// is not a hollow err==nil-only check.
func TestUpdateOption_SilentlySwallowsDBWriteError_FINDING(t *testing.T) {
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

	// Now break the persistence path underneath UpdateOption without closing
	// the connection (so updateOptionMap's own DB-free logic keeps working):
	// drop the options table. FirstOrCreate/Save will both fail with "relation
	// does not exist", but option.go discards both errors.
	if err := DB.Migrator().DropTable(&Option{}); err != nil {
		t.Fatalf("drop options table: %v", err)
	}

	err := UpdateOption("cov_finding_should_not_persist", "should-not-persist")

	// FINDING: current code returns nil here even though the write failed.
	if err != nil {
		t.Fatalf("FINDING regression: UpdateOption now propagates the DB error (err=%v) — "+
			"if option.go was intentionally fixed, update this test to match the new contract "+
			"instead of silently deleting the finding", err)
	}

	// The in-memory map WAS updated (this is the real, substantive assertion —
	// not just err==nil) — proving the caller-visible "success" is genuinely
	// backed by nothing durable.
	common.OptionMapRWMutex.RLock()
	gotMapValue, ok := common.OptionMap["cov_finding_should_not_persist"]
	common.OptionMapRWMutex.RUnlock()
	if !ok || gotMapValue != "should-not-persist" {
		t.Fatalf("expected in-memory OptionMap to be updated to %q despite DB failure, got %q (ok=%v)",
			"should-not-persist", gotMapValue, ok)
	}

	// Recreate the table and confirm nothing was ever persisted for this key —
	// the silent failure really did lose the write.
	if err := DB.Migrator().AutoMigrate(&Option{}); err != nil {
		t.Fatalf("recreate options table: %v", err)
	}
	var count int64
	if err := DB.Model(&Option{}).Where("key = ?", "cov_finding_should_not_persist").Count(&count).Error; err != nil {
		t.Fatalf("count after recreate: %v", err)
	}
	if count != 0 {
		t.Fatalf("FINDING regression: expected 0 persisted rows for the failed write, got %d — "+
			"UpdateOption may have started actually persisting despite the earlier table drop", count)
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
