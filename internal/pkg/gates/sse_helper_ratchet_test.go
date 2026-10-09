package gates

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// privateSSEHelperCeiling is how many test files still declare their own SSE
// body builder or parser instead of using internal/testkit/fakeupstream
// (SSEData / SSEEvents / SSEResponse, or a live fakeupstream.NewTest server).
//
// Each copy is a private model of a vendor's stream, and private models are
// how the relay ended up tested against shapes no vendor sends. The existing
// ones are not rewritten in bulk; they go when their file is next touched.
// The ceiling is two-sided like the source-size ratchet: above it fails (a new
// copy), below it fails too, with the number to write here, so a removal is
// locked in by the change that made it.
//
// Measured 2026-10-03 (acceptance suite, phase 1): 12.
const privateSSEHelperCeiling = 12

// A helper is a top-level func in a _test.go file whose name says SSE in
// camel or snake case (sseBody, crossWireSSE, prov_x_sseBody, xaiSSEBody).
// Case-sensitive on purpose: "assert" and "Session" must not match.
var privateSSEHelperName = regexp.MustCompile(`^func (?:\([^)]*\) )?([A-Za-z0-9_]*(?:^sse[A-Z]|SSE|_sse[A-Z]|Sse[A-Z])[A-Za-z0-9_]*|sse[A-Z][A-Za-z0-9_]*)\(`)

func TestPrivateSSEHelpersOnlyShrink(t *testing.T) {
	root := repoRootForGates(t)
	var found []string
	scanned := 0
	err := filepath.Walk(filepath.Join(root, "internal"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if sourceSizeSkipDirs[info.Name()] || info.Name() == "testkit" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		scanned++
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		rel := filepath.ToSlash(strings.TrimPrefix(p, root))
		for n := 1; sc.Scan(); n++ {
			m := privateSSEHelperName.FindStringSubmatch(sc.Text())
			if m == nil || strings.HasPrefix(m[1], "Test") {
				continue
			}
			found = append(found, rel+":"+itoa(n)+": "+m[1])
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 500 {
		t.Fatalf("scanned only %d test files under internal/ — the walk is broken", scanned)
	}
	sort.Strings(found)
	switch {
	case len(found) > privateSSEHelperCeiling:
		t.Errorf("%d private SSE helpers, ceiling %d — use internal/testkit/fakeupstream (SSEData/SSEEvents/SSEResponse) instead of declaring another:\n%s",
			len(found), privateSSEHelperCeiling, strings.Join(found, "\n"))
	case len(found) < privateSSEHelperCeiling:
		t.Errorf("%d private SSE helpers, below the ceiling %d — lock the win in: set privateSSEHelperCeiling = %d",
			len(found), privateSSEHelperCeiling, len(found))
	}
}
