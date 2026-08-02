package app

// coverage_honesty_test.go — honesty lock for BMAD-03 (and the α9 half of
// BMAD-14).
//
// Claim under audit:
//
//	"newhub SC-6 test coverage app/ target ≥80% (current ~70%)."
//	"α9 CI thresholds 18/58/19 in go-ci.yml."
//
// HISTORY — the direction of this lock has been INVERTED (2026-08-02):
//
// It was written when ≥80% was aspirational and unreachable hermetically, to
// stop anyone raising the gate to match a doc without lifting real coverage.
// That job is done. CI has measured internal/app at 86.2% on main (run
// 30152927620) and on PR #67 (run 30447541676) — SC-6's ≥80% is MET, as are
// NFR-6.1's repo ≥60% (63.8%) and handler ≥50% (52.6%).
//
// The failure mode worth guarding therefore flipped. It is no longer "gate
// raised past reality"; it is "reality quietly falls back below the gate".
// Until 2026-08-02 the app line read 25 against a comment claiming 27.0% from
// 2026-05-31, so coverage on the package holding the quota/billing money path
// could have dropped SIXTY-ONE points with CI still printing PASS. The
// assertion below now holds the gate AT OR ABOVE 80 instead of below it.
//
// This is a doc/CI consistency lock. Its subject is the committed
// .github/workflows/go-ci.yml; its assertion is the literal check_pkg lines.
// Independent of any production Go code → not a tautology.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// findGoCIWorkflow walks up from the test's working directory to the repo root
// (where .github/workflows/go-ci.yml lives) and returns its contents. Tests run
// with CWD = the package dir, so we ascend until we find the workflow.
func findGoCIWorkflow(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, ".github", "workflows", "go-ci.yml")
		if data, err := os.ReadFile(candidate); err == nil {
			return string(data)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("go-ci.yml not found by walking up from CWD; skipping CI-gate honesty lock (cannot verify without the workflow file)")
	return ""
}

// checkPkgRe matches a line like:
//
//	check_pkg "internal/app"             18 "internal/app"
var checkPkgRe = regexp.MustCompile(`check_pkg\s+"([^"]+)"\s+(\d+)\s+"[^"]+"`)

// TestAppCoverageGate_HonestBaseline asserts the REAL gate values configured in
// CI. Changing a threshold in go-ci.yml without coming here — and without a
// measured before/after — breaks this lock and forces a conversation.
func TestAppCoverageGate_HonestBaseline(t *testing.T) {
	wf := findGoCIWorkflow(t)

	gates := map[string]int{}
	for _, m := range checkPkgRe.FindAllStringSubmatch(wf, -1) {
		v, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("parse threshold %q: %v", m[2], err)
		}
		gates[m[1]] = v
	}

	if len(gates) == 0 {
		t.Fatal("no check_pkg gate lines found in go-ci.yml — coverage gate may have been removed")
	}

	// Values track go-ci.yml. Re-ratcheted 2026-08-02 from 25/59/48 against
	// CI-measured actuals — app 86.2%, repo 63.8%, handler 52.6% (run
	// 30152927620 on main). Buffers: app -2.2pt, repo -1.8pt, handler -2.6pt
	// (handler kept widest for the baseline OAuth/JWKS test debt).
	want := map[string]int{
		"internal/app":             84,
		"internal/adapter/repo":    62,
		"internal/adapter/handler": 50,
	}
	for pkg, w := range want {
		got, ok := gates[pkg]
		if !ok {
			t.Errorf("no CI gate found for %q (expected a check_pkg line)", pkg)
			continue
		}
		if got != w {
			t.Errorf("CI gate for %q = %d, want %d — if this changed, reconcile the SC-6 doc claim too", pkg, got, w)
		}
	}

	// SC-6 / NFR-6.1 targets, now MET and locked so they cannot be given back.
	// The old form of this check asserted the opposite (app gate must stay
	// BELOW 80, because 80 was aspirational); see the inversion note in the
	// file header for why it flipped.
	sc6Targets := map[string]int{
		"internal/app":             80, // SC-6 / NFR-6.1
		"internal/adapter/repo":    60, // project-context.md coverage line
		"internal/adapter/handler": 50, // project-context.md coverage line
	}
	for pkg, target := range sc6Targets {
		if gates[pkg] < target {
			t.Errorf("CI gate for %q is %d, below the %d target that project-context.md records as MET — "+
				"lowering it silently re-opens a closed commitment; if coverage genuinely regressed, say so in the PR body",
				pkg, gates[pkg], target)
		}
	}

	// Sanity: the workflow must still self-describe these as baselines, not
	// targets — guards against a future edit that flips the comment's meaning.
	if !strings.Contains(wf, "CURRENT BASELINE") {
		t.Errorf("go-ci.yml no longer labels the thresholds as CURRENT BASELINE; the honest-baseline framing may have drifted")
	}
}
