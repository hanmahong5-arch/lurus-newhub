package repo

// erasure_model_coverage_test.go — cycle-13 L5: a structural forcing
// function for the PIPL §47 erasure cascade. Every model repo/main.go's
// migrateDB() registers with DB.AutoMigrate is inspected for a field whose
// name contains one of a small set of personal-data-shaped substrings —
// Username/Content/Prompt/Properties/IpAddress/UserAgent, the same list the
// cycle-13 planning doc names. A match forces the model's name to appear in
// exactly one of the two hand-maintained tables below:
//
//   - erasureCoveredModels: the cascade (lifecycle.executeErasure, driven by
//     the repo.Hard*/Scrub*/Anonymize* primitives in privacy_erasure.go)
//     disposes of that column for the erasing user.
//   - erasureExemptModels: the column cannot be attributed to an erasure
//     subject (no user_id-shaped key on the row), or some other documented
//     reason. Every entry states the reason; none may claim the model has
//     no personal data, because every entry in this table by construction
//     DOES have a matching field name.
//
// That last rule is a deliberate departure from the cycle-13 plan's
// parenthetical for this gate ("豁免表初始只含真正无个人数据的表" — the
// exemption table initially holds only tables with no personal data). As
// written it cannot be satisfied: a model only ever reaches the exemption
// table BECAUSE one of its field names matched the personal-data
// vocabulary, so "has no personal data" is never an available reason. The
// rule in force is the one above — an exemption states why the per-user
// cascade has no key to reach the row by, plus the mitigation that stands
// in for it (cycle-13 L5 repair round, D-L5-4; carried as an accepted
// deviation in the lane report rather than inferred from this comment).
//
// The set of registered models is not a hand-copied list — it is parsed
// from repo/main.go's actual source via go/ast on every run
// (erasureMigrateDBModelNames), so a model migrateDB() registers in a later
// change and never classified here fails this test with "migrateDB()
// registers ... which erasureModelTypeRegistry does not know", not
// silently.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// erasurePersonalDataFieldNames is the field-name vocabulary the cycle-13
// plan names as personal-data-shaped. A model with a field whose name
// CONTAINS one of these (case-sensitive) as a substring — not just an exact
// match — must be classified below; substring matching is what catches
// Midjourney's PromptEn alongside Prompt.
var erasurePersonalDataFieldNames = []string{
	"Username", "Content", "Prompt", "Properties", "IpAddress", "UserAgent",
}

// erasureFieldMatches reports the first keyword (if any) that fieldName
// contains.
func erasureFieldMatches(fieldName string) (keyword string, matched bool) {
	for _, kw := range erasurePersonalDataFieldNames {
		if strings.Contains(fieldName, kw) {
			return kw, true
		}
	}
	return "", false
}

// erasureModelTypeRegistry maps every name erasureMigrateDBModelNames can
// produce — a bare local identifier ("Channel") or a package-qualified one
// ("entity.Release") — to a zero-value instance of the real Go type, so
// field names can be read with reflect instead of a second hand-copied
// field list. Spelled out by hand once; kept honest against drift by the
// "missing from registry" failure mode in
// TestErasureCascadeCoversEveryPersonalDataShapedModel below, not by
// re-deriving it structurally (that would just move the hand-maintained
// step, not remove it).
var erasureModelTypeRegistry = map[string]reflect.Type{
	"Channel":                           reflect.TypeOf(Channel{}),
	"Token":                             reflect.TypeOf(Token{}),
	"User":                              reflect.TypeOf(User{}),
	"Option":                            reflect.TypeOf(Option{}),
	"Redemption":                        reflect.TypeOf(Redemption{}),
	"Ability":                           reflect.TypeOf(Ability{}),
	"Log":                               reflect.TypeOf(Log{}),
	"Midjourney":                        reflect.TypeOf(Midjourney{}),
	"QuotaData":                         reflect.TypeOf(QuotaData{}),
	"Task":                              reflect.TypeOf(Task{}),
	"Model":                             reflect.TypeOf(Model{}),
	"Vendor":                            reflect.TypeOf(Vendor{}),
	"PrefillGroup":                      reflect.TypeOf(PrefillGroup{}),
	"Setup":                             reflect.TypeOf(Setup{}),
	"InternalApiKey":                    reflect.TypeOf(InternalApiKey{}),
	"Tenant":                            reflect.TypeOf(Tenant{}),
	"UserIdentityMapping":               reflect.TypeOf(UserIdentityMapping{}),
	"TenantConfig":                      reflect.TypeOf(TenantConfig{}),
	"entity.Release":                    reflect.TypeOf(entity.Release{}),
	"entity.ReleaseArtifact":            reflect.TypeOf(entity.ReleaseArtifact{}),
	"entity.DownloadLog":                reflect.TypeOf(entity.DownloadLog{}),
	"SwitchConfigPresetRow":             reflect.TypeOf(SwitchConfigPresetRow{}),
	"entity.CurrencyExchange":           reflect.TypeOf(entity.CurrencyExchange{}),
	"entity.AuditEvent":                 reflect.TypeOf(entity.AuditEvent{}),
	"entity.OpenRouterSyncJob":          reflect.TypeOf(entity.OpenRouterSyncJob{}),
	"entity.ModelUsageStat":             reflect.TypeOf(entity.ModelUsageStat{}),
	"entity.TenantCreditPool":           reflect.TypeOf(entity.TenantCreditPool{}),
	"entity.TenantCreditPoolDraw":       reflect.TypeOf(entity.TenantCreditPoolDraw{}),
	"entity.CreditPoolFundEvent":        reflect.TypeOf(entity.CreditPoolFundEvent{}),
	"entity.ProvisionedRedemptionBatch": reflect.TypeOf(entity.ProvisionedRedemptionBatch{}),
	"PlaygroundPreset":                  reflect.TypeOf(PlaygroundPreset{}),
	"entity.LeaderElection":             reflect.TypeOf(entity.LeaderElection{}),
	"entity.PrivacyErasureRequest":      reflect.TypeOf(entity.PrivacyErasureRequest{}),
	"entity.ModelRateLimit":             reflect.TypeOf(entity.ModelRateLimit{}),
	"entity.BillingCheckoutOrder":       reflect.TypeOf(entity.BillingCheckoutOrder{}),
	"entity.Project":                    reflect.TypeOf(entity.Project{}),
	"entity.TenantInvite":               reflect.TypeOf(entity.TenantInvite{}),
	"entity.UserSession":                reflect.TypeOf(entity.UserSession{}),
	"entity.AdminPermissionGrant":       reflect.TypeOf(entity.AdminPermissionGrant{}),
	"entity.ResponseRegistry":           reflect.TypeOf(entity.ResponseRegistry{}),
	"entity.ChatSession":                reflect.TypeOf(entity.ChatSession{}),
	"entity.ChatMessage":                reflect.TypeOf(entity.ChatMessage{}),
}

// erasureCoveredModels: the cascade actively disposes of the matched field
// for the erasing user. Each reason names the repo primitive that does it.
var erasureCoveredModels = map[string]string{
	"User": "repo.AnonymizeUserRow overwrites Username in place (executeErasure's final step)",
	"Log":  "repo.AnonymizeLogsBatch clears username/content in batches (executeErasure's logs step)",
	"UserIdentityMapping": "repo.HardDeleteUserIdentityMappings hard-deletes the whole row " +
		"(executeErasure's mappings step) — PreferredUsername matches on substring, not the more " +
		"obvious Email/DisplayName; caught by this gate's substring match, not by the manual review " +
		"that wrote the cycle-13 plan's table list",
	"Midjourney": "repo.TerminaliseOpenMidjourneyForUser takes the rows out of the poller's " +
		"selection, then repo.HardDeleteMidjourneyBatch hard-deletes them in batches " +
		"(executeErasure's content step, cycle-13 L5)",
	"QuotaData": "repo.ScrubQuotaDataUsernameForUser overwrites username with ErasedMarker " +
		"(executeErasure's content step, cycle-13 L5)",
	"Task": "repo.TerminaliseOpenTasksForUser stops the task poller from writing to the rows, " +
		"then repo.ScrubTasksForUser blanks properties/data/fail_reason, keeping quota/ids " +
		"(executeErasure's content step, cycle-13 L5)",
	"PlaygroundPreset": "repo.HardDeletePlaygroundPresetsForUser hard-deletes the user's rows " +
		"(executeErasure's content step, cycle-13 L5) — found via this gate, not listed in the " +
		"cycle-13 plan's own table enumeration",
	"entity.UserSession": "repo.HardDeleteUserSessions hard-deletes the user's rows (executeErasure's tokens step)",
	"entity.ChatMessage": "repo.HardDeleteChatMessagesBatch hard-deletes the user's rows in " +
		"batches, keyed on session ownership OR the message's own user_id " +
		"(executeErasure's content step, cycle-13 L5)",
}

// erasureExemptModels: the matched field cannot be attributed to an
// erasure subject through the per-user cascade. Every reason states the
// structural cause (not "no personal data" — the field name matched
// because the column IS personal-data-shaped) and, where relevant, the
// separate mitigation in place.
var erasureExemptModels = map[string]string{
	"entity.DownloadLog": "download_logs has no user_id/tenant_id column — a download log row " +
		"cannot be attributed to an erasure subject, so the per-user cascade has no key to select " +
		"it by. Exposure is minimized at write time instead: ReleaseService.HandleDownload " +
		"(release_service.go) stores repo.MaskIP(ip) and truncates user_agent/referer to 512 " +
		"bytes, cycle-13 L5.",
}

// erasureParseMigrateDBModels parses a Go file and returns, for every
// argument that file's migrateDB() passes to DB.AutoMigrate(...): the type
// name (spelled the way erasureModelTypeRegistry keys them) of each
// argument it can read, and the source text of each argument it CANNOT.
// The second return value is why this is not a t.Helper with a t inside:
// an argument shape the gate does not understand must be a loud failure at
// the call site (a dropped argument is a model that escapes classification
// silently), and the unit test below needs to observe that list for a
// fixture file rather than have it fataled out from under it.
func erasureParseMigrateDBModels(path string) (names []string, unparsed []string, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "migrateDB" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "AutoMigrate" {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok || recv.Name != "DB" {
				return true
			}
			for _, arg := range call.Args {
				name, ok := erasureModelArgName(arg)
				if !ok {
					unparsed = append(unparsed, erasureExprSource(fset, arg))
					continue
				}
				names = append(names, name)
			}
			return false
		})
	}
	return names, unparsed, nil
}

// erasureExprSource renders an AST expression back to source text for a
// failure message, falling back to its position when printing fails.
func erasureExprSource(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return fmt.Sprintf("<unprintable expression at %s>", fset.Position(expr.Pos()))
	}
	return fmt.Sprintf("%s (at %s)", buf.String(), fset.Position(expr.Pos()))
}

// erasureMigrateDBModelNames is the gate's own wrapper: same derivation,
// with the two ways it can stop being trustworthy — an argument shape it
// cannot read, or a suspiciously small result — turned into test failures.
func erasureMigrateDBModelNames(t *testing.T, mainGo string) []string {
	t.Helper()
	names, unparsed, err := erasureParseMigrateDBModels(mainGo)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// A dropped argument is a model nobody classified. Until cycle-13's
	// repair round these were skipped in silence, so a future
	// DB.AutoMigrate(models...) or a non-pointer literal could add a
	// personal-data-bearing table with this gate still green.
	if len(unparsed) > 0 {
		t.Fatalf("migrateDB()'s DB.AutoMigrate(...) passes %d argument(s) this gate cannot read: %s\n  — extend erasureModelArgName to cover the shape, or spell the argument &Xxx{} / &pkg.Xxx{}",
			len(unparsed), strings.Join(unparsed, "; "))
	}
	// Fail fast rather than pass vacuously: if the derivation ever stops
	// matching migrateDB's shape it would return a tiny set (or none) and
	// the coverage check below would report nothing to classify. The floor
	// is well under the 42 registered at the time this gate was written.
	if len(names) < 30 {
		t.Fatalf("derived only %d models from migrateDB()'s DB.AutoMigrate(...) call (%v); the derivation no longer matches the source", len(names), names)
	}
	return names
}

// erasureModelArgName extracts the type name from one AutoMigrate argument
// spelled "&Xxx{}" or "&pkg.Xxx{}" — the two shapes migrateDB uses today.
// Anything else returns false and is reported by the caller.
func erasureModelArgName(arg ast.Expr) (string, bool) {
	unary, ok := arg.(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return "", false
	}
	lit, ok := unary.X.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	switch typ := lit.Type.(type) {
	case *ast.Ident:
		return typ.Name, true
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		return pkg.Name + "." + typ.Sel.Name, true
	}
	return "", false
}

// erasureGateRepoRoot walks up from this package directory to the module
// root (same technique as the sibling option_owned_globals_gate_test.go,
// duplicated here rather than shared so this file has no dependency on
// another lane's test file surviving unchanged).
func erasureGateRepoRoot(t *testing.T) string {
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

// erasureFirstMatchingField returns the first field of typ (direct fields
// only, not promoted/embedded) whose name matches
// erasurePersonalDataFieldNames.
func erasureFirstMatchingField(typ reflect.Type) (fieldName, keyword string, matched bool) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if kw, ok := erasureFieldMatches(f.Name); ok {
			return f.Name, kw, true
		}
	}
	return "", "", false
}

func TestErasureCascadeCoversEveryPersonalDataShapedModel(t *testing.T) {
	root := erasureGateRepoRoot(t)
	mainGo := filepath.Join(root, "internal", "adapter", "repo", "main.go")
	names := erasureMigrateDBModelNames(t, mainGo)

	var missingFromRegistry []string
	var unclassified []string
	matchedCount := 0
	for _, name := range names {
		typ, ok := erasureModelTypeRegistry[name]
		if !ok {
			missingFromRegistry = append(missingFromRegistry, name)
			continue
		}
		fieldName, keyword, matched := erasureFirstMatchingField(typ)
		if !matched {
			continue
		}
		matchedCount++
		_, covered := erasureCoveredModels[name]
		_, exempt := erasureExemptModels[name]
		if !covered && !exempt {
			unclassified = append(unclassified, fmt.Sprintf("%s (field %s matches %q)", name, fieldName, keyword))
		}
	}

	if len(missingFromRegistry) > 0 {
		sort.Strings(missingFromRegistry)
		t.Fatalf("migrateDB() registers %v which erasureModelTypeRegistry does not know — add it there and classify it in erasureCoveredModels or erasureExemptModels", missingFromRegistry)
	}
	// Same anti-vacuous-pass floor as erasureMigrateDBModelNames above: 9
	// models matched at the time this gate was written (User, Log,
	// Midjourney, QuotaData, Task, PlaygroundPreset, entity.DownloadLog,
	// entity.UserSession, entity.ChatMessage).
	if matchedCount < 7 {
		t.Fatalf("only %d models matched a personal-data-shaped field name; the derivation no longer matches the known set (want >= 7)", matchedCount)
	}
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Fatalf("models with a personal-data-shaped field are covered by neither the erasure cascade nor an exemption entry:\n  %s", strings.Join(unclassified, "\n  "))
	}
}

// TestErasureModelDerivationRejectsUnparsedAutoMigrateArgs is the mutation
// surface for the "cannot read this argument" arm added in cycle-13 L5's
// repair round (D-L5-4). It runs the real derivation over a fixture file
// rather than repo/main.go, because proving the arm by editing
// migrateDB()'s own argument list would mean editing a file this lane does
// not own. The fixture carries both shapes the gate used to drop in
// silence: a non-pointer composite literal and a variadic slice spread.
func TestErasureModelDerivationRejectsUnparsedAutoMigrateArgs(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "fixture_main.go")
	var b strings.Builder
	b.WriteString("package repo\n\nfunc migrateDB() error {\n\treturn DB.AutoMigrate(\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "\t\t&Model%d{},\n", i)
	}
	b.WriteString("\t\tNotAPointer{},\n")
	b.WriteString("\t\textraModels...,\n")
	b.WriteString("\t)\n}\n")
	if err := os.WriteFile(fixture, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	names, unparsed, err := erasureParseMigrateDBModels(fixture)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(names) != 30 {
		t.Errorf("readable arguments = %d, want 30", len(names))
	}
	if len(unparsed) != 2 {
		t.Fatalf("unreadable arguments = %d (%v), want 2 — a dropped argument is a model that escapes classification", len(unparsed), unparsed)
	}
	joined := strings.Join(unparsed, "; ")
	for _, want := range []string{"NotAPointer{}", "extraModels"} {
		if !strings.Contains(joined, want) {
			t.Errorf("unreadable argument list %q does not name %q", joined, want)
		}
	}
}

// TestErasureModelDerivationReadsTheRealMigrateDB keeps the derivation
// honest about the file it actually guards: every argument of the live
// migrateDB() must be readable, which is the same condition
// erasureMigrateDBModelNames fatals on — asserted here as a value so the
// count is visible in the failure message rather than only as a fatal.
func TestErasureModelDerivationReadsTheRealMigrateDB(t *testing.T) {
	root := erasureGateRepoRoot(t)
	names, unparsed, err := erasureParseMigrateDBModels(filepath.Join(root, "internal", "adapter", "repo", "main.go"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(unparsed) != 0 {
		t.Errorf("migrateDB() has %d AutoMigrate argument(s) the gate cannot read: %s", len(unparsed), strings.Join(unparsed, "; "))
	}
	if len(names) < 30 {
		t.Errorf("readable models = %d, want >= 30", len(names))
	}
}
