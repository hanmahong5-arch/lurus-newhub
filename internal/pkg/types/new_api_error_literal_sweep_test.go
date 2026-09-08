package types

// new_api_error_literal_sweep_test.go — L3-CONTRACT-TAXONOMY item 12: a Go
// test automating the plan's acceptance grep
//
//	grep -rn '"new_api_error"' internal cmd pkg --include=*.go | grep -v _test | grep -v types/error.go
//
// so a future call site that re-introduces the retired ErrorTypeNewAPIError
// literal into a wire body — instead of using WireErrorType/ErrorCode — fails
// CI, not just a one-time cross-repo grep in a report.
//
// Two exclusions, both intentional and both documented in
// doc/product-integration-guide.md §B:
//   - this file's own package (internal/pkg/types/error.go) declares and
//     documents ErrorTypeNewAPIError itself;
//   - internal/adapter/middleware/utils.go's abortWithMidjourneyMessage: the
//     Midjourney wire's official envelope has no stable code field, so that
//     one JSON body keeps stamping the literal on purpose (spec item 2).
//
// Comment-only lines (trimmed line starts with "//") are not counted — a
// prose reference to the retired literal (there are several, explaining why
// it is retired) is not the code regression this test guards against.

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const newAPIErrorLiteral = `"new_api_error"`

// documentedNewAPIErrorLiteralExceptionFiles lists the only non-test,
// non-types/error.go source files allowed to contain the literal on a
// non-comment line.
var documentedNewAPIErrorLiteralExceptionFiles = map[string]bool{
	filepath.FromSlash("internal/adapter/middleware/utils.go"): true,
}

func TestNoNewAPIErrorLiteralOutsideTypesPackageAndDocumentedException(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")

	var (
		filesScanned int
		violations   []string
	)
	perRootScanned := map[string]int{}

	roots := []string{"internal", "cmd", "pkg"}
	for _, root := range roots {
		start := filepath.Join(repoRoot, root)
		err := filepath.Walk(start, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			name := info.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if rel == "internal/pkg/types/error.go" {
				return nil
			}
			filesScanned++
			perRootScanned[root]++

			f, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = f.Close() }()

			exception := documentedNewAPIErrorLiteralExceptionFiles[filepath.FromSlash(rel)]
			scanner := bufio.NewScanner(f)
			lineNo := 0
			for scanner.Scan() {
				lineNo++
				line := scanner.Text()
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue // prose, not a code literal
				}
				if strings.Contains(line, newAPIErrorLiteral) {
					if exception {
						continue
					}
					violations = append(violations,
						rel+":"+strconv.Itoa(lineNo)+": "+trimmed)
				}
			}
			return scanner.Err()
		})
		if err != nil {
			t.Fatalf("walk %s: %v", start, err)
		}
	}

	if filesScanned == 0 {
		t.Fatal("scanned zero non-test .go files under internal/cmd/pkg — the walk is broken, not the codebase clean")
	}
	// requiredRoots is a fixed list independent of the `roots` slice the walk
	// above actually iterates: if a future edit shrinks `roots` back to just
	// {"internal", "cmd"} (dropping "pkg" — item 12's regression), the walk
	// loop simply stops visiting pkg/ and perRootScanned["pkg"] silently stays
	// at its zero value; checking against this separate, hardcoded list is
	// what turns that drop into a loud failure instead of a vacuous pass.
	requiredRoots := []string{"internal", "cmd", "pkg"}
	for _, root := range requiredRoots {
		if perRootScanned[root] == 0 {
			t.Fatalf("scanned zero non-test .go files under required root %q — the sweep no longer covers this directory (regression: item 12 requires internal, cmd, and pkg all covered — pkg/ionet has Go sources)", root)
		}
	}
	t.Logf("new_api_error literal sweep: %d file(s) scanned (%v)", filesScanned, perRootScanned)

	if len(violations) > 0 {
		t.Errorf("%d non-test source line(s) outside types/error.go and the documented Midjourney exception still carry the retired \"new_api_error\" literal:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
