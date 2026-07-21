package app

// coverage_honesty_test.go — honesty lock for BMAD-03 (and the α9 half of
// BMAD-14).
//
// Claim under audit:
//
//	"newhub SC-6 test coverage app/ target ≥80% (current ~70%)."
//	"α9 CI thresholds 18/58/19 in go-ci.yml."
//
// CLAUDE.md §4.1①/⑥: the ≥80% figure is ASPIRATIONAL — it is not the gate that
// actually runs in CI, and go-ci.yml says so in a comment ("the plan's 80/60/50
// targets aren't reachable in hermetic tests today"). The number that gates a
// merge is 18 (app) / 58 (repo) / 19 (handler). This test reads the real
// workflow file and locks those values, so the doc can be reconciled to the
// gate rather than the gate being silently raised to match a doc.
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

	// Values track go-ci.yml. app/repo ratcheted on main 2026-05-31 (actuals
	// 27.0% / 60.9%). handler ratcheted 2026-07-20 from 19->48 after measuring
	// hermetic 52.8% on this base (was ~32pt stale at "20.0%/19"); lifted by the
	// cover_uplift_{analytics,presets,internal} handler tests.
	want := map[string]int{
		"internal/app":             25,
		"internal/adapter/repo":    59,
		"internal/adapter/handler": 48,
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

	// §4.1①: the aspirational 80 is NOT the real app gate. Lock the inequality
	// so the divergence is explicit and cannot be papered over.
	const aspirationalSC6 = 80
	if gates["internal/app"] >= aspirationalSC6 {
		t.Errorf("app CI gate is %d, which already meets the aspirational %d — update this lock + the SC-6 doc to stop calling 80 'aspirational'",
			gates["internal/app"], aspirationalSC6)
	}

	// Sanity: the workflow must still self-describe these as baselines, not
	// targets — guards against a future edit that flips the comment's meaning.
	if !strings.Contains(wf, "CURRENT BASELINE") {
		t.Errorf("go-ci.yml no longer labels the thresholds as CURRENT BASELINE; the honest-baseline framing may have drifted")
	}
}
