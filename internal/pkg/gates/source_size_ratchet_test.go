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
// file above sourceSizeThreshold, each set to the file's line count when it
// was last measured. The gate is a two-sided ratchet: a listed file may not
// grow past its ceiling, a listed file that is BELOW its ceiling fails too
// (with the replacement line to paste), and no unlisted file may cross the
// threshold. It is not a review of the listed files — internal/adapter/
// middleware/oidc_auth.go is not better for being frozen at its size — it is
// what stops the next cycle's decomposition from being undone quietly.
//
// The shrink side used to be a t.Logf, which nobody read: cycle 12's numbers
// were stale within a day. Since cycle 13 (plan section 2, "只许缩不许长") a
// shrink is a failure, so the win is locked in by the same change that won
// it, and growth has to be paid for with a pure move into a sibling file in
// the same package rather than a quiet +40 here.
//
// Measured 2026-09-20 (cycle-13 W), after the moves that paid for this
// cycle's growth: handler/channel_key.go, handler/oauth_state.go,
// middleware/auth_token_context.go, middleware/oidc_session_fallback.go,
// repo/log_billable.go, repo/option_validation.go, repo/token_messages.go,
// repo/user_edit.go, app/quota_pool.go.
var goSourceSizeCeilings = map[string]int{
	"internal/adapter/handler/channel-test.go":         850,
	"internal/adapter/handler/channel.go":              2459,
	"internal/adapter/handler/deployment.go":           810,
	"internal/adapter/handler/internal_api_ext.go":     1057,
	"internal/adapter/handler/oauth.go":                978,
	"internal/adapter/handler/relay.go":                1014,
	"internal/adapter/middleware/auth.go":              913, // +8 (cycle-13 hand-finish): the SDK self-heal arm now records that it registers no session-registry row; comment only
	"internal/adapter/middleware/oidc_auth.go":         1206,
	"internal/adapter/provider/claude/relay-claude.go": 940,
	"internal/adapter/provider/common/relay_info.go":   893,
	"internal/adapter/provider/gemini/relay-gemini.go": 1427,
	"internal/adapter/repo/channel.go":                 1226,
	"internal/adapter/repo/log.go":                     1054,
	"internal/adapter/repo/option.go":                  948,
	"internal/adapter/repo/token.go":                   794,
	"internal/adapter/repo/user.go":                    1206,
	"internal/app/convert.go":                          1304,
	"internal/app/quota.go":                            1386, // +10 (cycle-13 hand-finish + the 402 ASCII fix): the TokenId > 0 guard and why, plus three lines saying why the pre-consume rejection formats ASCII
	"internal/pkg/common/identity_client.go":           834,
	"internal/pkg/dto/openai_request.go":               1020,
	// NEW ROW, not a raise: metrics.go crossed the 800 threshold in cycle 13
	// when the observability lane added the counters the fourteen new netdata
	// alarms are bound to. A pure move was not available to the wiring pass —
	// internal/pkg/metrics belongs to that lane and splitting its central
	// registry file is its call, not a wiring-step side effect. The row
	// records the measured count so the next growth has to argue for itself.
	"internal/pkg/metrics/metrics.go":                   823, // +1 (cycle-13 hand-finish): the log-retention series comment now says which legs write and when the label is absent
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

// TestGoSourceSizeRatchet fails when a listed file grows past its ceiling,
// when a listed file is below its ceiling (the win has to be recorded in the
// same change that won it, or the table drifts back into permission to grow),
// or when an unlisted non-test Go file crosses sourceSizeThreshold.
//
// Mutations that prove it: raise any row by 1 — the shrink path fails with
// the exact replacement line; lower any row by 1 — the over path fails.
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
				shrank = append(shrank, fmt.Sprintf("%s: %d lines, ceiling %d — replace that row with:\n      %q: %d,",
					rel, n, ceiling, rel, n))
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
		t.Errorf("files below their ceiling — lower the row in the same change that shrank the file, or the ratchet quietly becomes permission to grow back:\n  %s",
			strings.Join(shrank, "\n  "))
	}
	if len(over) > 0 || len(unlisted) > 0 {
		t.Fatalf("Go source size ratchet:\n  %s\n\nA file may shrink, not grow: split it (pure move, same package) or raise its row here with a reason in the commit.",
			strings.Join(append(over, unlisted...), "\n  "))
	}
}
