package middleware

// wire_message_language_gate_test.go — cycle13 L3's structural lock for the
// "API error.message is English-only" contract (cycle13 plan §2, same rule
// as the existing 429 lock — rate_limit_message_lock_test.go). Before this
// cycle the only enforcement was hand-maintained (repo/token.go:159/183/209,
// middleware/distributor.go:335/355/363, repo/channel_cache.go:317/328,
// app/relay/helper/price.go:83, app/relay/mjproxy_handler.go:295/469 — see
// cycle13 plan finding #6) — nothing stopped a FUTURE edit from
// reintroducing a Chinese literal into a wire-facing error constructor.
//
// Scope, stated precisely so this is not read as more than it is: a go/ast
// scan of every non-test .go file under internal/adapter/middleware,
// internal/adapter/repo, and internal/app/relay (recursive, so its "helper"
// and "common_handler" subpackages are covered too) for CallExpr nodes whose
// callee is one of:
//   - abortWithOpenAiMessage (bare identifier — this package's own helper)
//   - MidjourneyErrorWrapper (bare or qualified, e.g. app.MidjourneyErrorWrapper)
//   - errors.New / fmt.Errorf (qualified by those exact package names)
//   - a qualified call whose selector name has the prefix "NewError" (covers
//     types.NewError / types.NewErrorWithStatusCode from any import alias)
//
// For each matched call, every argument that is a direct string literal, or
// a chain of string literals joined with "+" (e.g. "無効…" + err.Error()),
// is checked for any rune >= 0x80. This is a SHALLOW, syntactic check, not
// full interprocedural dataflow: a literal buried inside a nested,
// non-target function call passed as an argument (there is no such case in
// this tree today — grepped) would not be caught. It does not need to
// recurse into nested target calls itself (e.g. errors.New("…") passed as
// types.NewError's first argument) — ast.Inspect below visits every CallExpr
// in the file exactly once regardless of nesting depth, so a nested target
// call is matched independently when Inspect reaches it.
//
// Named whitelist (wireMessageGateWhitelist): every entry is either (a) text
// this repo has a documented reason to keep Chinese — today, exactly
// repo/redemption.go's five Switch-classifier-facing sentinels (the
// cycle13 plan §2 decision: "其余含 过期/禁用/不存在 的子串不改") plus
// ErrRedemptionFailed's fallback text (matches a pre-existing string a
// foreign test — switch_redeem_test.go's
// TestSwitchRedeemAnonymous_RawDBErrorSanitized — pins) — or (b) pre-existing
// Chinese text this lane does not own the file for and a foreign test pins
// byte-for-byte (repo/redemption.go's two Redeem() precondition guards,
// pinned by cov_repo-deep_redemption_redeem_test.go; repo/ability.go and
// repo/user.go's argument-validation guards, pinned by
// cov_handler-identity-log_token_test.go and others — neither file is owned
// by cycle13 L3). A whitelist hit still counts toward sitesSeen (scanner
// honesty) and is checked for staleness (every entry must actually match a
// site, or the whitelist itself has drifted from the source).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// wireMessageGateWhitelist maps "relpath:line" (relpath is repo-root
// relative, forward-slash, of the FILE; line is the *ast.BasicLit's own
// line) to a short reason. See the file-level comment for the two
// categories every entry falls into.
var wireMessageGateWhitelist = map[string]string{
	"internal/adapter/repo/redemption.go:172":        "ErrRedemptionInvalid — switch-contract text, cycle13 plan §2 (unchanged this cycle)",
	"internal/adapter/repo/redemption.go:184":        "ErrRedemptionUsed — switch-contract text, cycle13 plan §2 (the finding #7 fix: 已被使用→已使用)",
	"internal/adapter/repo/redemption.go:188":        "ErrRedemptionExpired — switch-contract text, cycle13 plan §2 (unchanged this cycle)",
	"internal/adapter/repo/redemption.go:198":        "ErrRedemptionUserNotFound — switch-contract text, cycle13 plan §2 (unchanged this cycle)",
	"internal/adapter/repo/redemption.go:206":        "ErrRedemptionWrongTenant — switch-contract text, cycle13 plan §2 (unchanged this cycle)",
	"internal/adapter/repo/redemption.go:221":        "ErrRedemptionFailed — matches the pre-existing generic fallback switch_redeem_test.go's TestSwitchRedeemAnonymous_RawDBErrorSanitized pins",
	"internal/adapter/repo/redemption.go:284":        "Redeem() empty-key guard — pinned byte-for-byte by cov_repo-deep_redemption_redeem_test.go:66 (not owned by L3)",
	"internal/adapter/repo/redemption.go:287":        "Redeem() zero-userId guard — pinned byte-for-byte by cov_repo-deep_redemption_redeem_test.go:69 (not owned by L3)",
	"internal/adapter/repo/ability.go:141":           "pre-existing, out of cycle13 L3 ownership (ability.go not in L3's Owned list)",
	"internal/adapter/repo/ability.go:380":           "pre-existing, out of cycle13 L3 ownership (ability.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:308":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list; pinned by comments in a1_provisioned_token_auth_test.go)",
	"internal/adapter/repo/user.go:440":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:477":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:494":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:546":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:572":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:691":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:701":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:708":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:716":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:858":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:890":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/adapter/repo/user.go:935":              "pre-existing, out of cycle13 L3 ownership (user.go not in L3's Owned list)",
	"internal/app/relay/helper/valid_request.go:199": "the message is English prose but quotes the literal '×' (U+00D7 multiplication sign, not a CJK character) while explaining callers must not use it — pre-existing, out of cycle13 L3 ownership (valid_request.go not in L3's Owned list)",
}

// wireMessageGateSitesFloor is the scanner-honesty floor: today's scan of
// the three roots finds well over 100 sites (abortWithOpenAiMessage alone
// has 40+ call sites in this package — see abort_code_structural_test.go's
// own floor). Set far below that so ordinary edits do not need to bump it;
// it exists to catch a REGRESSION (the scan silently walking zero/few files)
// rather than to pin today's count.
const wireMessageGateSitesFloor = 20

// wireMessageGateRepoRoot walks up from this package directory to the
// module root (mirrors option_owned_globals_gate_test.go's
// optionGateRepoRoot).
func wireMessageGateRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root above this package")
	return ""
}

// wireMessageGateTargetName reports the matched target function's display
// name and whether call is one of the four categories described in the
// file-level comment.
func wireMessageGateTargetName(call *ast.CallExpr) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		if fn.Name == "abortWithOpenAiMessage" || fn.Name == "MidjourneyErrorWrapper" {
			return fn.Name, true
		}
	case *ast.SelectorExpr:
		if strings.HasPrefix(fn.Sel.Name, "NewError") {
			return "types." + fn.Sel.Name, true
		}
		if fn.Sel.Name == "MidjourneyErrorWrapper" {
			return "MidjourneyErrorWrapper", true
		}
		if pkgIdent, ok := fn.X.(*ast.Ident); ok {
			if pkgIdent.Name == "errors" && fn.Sel.Name == "New" {
				return "errors.New", true
			}
			if pkgIdent.Name == "fmt" && fn.Sel.Name == "Errorf" {
				return "fmt.Errorf", true
			}
		}
	}
	return "", false
}

// wireMessageGateStringLiterals returns every *ast.BasicLit STRING node
// directly reachable from expr through a chain of "+" BinaryExprs — i.e. the
// literal pieces of `"a" + x + "b"`-shaped arguments, WITHOUT descending
// into unrelated nested CallExprs (see the file-level comment for why that
// is deliberate and sufficient).
func wireMessageGateStringLiterals(expr ast.Expr) []*ast.BasicLit {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return []*ast.BasicLit{e}
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			out := wireMessageGateStringLiterals(e.X)
			out = append(out, wireMessageGateStringLiterals(e.Y)...)
			return out
		}
	}
	return nil
}

// wireMessageGateASCIIOnly reports the first offending rune (0 if none) in
// the BasicLit's decoded value.
func wireMessageGateASCIIOnly(lit *ast.BasicLit) (rune, bool) {
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		// Not a plain quoted string (e.g. an unusual raw-string edge case) —
		// fall back to the raw token text minus its quote/backtick delimiters.
		value = strings.Trim(lit.Value, "`\"")
	}
	for _, r := range value {
		if r >= 0x80 {
			return r, true
		}
	}
	return 0, false
}

func TestWireMessageLanguageGate_ASCIIOnly(t *testing.T) {
	root := wireMessageGateRepoRoot(t)
	scanRoots := []string{
		filepath.Join(root, "internal", "adapter", "middleware"),
		filepath.Join(root, "internal", "adapter", "repo"),
		filepath.Join(root, "internal", "app", "relay"),
	}

	fset := token.NewFileSet()
	sitesSeen := 0
	filesTouched := map[string]bool{}
	seenWhitelist := map[string]bool{}
	var violations []string

	for _, scanRoot := range scanRoots {
		walkErr := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)

			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			file, parseErr := parser.ParseFile(fset, path, src, 0)
			if parseErr != nil {
				// A file another lane is mid-edit must not be reported as a
				// finding of this gate.
				t.Logf("skipping unparseable %s: %v", rel, parseErr)
				return nil
			}

			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				targetName, matched := wireMessageGateTargetName(call)
				if !matched {
					return true
				}
				sitesSeen++
				filesTouched[rel] = true

				for _, arg := range call.Args {
					for _, lit := range wireMessageGateStringLiterals(arg) {
						bad, isBad := wireMessageGateASCIIOnly(lit)
						if !isBad {
							continue
						}
						line := fset.Position(lit.Pos()).Line
						key := rel + ":" + strconv.Itoa(line)
						if _, ok := wireMessageGateWhitelist[key]; ok {
							seenWhitelist[key] = true
							continue
						}
						violations = append(violations, key+": "+targetName+"() argument contains non-ASCII rune "+strconv.QuoteRune(bad)+": "+lit.Value)
					}
				}
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", scanRoot, walkErr)
		}
	}

	if sitesSeen < wireMessageGateSitesFloor {
		t.Fatalf("sitesSeen = %d, want >= %d (scanner-honesty floor — the scan found suspiciously few call sites; it may be broken, not the codebase clean)", sitesSeen, wireMessageGateSitesFloor)
	}
	if len(filesTouched) == 0 {
		t.Fatal("filesTouched = 0 — the scan touched no files, it is broken")
	}
	t.Logf("wire message language gate: %d call sites scanned across %d files", sitesSeen, len(filesTouched))

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("%d non-ASCII wire-message literal(s) found (API error.message must be English-only — cycle13 plan §2):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}

	// Whitelist-honesty: every entry must have actually matched a scanned
	// site this run, otherwise it is stale (the line moved, the file
	// changed, or the entry was never real) and silently hides nothing.
	var stale []string
	for key := range wireMessageGateWhitelist {
		if !seenWhitelist[key] {
			stale = append(stale, key)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d stale wire-message-gate whitelist entr(y/ies) never matched a scan site (the file moved/changed — update the whitelist):\n%s",
			len(stale), strings.Join(stale, "\n"))
	}
}
