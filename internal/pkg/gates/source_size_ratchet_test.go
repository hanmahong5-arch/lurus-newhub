package gates

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// goSourceSizeCeilings is the per-file line ceiling for every non-test Go
// file that was above sourceSizeThreshold on 2026-09-19 (cycle 12 W), each set
// to the file's line count on that day. The gate is a ratchet: a listed file
// may shrink (and the ceiling should then be lowered in the same change), it
// may not grow past its ceiling, and no unlisted file may cross the threshold.
// It is not a review of the listed files — internal/adapter/middleware/
// oidc_auth.go is not better for being frozen at its size — it is what stops
// the next cycle's decomposition from being undone quietly.
var goSourceSizeCeilings = map[string]int{
	"internal/adapter/handler/channel-test.go":          850,
	"internal/adapter/handler/channel.go":               2497,
	"internal/adapter/handler/deployment.go":            810,
	"internal/adapter/handler/internal_api_ext.go":      1057,
	"internal/adapter/handler/oauth.go":                 1046,
	"internal/adapter/handler/relay.go":                 1014,
	"internal/adapter/middleware/auth.go":               930,
	"internal/adapter/middleware/oidc_auth.go":          1353,
	"internal/adapter/provider/claude/relay-claude.go":  940,
	"internal/adapter/provider/common/relay_info.go":    893,
	"internal/adapter/provider/gemini/relay-gemini.go":  1427,
	"internal/adapter/repo/channel.go":                  1226,
	"internal/adapter/repo/log.go":                      1084,
	"internal/adapter/repo/option.go":                   985,
	"internal/adapter/repo/token.go":                    807,
	"internal/adapter/repo/user.go":                     1235,
	"internal/app/convert.go":                           1304,
	"internal/app/quota.go":                             1450,
	"internal/pkg/common/identity_client.go":            834,
	"internal/pkg/dto/openai_request.go":                1020,
	"internal/pkg/setting/ratio_setting/model_ratio.go": 944,
}

// sourceSizeThreshold is the line count above which a non-test Go file needs
// a row in goSourceSizeCeilings.
const sourceSizeThreshold = 800

// sourceSizeSkipDirs are never walked: dependencies, build output, the
// planning artefacts, and git itself.
var sourceSizeSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "vendor": true,
	"_bmad-output": true, "coverage": true,
}

func repoRootForGates(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found at %s (was this test file moved?): %v", root, err)
	}
	return root
}

func countLines(t *testing.T, p string) int {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if len(data) == 0 {
		return 0
	}
	n := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// TestGoSourceSizeRatchet fails when a listed file grows past its ceiling or
// an unlisted non-test Go file crosses sourceSizeThreshold, and reports the
// listed files that shrank so their ceilings can be lowered. Mutation that
// proves it: append 50 lines to any file in goSourceSizeCeilings.
func TestGoSourceSizeRatchet(t *testing.T) {
	root := repoRootForGates(t)
	scanned := 0
	seen := map[string]bool{}
	var over, unlisted, shrank []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if sourceSizeSkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root))
		rel = strings.TrimPrefix(rel, "/")
		scanned++
		n := countLines(t, p)
		ceiling, listed := goSourceSizeCeilings[rel]
		switch {
		case listed:
			seen[rel] = true
			if n > ceiling {
				over = append(over, fmt.Sprintf("%s: %d lines, ceiling %d (+%d)", rel, n, ceiling, n-ceiling))
			} else if n < ceiling {
				shrank = append(shrank, fmt.Sprintf("%s: %d lines, ceiling %d — lower the ceiling", rel, n, ceiling))
			}
		case n > sourceSizeThreshold:
			unlisted = append(unlisted, fmt.Sprintf("%s: %d lines and not in goSourceSizeCeilings", rel, n))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if scanned < 500 {
		t.Fatalf("scanned only %d non-test Go files — the walk is broken, not the sources", scanned)
	}
	for rel := range goSourceSizeCeilings {
		if !seen[rel] {
			over = append(over, rel+": listed in goSourceSizeCeilings but not found — remove the row")
		}
	}
	sort.Strings(over)
	sort.Strings(unlisted)
	sort.Strings(shrank)
	if len(shrank) > 0 {
		t.Logf("files below their ceiling (lower them):\n  %s", strings.Join(shrank, "\n  "))
	}
	if len(over) > 0 || len(unlisted) > 0 {
		t.Fatalf("Go source size ratchet:\n  %s\n\nA file may shrink, not grow: split it (pure move, same package) or raise its row here with a reason in the commit.",
			strings.Join(append(over, unlisted...), "\n  "))
	}
}
