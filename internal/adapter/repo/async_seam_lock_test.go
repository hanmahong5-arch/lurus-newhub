package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every fire-and-forget spawn in this package must go through AsyncGo.
//
// The seam only works if nothing bypasses it, and bypassing it is the easy
// mistake: gopool.Go is what the rest of the codebase reaches for, it compiles,
// it behaves identically in production, and the damage shows up somewhere else
// entirely — as a -race failure in whichever package's tests happen to swap
// common.RDB while the orphaned goroutine is still reading it. That is exactly
// how the hole this seam closes was found: a DATA RACE reported against
// internal/app, on a pull request that touched neither package.
//
// So the rule is checked here, at the only place where re-introducing it is
// cheap to notice.
func TestNoDirectGopoolSpawnsInRepo(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// async.go holds the seam itself: it is the one legitimate reference.
		if name == "async.go" {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(src), "gopool.Go(") {
			offenders = append(offenders, name)
		}
	}

	if len(offenders) > 0 {
		t.Errorf("these files spawn through gopool directly instead of AsyncGo, "+
			"so their goroutines cannot be made synchronous by a test binary and "+
			"will read package globals after the spawning test has returned: %v\n"+
			"Use AsyncGo (see async.go for why).", offenders)
	}
}
