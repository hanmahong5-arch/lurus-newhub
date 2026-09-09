package middleware

// abort_code_structural_test.go — L3-CONTRACT-TAXONOMY's closing lock: every
// abortWithOpenAiMessage call site in this package must pass a machine code
// (the 4th argument), not leave it to the "" default. A go/ast scan over the
// real source files, not a hand-maintained list, so a future call site added
// without a code fails CI the moment it lands rather than silently shipping
// error.code:"" on the wire.
//
// The >40-sites / >5-files floor is a scanner-honesty guard: if a refactor
// ever collapses these call sites into a helper (or the scan itself breaks
// and silently walks zero files), this test must fail loudly instead of
// vacuously passing on an empty set. Baseline at HEAD d1351475 was 39 call
// sites across 6 files (auth.go, business_rate_limit.go,
// concurrency_limit.go, distributor.go, jimeng_adapter.go,
// model-rate-limit.go; utils.go only defines the helper, it never calls
// itself, so it does not count toward filesTouched). L6 raised the floor to
// >40 after wiring the memory-backend user rate limiter's two reject
// branches through abortWithOpenAiMessage (model-rate-limit.go) — they used
// to abort with a bare status and no body; a sibling lane added one more
// site the same cycle, so the count on this tree today is 39 + 2 (L6) + 1
// (sibling) = 42. Do not restate a specific number here again — it drifts
// every time any lane in this package adds a site; the assertion below is
// the source of truth.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestAbortWithOpenAiMessage_EveryCallSiteCarriesACode(t *testing.T) {
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	fset := token.NewFileSet()
	var (
		sitesScanned int
		filesTouched = map[string]bool{}
		violations   []string
	)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "abortWithOpenAiMessage" {
				return true
			}
			// Skip the function's own declaration signature — ast.Inspect
			// only visits CallExpr nodes, so a *ast.FuncDecl for
			// abortWithOpenAiMessage itself never matches here; this branch
			// only ever sees call sites.
			sitesScanned++
			filesTouched[name] = true
			if len(call.Args) < 4 {
				pos := fset.Position(call.Pos())
				violations = append(violations, pos.String())
			}
			return true
		})
	}

	if sitesScanned == 0 {
		t.Fatal("scanner found zero abortWithOpenAiMessage call sites — the scan is broken, not the codebase clean")
	}
	if sitesScanned <= 40 {
		t.Fatalf("sitesScanned = %d, want > 40 (scanner-honesty floor; today's baseline is 42)", sitesScanned)
	}
	if len(filesTouched) <= 5 {
		t.Fatalf("filesTouched = %d, want > 5 (scanner-honesty floor; today's baseline is 6)", len(filesTouched))
	}
	t.Logf("abortWithOpenAiMessage: %d call sites scanned across %d files", sitesScanned, len(filesTouched))

	if len(violations) > 0 {
		t.Errorf("%d abortWithOpenAiMessage call site(s) missing the code argument (< 4 args):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// TestBizAndConcurrencyLimitErrorCodesMatchTypesPackage is the L3-CONTRACT-TAXONOMY
// residual (round 2) item 5 lock: business_rate_limit.go's bizRateLimitErrorCode
// and concurrency_limit.go's concurrencyLimitErrorCode must equal
// types.ErrorCodeBusinessRateLimitExceeded / types.ErrorCodeConcurrencyLimitExceeded
// so they cannot drift from the canonical ErrorCode constants that
// openapi_contract_lock_test.go's lock (e) cross-checks against relay.json.
// The test pins value equality only; it does not inspect how the local
// constants are declared.
func TestBizAndConcurrencyLimitErrorCodesMatchTypesPackage(t *testing.T) {
	if bizRateLimitErrorCode != string(types.ErrorCodeBusinessRateLimitExceeded) {
		t.Errorf("bizRateLimitErrorCode = %q, want types.ErrorCodeBusinessRateLimitExceeded = %q",
			bizRateLimitErrorCode, types.ErrorCodeBusinessRateLimitExceeded)
	}
	if concurrencyLimitErrorCode != string(types.ErrorCodeConcurrencyLimitExceeded) {
		t.Errorf("concurrencyLimitErrorCode = %q, want types.ErrorCodeConcurrencyLimitExceeded = %q",
			concurrencyLimitErrorCode, types.ErrorCodeConcurrencyLimitExceeded)
	}
}
