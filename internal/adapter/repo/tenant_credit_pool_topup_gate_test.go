package repo

// tenant_credit_pool_topup_gate_test.go — structural gate: no production code
// may credit a tenant credit pool through the non-idempotent TopupPool.
//
// Why a gate and not just a comment: the defect it guards was one call. The
// admin topup endpoint debited the platform wallet with an Idempotency-Key
// (deduped upstream, so the money left exactly once) and then credited the
// pool with TopupPool, which dedupes nothing — a retry of a successful topup
// produced one debit and two credits. The credit now goes through
// FundPoolIdempotentWithReason, which books the same key into
// credit_pool_fund_events under UNIQUE(tenant_id, event_id).
//
// WHAT THIS GATE SCANS: every non-test .go file in the module (excluding
// web/, vendor/, .git/ and dot-directories), parsed with go/parser, looking
// for call expressions whose callee is named TopupPool — both the qualified
// spelling (repo.TopupPool(...), under any import alias) and the bare
// same-package spelling. Being AST-based it cannot mistake a comment or a
// string for a call, which is how an earlier gate in this repo overcounted.
//
// WHAT IT STRUCTURALLY CANNOT SEE — every one of these is a way to reintroduce
// the same double-credit with this gate still green:
//
//   - A method value or an indirection: f := repo.TopupPool; f(...), or the
//     function stored in a struct field / map and called through it. The
//     callee at the call site is not named TopupPool.
//   - Any OTHER non-idempotent credit. topupPoolInTx, CreditPoolAdjustment,
//     ResetDuePools and a hand-written "UPDATE tenant_credit_pools SET
//     current_balance = current_balance + ?" all move the same balance and are
//     invisible here. This gate pins one door, not the room.
//   - A bad key passed to the idempotent function. FundPoolIdempotentWithReason
//     called with a freshly generated event id per request dedupes exactly
//     nothing, and this gate would call that fine.
//   - Production code living in a _test.go file (none does today, but the
//     exclusion is by filename, not by build role).
//   - Any file it fails to parse. Such a file is reported as a FAILURE rather
//     than skipped: a scan that could not read a file is not evidence that the
//     file is clean.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRootForGate resolves the repository root from this package's
// directory and proves it by finding go.mod there.
func moduleRootForGate(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q has no go.mod (%v) — the gate would scan the wrong tree and pass vacuously", root, err)
	}
	return root
}

func TestNoProductionCallerOfNonIdempotentTopupPool(t *testing.T) {
	root := moduleRootForGate(t)

	type site struct {
		file string
		line int
		expr string
	}
	var sites []site
	scanned := 0

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			// Never the root itself: a checkout may live in a dot-directory
			// (e.g. a worktree named .wt-*), and skipping it scans nothing.
			if path != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			switch name {
			case "web", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// Not evidence of absence — say so loudly.
			t.Errorf("gate could not parse %s: %v", path, perr)
			return nil
		}
		scanned++

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name, expr string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
				if pkg, ok := fn.X.(*ast.Ident); ok {
					expr = pkg.Name + "." + name
				} else {
					expr = name
				}
			case *ast.Ident:
				name, expr = fn.Name, fn.Name
			default:
				return true
			}
			if name == "TopupPool" {
				pos := fset.Position(call.Pos())
				rel, rerr := filepath.Rel(root, pos.Filename)
				if rerr != nil {
					rel = pos.Filename
				}
				sites = append(sites, site{file: filepath.ToSlash(rel), line: pos.Line, expr: expr})
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", root, walkErr)
	}

	// A scan that matched nothing because it looked at nothing is not a pass.
	if scanned < 100 {
		t.Fatalf("gate parsed only %d production .go files under %s — it is not scanning the tree it claims to", scanned, root)
	}

	for _, s := range sites {
		t.Errorf("%s:%d calls %s — TopupPool is not idempotent, so a retry of a wallet-deduped "+
			"Idempotency-Key credits the pool twice against one debit. Use "+
			"repo.FundPoolIdempotentWithReason with the same key the wallet debit used.",
			s.file, s.line, s.expr)
	}
}
