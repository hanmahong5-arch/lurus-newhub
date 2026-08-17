package app

// coverage_honesty_test.go — honesty lock for BMAD-03 (and the α9 half of
// BMAD-14).
//
// Claim under audit:
//
//	"newhub SC-6 test coverage app/ target ≥80% (current ~70%)."
//	"α9 CI thresholds 18/58/19 in go-ci.yml."
//
// CLAUDE.md §4.1①/⑥: the ≥80% figure used to be ASPIRATIONAL — it was not the
// gate that actually ran in CI, and go-ci.yml said so ("the plan's 80/60/50
// targets aren't reachable in hermetic tests today"). This test reads the real
// workflow file and locks the literal values, so the doc gets reconciled to the
// gate rather than the gate being silently raised to match a doc.
//
// 2026-08-09: the divergence closed in the honest direction. The 2026-07-25
// coverage corpus (PR #72) landed and CI measured app 88.4% / repo 64.3% /
// handler 69.1% on the merge tree, so the gates were ratcheted 25/59/48 →
// 84/62/64 and the SC-6 doc claims were corrected in the same commit. The
// inequality below therefore flipped: the lock now guards against someone
// dropping the app gate back under 80 while the docs keep claiming the target
// is met.
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
// CI, not the aspirational 80. If anyone bumps the gate toward 80 without
// lifting actual coverage, this lock breaks and forces a conversation.
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

	// Values track go-ci.yml. Ratcheted 2026-08-17 from 84/62/64 against actuals
	// CI itself printed once coverage-gate was given a real PostgreSQL
	// (run 32052999168): app 89.4% / repo 80.5% / handler 69.1%, minus a
	// 3.4 / 3.5 / 5.1pt buffer.
	//
	// repo moved 62 → 77 because its old "hermetic ceiling" of 64.3% was not a
	// ceiling at all — it was the PG-gated tests silently skipping for want of
	// TEST_POSTGRES_DSN. 18.5 points of real coverage could have been lost
	// without this gate noticing.
	//
	// History: 18/58/19 at α9 → 25/59/19 (2026-05-31) → handler 48 (2026-07-20)
	// → 84/62/64 (2026-08-09, the 232-file corpus) → 86/77/64 (2026-08-17).
	want := map[string]int{
		"internal/app":             86,
		"internal/adapter/repo":    77,
		"internal/adapter/handler": 64,
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

	// §4.1①, inverted 2026-08-09: SC-6's ≥80% is now the *enforced* app gate,
	// not an aspiration the CI quietly undershot. prd.md SC-6 and
	// doc/uat-handbook.md §3/§4 were updated to say "met" in the same commit, so
	// dropping the gate back under 80 without reverting those claims re-opens
	// exactly the doc/CI divergence this file exists to prevent.
	const targetSC6 = 80
	if gates["internal/app"] < targetSC6 {
		t.Errorf("app CI gate is %d, below the SC-6 target %d that prd.md and doc/uat-handbook.md now record as MET — either restore the gate or revert those doc claims in the same change",
			gates["internal/app"], targetSC6)
	}

	// Sanity: the workflow must still self-describe these as baselines, not
	// targets — guards against a future edit that flips the comment's meaning.
	if !strings.Contains(wf, "CURRENT BASELINE") {
		t.Errorf("go-ci.yml no longer labels the thresholds as CURRENT BASELINE; the honest-baseline framing may have drifted")
	}
}
