package app

// quota_conservation_c13_test.go — money conservation across the four ledgers
// a relay moves (users.quota, tokens.remain_quota, the tenant credit pool, the
// platform wallet). Cycle-13 L1.
//
// Every test here drives the real settlement entry points against the sqlite
// fixture and reads the resulting rows back; none of them hand-build a
// "charged" state, because the defects these pin are exactly the ones a
// hand-built state hides (a ledger nobody wrote is indistinguishable from a
// ledger nobody read).

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
	"gorm.io/gorm"
)

// userDailyUsed reads back the daily_used counter the per-day cap is enforced
// against (repo.GetUserDailyQuotaInfo / SwitchToFallbackGroup read the same
// column).
func userDailyUsed(t *testing.T, db *gorm.DB, userId int) int {
	t.Helper()
	var u repo.User
	if err := db.First(&u, userId).Error; err != nil {
		t.Fatalf("read user %d: %v", userId, err)
	}
	return u.DailyUsed
}

// TestPostConsumeQuota_DailyUsedCountsTotalCost pins the daily counter to the
// REQUEST'S COST (quota + preConsumedQuota — the same total Phase 2.5 debits
// the pool), not to the post-consume difference against the estimate.
//
// With the difference, a request whose estimate overshot counted nothing at
// all (a negative delta never reached the counter) and one whose estimate
// covered most of the cost counted only the remainder — so a daily cap of
// 1,000,000 admitted many times that much spend.
func TestPostConsumeQuota_DailyUsedCountsTotalCost(t *testing.T) {
	db := setupServiceTestDB(t)

	// Case 1: the estimate overshot. 1200 was frozen, 200 is handed back, so
	// the request cost 1000.
	overUserId := seedTestUser(t, db, 100_000)
	overKey, overTokenId := seedTestToken(t, db, overUserId, 100_000, false)
	overInfo := &relaycommon.RelayInfo{UserId: overUserId, TokenId: overTokenId, TokenKey: overKey}
	if err := PostConsumeQuota(overInfo, -200, 1200, false); err != nil {
		t.Fatalf("PostConsumeQuota (over-estimated): %v", err)
	}
	if got := userDailyUsed(t, db, overUserId); got != 1000 {
		t.Errorf("daily_used after a 1200-frozen / 1000-cost request = %d, want 1000 — "+
			"the daily cap must count what the request cost, not the settlement difference", got)
	}

	// Case 2: the estimate undershot. 1200 was frozen and 500 more is charged,
	// so the request cost 1700.
	underUserId := seedTestUser(t, db, 100_000)
	underKey, underTokenId := seedTestToken(t, db, underUserId, 100_000, false)
	underInfo := &relaycommon.RelayInfo{UserId: underUserId, TokenId: underTokenId, TokenKey: underKey}
	if err := PostConsumeQuota(underInfo, 500, 1200, false); err != nil {
		t.Fatalf("PostConsumeQuota (under-estimated): %v", err)
	}
	if got := userDailyUsed(t, db, underUserId); got != 1700 {
		t.Errorf("daily_used after a 1200-frozen / 1700-cost request = %d, want 1700", got)
	}
}

// TestPostWssConsumeQuota_RefundsUnsettledPreConsume pins the realtime
// session's settlement: /v1/realtime freezes an estimate like every other
// relay format (handler/relay.go:360) and charges each usage event as it
// arrives (PreWssConsumeQuota -> PostConsumeQuota), so the freeze itself is
// pure surplus by the time the session ends. Until this test it was never
// settled nor released on the success path — handler/relay.go returns on
// newAPIError == nil, so releasePreConsumedOnFailure never ran — and every
// realtime session silently kept FinalPreConsumedQuota of the customer's
// money.
func TestPostWssConsumeQuota_RefundsUnsettledPreConsume(t *testing.T) {
	db := setupServiceTestDB(t)

	const start = 100_000
	userId := seedTestUser(t, db, start)
	key, tokenId := seedTestToken(t, db, userId, start, false)

	c := createTestGinContext()
	c.Set("token_name", "wss-tkn")

	const model = "gpt-4o-realtime-preview"
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userId,
		TokenId:         tokenId,
		TokenKey:        key,
		OriginModelName: model,
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
		PriceData: types.PriceData{
			ModelRatio:     2.0,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.0},
		},
	}

	// The real freeze — the same call every relay format makes before dialing
	// the upstream.
	if apiErr := PreConsumeQuota(c, 1200, relayInfo); apiErr != nil {
		t.Fatalf("PreConsumeQuota: %v", apiErr)
	}
	if relayInfo.FinalPreConsumedQuota != 1200 {
		t.Fatalf("pre-consume did not freeze 1200 (got %d) — the rest of this test would prove nothing",
			relayInfo.FinalPreConsumedQuota)
	}

	// One usage event, charged exactly the way PreWssConsumeQuota charges it.
	const eventQuota = 700
	if err := PostConsumeQuota(relayInfo, eventQuota, 0, false); err != nil {
		t.Fatalf("event charge: %v", err)
	}

	usage := &dto.RealtimeUsage{
		TotalTokens:  200,
		InputTokens:  120,
		OutputTokens: 80,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens:  80,
			AudioTokens: 40,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  50,
			AudioTokens: 30,
		},
	}

	PostWssConsumeQuota(c, relayInfo, model, usage, "")

	if got := userQuota(t, db, userId); got != start-eventQuota {
		t.Errorf("user quota after the session = %d, want %d — the session charged %d in events, "+
			"so anything else is the unsettled freeze still held", got, start-eventQuota, eventQuota)
	}
	if got := tokenRemain(t, db, tokenId); got != start-eventQuota {
		t.Errorf("token remain_quota after the session = %d, want %d", got, start-eventQuota)
	}
}

// preConsumeFreezeCallees are the two entry points that take money BEFORE the
// work is done and therefore owe a settle or a release. PreWssConsumeQuota is
// deliberately not one of them: despite the name it is a per-event CHARGE
// (it calls PostConsumeQuota), not a freeze.
var preConsumeFreezeCallees = map[string]bool{
	"PreConsumeQuota":      true,
	"PreConsumeTokenQuota": true,
}

// settleOrReleaseSymbols are the three ways a package can discharge a freeze.
var settleOrReleaseSymbols = []string{
	"SettleConsume",
	"ReturnPreConsumedQuota",
	"releasePreConsumedOnFailure",
}

// TestEveryPreConsumeSiteHasASettleOrRelease is the structural half of the
// realtime defect above: the behaviour test pins ONE path, this pins the
// shape. A package that freezes quota must also contain the code that gives
// it back or settles it; a new relay format that only calls PreConsumeQuota
// fails here before it ships.
//
// The floors below exist because a gate that silently matches nothing reports
// green forever (cycle-6: removing `seen > 0` turned a gate into a no-op).
func TestEveryPreConsumeSiteHasASettleOrRelease(t *testing.T) {
	root := repoRootForGate(t)

	type pkgFacts struct {
		freezeSites   []string
		hasDischarger bool
	}
	packages := map[string]*pkgFacts{}
	filesParsed := 0

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web", "vendor", "_bmad-output":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			// A file another lane is mid-edit on must not turn this gate red.
			return nil
		}
		filesParsed++
		dir := filepath.Dir(path)
		facts := packages[dir]
		if facts == nil {
			facts = &pkgFacts{}
			packages[dir] = facts
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calleeName(call.Fun)
			if name == "" {
				return true
			}
			if preConsumeFreezeCallees[name] {
				pos := fset.Position(call.Pos())
				facts.freezeSites = append(facts.freezeSites,
					filepath.Base(pos.Filename)+":"+itoa(pos.Line)+" "+name)
			}
			for _, sym := range settleOrReleaseSymbols {
				if name == sym {
					facts.hasDischarger = true
				}
			}
			return true
		})
		// A package that DECLARES a discharger (handler's
		// releasePreConsumedOnFailure) discharges too, even if the call is in
		// a deferred closure this walk reads as a call anyway.
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			for _, sym := range settleOrReleaseSymbols {
				if fn.Name.Name == sym {
					facts.hasDischarger = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if filesParsed < 200 {
		t.Fatalf("only %d Go files parsed under %s — the walker is broken, so a green result here means nothing",
			filesParsed, root)
	}

	sitesSeen := 0
	for dir, facts := range packages {
		sitesSeen += len(facts.freezeSites)
		if len(facts.freezeSites) > 0 && !facts.hasDischarger {
			t.Errorf("package %s freezes quota at %v but contains none of %v — "+
				"a freeze that is never settled or released keeps the customer's money",
				dir, facts.freezeSites, settleOrReleaseSymbols)
		}
	}
	// Measured at HEAD (2026-09-20): handler/relay.go's PreConsumeQuota call
	// and app/pre_consume_quota.go's PreConsumeTokenQuota call. The plan asked
	// for a floor of 3; there are 2, so 3 would fail on a correct tree.
	if sitesSeen < 2 {
		t.Fatalf("found %d pre-consume freeze site(s), want >= 2 — the matcher stopped matching "+
			"(renamed callee?), so the rule above checked nothing", sitesSeen)
	}
}

// TestPostWssConsumeQuotaCallsSettleConsume is the same structural argument
// applied to the one function the behaviour test above covers: the realtime
// settlement lives inside PostWssConsumeQuota and nowhere else (WssHelper
// returns straight after it), so its absence is invisible to every
// package-level rule.
func TestPostWssConsumeQuotaCallsSettleConsume(t *testing.T) {
	fset := token.NewFileSet()
	path := filepath.Join(repoRootForGate(t), "internal", "app", "quota.go")
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found := false
	seenFunc := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "PostWssConsumeQuota" {
			continue
		}
		seenFunc = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && calleeName(call.Fun) == "SettleConsume" {
				found = true
			}
			return true
		})
	}
	if !seenFunc {
		t.Fatalf("PostWssConsumeQuota not found in %s — this gate is matching nothing", path)
	}
	if !found {
		t.Error("PostWssConsumeQuota does not call SettleConsume — the realtime freeze is never discharged")
	}
}

// calleeName returns the identifier a call expression targets (`f` for f(),
// `F` for pkg.F()), or "" for anything else (method values on expressions,
// function literals).
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// repoRootForGate walks up from the test's working directory to the directory
// holding go.mod.
func repoRootForGate(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("go.mod not found above %s", dir)
	return ""
}

// TestPostConsumeQuota_TokenWriteFailureCompensatesPool pins the third ledger
// in the one branch where the three can disagree about a single request.
//
// Phase 2.5 debits the tenant pool, Phase 3 then writes the per-key debit. If
// that write fails, Phase 3 hands the user's balance back — and before this
// test it left the pool debited, so one request landed as three different
// amounts: user charged 0, token charged 0, tenant charged the full cost.
func TestPostConsumeQuota_TokenWriteFailureCompensatesPool(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)

	const tenantID = "t-comp-pool"
	const startBalance = 50_000
	const startQuota = 100_000
	const charge = 900

	userId := seedTestUser(t, db, startQuota)
	tokenId := seedTenantToken(t, db, userId, tenantID)
	tok, err := repo.GetTokenById(tokenId)
	if err != nil {
		t.Fatalf("read seeded token: %v", err)
	}

	pool, err := repo.CreateTenantCreditPool(tenantID, 1, 1_000_000, repo.PoolResetMonthly, 80)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := repo.TopupPool(pool.ID, tenantID, startBalance, 1, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	// Force the per-key debit to fail the way a DB outage would.
	prevSeam := decreaseTokenQuotaSeam
	decreaseTokenQuotaSeam = func(id int, key string, quota int) error {
		return errors.New("simulated token quota write failure")
	}
	t.Cleanup(func() { decreaseTokenQuotaSeam = prevSeam })

	relayInfo := &relaycommon.RelayInfo{
		UserId:   userId,
		TokenId:  tokenId,
		TokenKey: tok.Key,
	}

	if err := PostConsumeQuota(relayInfo, charge, 0, false); err == nil {
		t.Fatal("PostConsumeQuota returned nil after a failed token write — the caller loses the inconsistency signal")
	}

	if got := userQuota(t, db, userId); got != startQuota {
		t.Errorf("user quota = %d, want %d (Phase 1 compensation)", got, startQuota)
	}

	var after repo.TenantCreditPool
	if err := db.First(&after, pool.ID).Error; err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if after.CurrentBalance != startBalance {
		t.Errorf("pool balance = %d, want %d — the Phase 2.5 debit was never handed back, so the tenant "+
			"paid for a request neither the user nor the key was charged for",
			after.CurrentBalance, startBalance)
	}

	var credits []repo.TenantCreditPoolDraw
	if err := db.Where("pool_id = ? AND direction = ?", pool.ID, repo.PoolDrawDirectionCredit).
		Find(&credits).Error; err != nil {
		t.Fatalf("read draws: %v", err)
	}
	found := false
	for _, d := range credits {
		if d.Amount == charge && strings.Contains(d.Reason, repo.PoolDrawReasonAdjustment) {
			found = true
		}
	}
	if !found {
		t.Errorf("no %q credit draw of %d found among %d credit draw(s) — the balance may be right by "+
			"accident but the append-only ledger does not explain it",
			repo.PoolDrawReasonAdjustment, charge, len(credits))
	}
}
