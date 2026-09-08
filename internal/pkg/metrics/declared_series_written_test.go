package metrics

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// declared_series_written_test.go — the M3 gate: every promauto.New* series
// declared in this package must have a real production writer. Before this,
// channel_health / channel_consecutive_errors / channel_errors_total were
// declared, exported, fully wired to Record*/Set* helpers with doc comments —
// and had zero non-test callers anywhere in the repo (grep across internal/
// turned up nothing). A scraper reading /metrics could not tell "nobody wrote
// this yet" from "this is being tracked and nothing has happened" — the
// series existing at all implied the latter.
//
// A var is "written" if either:
//  1. some non-test .go file OUTSIDE this package references it directly
//     (metrics.<Var>), or
//  2. some exported func declared in this package's own non-test files
//     references it, AND that func is itself called from a non-test .go file
//     outside this package (metrics.<Func>().
//
// This is a structural/textual check, not a full import-resolution — it
// assumes the repo-wide convention (confirmed by grep) of importing this
// package unaliased and referencing it as `metrics.X`.

// declaredMetricVarRe matches a package-level var initialised directly from
// promauto.New* — e.g. `RequestsTotal = promauto.NewCounterVec(`.
var declaredMetricVarRe = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*promauto\.New\w+\(`)

// packageNonTestGoFiles returns the .go files directly in dir, excluding
// _test.go files.
func packageNonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// declaredMetricVars scans this package's own non-test files for every
// promauto-declared series name.
func declaredMetricVars(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, f := range packageNonTestGoFiles(t, ".") {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range declaredMetricVarRe.FindAllStringSubmatch(string(body), -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// exportedFuncBodies returns a map of exported top-level function name -> its
// source text (signature through closing brace), for this package's own
// non-test files. Relies on gofmt convention: a top-level function's closing
// brace is a lone "}" at column 0, which nothing inside a gofmt'd function
// body ever is.
var topLevelFuncRe = regexp.MustCompile(`^func\s+([A-Z]\w*)\s*\(`)

func exportedFuncBodies(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range packageNonTestGoFiles(t, ".") {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		lines := strings.Split(string(body), "\n")
		for i := 0; i < len(lines); i++ {
			m := topLevelFuncRe.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			name := m[1]
			j := i + 1
			for j < len(lines) && strings.TrimRight(lines[j], "\r") != "}" {
				j++
			}
			if j >= len(lines) {
				j = len(lines) - 1
			}
			out[name] = strings.Join(lines[i:j+1], "\n")
			i = j
		}
	}
	return out
}

// repoGoFilesOutsidePackage walks the repo root for non-test .go files that
// are NOT inside this metrics package directory.
func repoGoFilesOutsidePackage(t *testing.T, root, pkgDir string) []string {
	t.Helper()
	absPkg, err := filepath.Abs(pkgDir)
	if err != nil {
		t.Fatalf("abs pkgDir: %v", err)
	}
	var out []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip vendor / node_modules / .git / web (JS, not relevant) — pure
			// perf, correctness doesn't depend on it since none of them contain
			// Go source that could plausibly reference these vars.
			switch info.Name() {
			case "vendor", "node_modules", ".git", "web":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		absDir, absErr := filepath.Abs(filepath.Dir(path))
		if absErr == nil && absDir == absPkg {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// TestDeclaredSeriesHaveAProductionWriter is the gate described above.
func TestDeclaredSeriesHaveAProductionWriter(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	vars := declaredMetricVars(t)
	if len(vars) == 0 {
		t.Fatal("found zero promauto-declared vars — the scan is measuring nothing")
	}
	funcs := exportedFuncBodies(t)
	externalFiles := repoGoFilesOutsidePackage(t, root, pkgDir)

	externalText := make([]string, 0, len(externalFiles))
	for _, f := range externalFiles {
		body, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		externalText = append(externalText, string(body))
	}
	joinedExternal := strings.Join(externalText, "\n")

	// Which exported funcs in this package are themselves called from outside
	// the package (metrics.FuncName().
	calledExternally := map[string]bool{}
	for name := range funcs {
		if regexp.MustCompile(`\bmetrics\.` + regexp.QuoteMeta(name) + `\(`).MatchString(joinedExternal) {
			calledExternally[name] = true
		}
	}

	for _, v := range vars {
		directRef := regexp.MustCompile(`\bmetrics\.` + regexp.QuoteMeta(v) + `\b`)
		if directRef.MatchString(joinedExternal) {
			continue // written directly from outside the package
		}

		// Or written via a helper in this package that references the var and
		// is itself called from outside.
		writtenViaHelper := false
		fieldRef := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\b`)
		for name, body := range funcs {
			if !calledExternally[name] {
				continue
			}
			if fieldRef.MatchString(body) {
				writtenViaHelper = true
				break
			}
		}
		if writtenViaHelper {
			continue
		}

		t.Errorf("%s is declared in internal/pkg/metrics but has no production writer: "+
			"no non-test file outside this package references metrics.%s directly, and no "+
			"exported helper that touches it is called from outside the package either. A "+
			"declared, undashboarded, unwritten series reads to a scraper as tracked data "+
			"that just hasn't happened yet — delete it, or wire a real writer.", v, v)
	}
}
