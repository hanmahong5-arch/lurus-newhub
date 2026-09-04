package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A package whose tests swap common.RDB or common.RedisEnabled must force the
// repo async seam inline, or its test binary can race itself.
//
// The mechanism is always the same and always arrives from somewhere else:
// the test swaps the global, calls something that reaches repo, repo spawns a
// detached cache writer, the test returns, t.Cleanup restores the global and
// closes the client, and the orphaned goroutine reads it. The report names the
// package that did the swap, not the one that spawned, which is why this took
// two separate CI failures on two unrelated pull requests to pin down.
//
// Adding the seam package by package as each one goes red is whack-a-mole with
// a very slow feedback loop — the failures are scheduler-dependent and can hide
// for days. This checks the property directly instead: if you swap those
// globals, you owe a TestMain that assigns repo.AsyncGo.
func TestPackagesSwappingRedisGlobalsForceTheSeam(t *testing.T) {
	root := filepath.Join("..", "..")

	swaps := map[string]bool{}  // package dir -> swaps a redis global in tests
	forces := map[string]bool{} // package dir -> forces repo.AsyncGo inline

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(filepath.Clean(path))
		if readErr != nil {
			return readErr
		}
		text := string(src)
		dir := filepath.Dir(path)

		if strings.Contains(text, "common.RDB =") || strings.Contains(text, "common.RedisEnabled =") {
			swaps[dir] = true
		}
		// This package assigns the seam by its bare name; everyone else
		// qualifies it.
		if strings.Contains(text, "repo.AsyncGo =") ||
			(dir == filepath.Join("..", "..", "adapter", "repo") && strings.Contains(text, "AsyncGo =")) {
			forces[dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if len(swaps) == 0 {
		// Guards against the scan silently matching nothing — a green run that
		// checked zero packages is the failure mode this whole file exists to
		// avoid, and a refactor of the helper names would produce exactly that.
		t.Fatal("no package was found swapping common.RDB / common.RedisEnabled in tests; " +
			"the scan is broken, not the codebase")
	}

	var missing []string
	for dir := range swaps {
		if !forces[dir] {
			missing = append(missing, dir)
		}
	}
	if len(missing) > 0 {
		t.Errorf("these packages swap common.RDB / common.RedisEnabled in tests but never "+
			"force repo.AsyncGo inline, so a detached cache writer can outlive the test that "+
			"spawned it and race the restore: %v\n"+
			"Add a TestMain assigning repo.AsyncGo = func(f func()) { f() } — see "+
			"internal/adapter/repo/async.go.", missing)
	}
}
