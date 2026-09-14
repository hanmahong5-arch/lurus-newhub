package relay

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// contextTierScanDirs are walked (non-recursively) by
// TestContextTierSettlement_EverySettlementSiteCallsResettle to DERIVE the
// set of functions that read a ".ModelRatio" field, rather than trust a
// hand-maintained list of call sites (cycle-8 plan §8 L5 B-F4: a hardcoded
// four-entry list can never flag a fifth site that reads
// PriceData.ModelRatio without resettling). "." is this test file's own
// directory (internal/app/relay); ".." is internal/app; "helper" holds the
// price computation and the customer-facing cost surface (perception.go);
// "../../adapter/handler" holds the channel-test billing path, which writes a
// consume-log row of its own.
var contextTierScanDirs = []string{".", "..", "helper", filepath.Join("..", "..", "adapter", "handler")}

// contextTierExpectedPresent are the settlement sites already known to call
// helper.ResettleContextTier before reading PriceData.ModelRatio for a
// !UsePrice model. Kept as an expected-present set (not just an allow-list)
// so the test also fails if one of these is renamed or the scan otherwise
// stops finding it — not only when a NEW unresettled site appears.
var contextTierExpectedPresent = map[string]bool{
	"compatible_handler.go:postConsumeQuota": true,
	"quota.go:PostClaudeConsumeQuota":        true,
	"quota.go:PostWssConsumeQuota":           true,
	"quota.go:PostAudioConsumeQuota":         true,
}

// contextTierExemptFuncs lists top-level functions in the scanned
// directories whose (comment-stripped) body mentions a ".ModelRatio" field
// access for a reason other than settling a final quota straight off
// relayInfo/info.PriceData, together with why each is safe to skip instead
// of calling helper.ResettleContextTier itself.
var contextTierExemptFuncs = map[string]string{
	"quota.go:calculateAudioQuota": "receives ModelRatio via QuotaInfo.ModelRatio from its two callers " +
		"(PostWssConsumeQuota, PostAudioConsumeQuota — both in the expected-present set), which have " +
		"already resettled relayInfo.PriceData before building the QuotaInfo passed in; not a direct " +
		"PriceData reader.",
	"quota.go:CalcOpenRouterCacheCreateTokens": "receives an already-resettled types.PriceData by value " +
		"from its only caller, PostClaudeConsumeQuota (in the expected-present set), which calls it after " +
		"its own helper.ResettleContextTier call earlier in the same function.",
	"price.go:ModelPriceHelper": "produces the PriceData the settlement sites later resettle: it picks the " +
		"pre-consume tier from the estimated prompt tokens and writes the Base* snapshot ResettleContextTier " +
		"reads. Calling ResettleContextTier here would re-run the selection it just performed.",
	"perception.go:ComputeLurusExtension": "reads relayInfo.PriceData for the response's cost extension after " +
		"EstimateQuotaFromUsage — which does call helper.ResettleContextTier — has run on the same RelayInfo. " +
		"Each of its call sites (openai/helper.go, openai/relay-openai.go twice) invokes the two in that order " +
		"on the same info and usage, so the PriceData it reads is already settled.",
}

// contextTierFuncHeaderRE matches a top-level Go function declaration
// ("func Name(" or "func (recv Type) Name(") at the start of a line, after
// comment-stripping.
var contextTierFuncHeaderRE = regexp.MustCompile(`(?m)^func (?:\([^)]*\)\s*)?(\w+)\(`)

// extractTopLevelFuncs returns every top-level function in path keyed by
// name, with the body running from its own "func " line up to (but not
// including) the next top-level "func " line (or EOF), and every "//"
// line-comment stripped — otherwise a comment merely mentioning the call
// would satisfy a substring check even after the real call is deleted (see
// funcSource's doc comment below for how this bit the lane once already).
// Heuristic, not an AST parse: a "//" inside a string literal on the same
// line would truncate that line early. Acceptable for the two directories
// this is scoped to (cycle-8 plan §8 L5 B-F4) — same trade-off funcSource
// below already made for its four files.
func extractTopLevelFuncs(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	stripped := strings.Join(lines, "\n")

	matches := contextTierFuncHeaderRE.FindAllStringSubmatchIndex(stripped, -1)
	funcs := make(map[string]string, len(matches))
	for i, m := range matches {
		start, nameStart, nameEnd := m[0], m[2], m[3]
		end := len(stripped)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		funcs[stripped[nameStart:nameEnd]] = stripped[start:end]
	}
	return funcs
}

// TestContextTierSettlement_EverySettlementSiteCallsResettle is the cycle-8
// plan §8 L5 structural lock: every top-level function in the scanned
// directories whose body reads a ".ModelRatio" field must either call
// helper.ResettleContextTier itself or be named in contextTierExemptFuncs
// with a reason it is safe not to. This DERIVES the site list from the
// source tree instead of trusting a fixed set, so a fifth settlement site
// added later (in these two directories) cannot ship unresettled and silent
// — it will show up as neither present-and-resettling nor exempt.
// contextTierExpectedPresent additionally pins the four sites already known
// today, so the test also fails if the scan stops finding one of them.
func TestContextTierSettlement_EverySettlementSiteCallsResettle(t *testing.T) {
	found := map[string]bool{}
	for _, dir := range contextTierScanDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read dir %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			funcs := extractTopLevelFuncs(t, filepath.Join(dir, name))
			for funcName, body := range funcs {
				// Narrow to functions that read a .ModelRatio field off a
				// PriceData value: the scanned handler directory is full of
				// pricing-CONFIG functions (admin write/read handlers, upstream
				// ratio sync) that read a .ModelRatio field of a request DTO or
				// a ratio_setting row, which settle no money and have no
				// PriceData in hand.
				// Case-insensitive on the type name so a local named priceData
				// counts as well as a field access on types.PriceData.
				if !strings.Contains(body, ".ModelRatio") || !strings.Contains(strings.ToLower(body), "pricedata") {
					continue
				}
				key := name + ":" + funcName
				found[key] = true
				if _, exempt := contextTierExemptFuncs[key]; exempt {
					continue
				}
				if !strings.Contains(body, "ResettleContextTier(") {
					t.Errorf("%s: reads a .ModelRatio field but calls neither helper.ResettleContextTier nor "+
						"appears in contextTierExemptFuncs with a reason — either wire it or add an exemption entry",
						key)
				}
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("scan found zero functions reading .ModelRatio — the scan itself is broken (dirs/pattern changed?)")
	}
	for key := range contextTierExpectedPresent {
		if !found[key] {
			t.Errorf("expected-present settlement site %s was not found by the scan (renamed, removed, or moved out of the scanned directories?)", key)
		}
	}
}

// TestContextTierSettlement_ActualAboveThresholdUsesHigherTier drives the
// real postConsumeQuota settlement path (not a hand-built ResettleContextTier
// call): the pre-consume estimate lands below a configured threshold
// (PriceData.ModelRatio=1, as ModelPriceHelper would have left it), the
// upstream-reported usage.PromptTokens lands above it, and the debited quota
// must reflect the higher tier's ratio — matching what a flat ModelRatio=2
// config would have billed for the same usage.
func TestContextTierSettlement_ActualAboveThresholdUsesHigherTier(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	if err := ratio_setting.UpdateContextLengthTiersByJSONString(
		`{"tier-settle-model":[{"threshold_tokens":0,"model_ratio":1.0},{"threshold_tokens":3000,"model_ratio":2.0}]}`,
	); err != nil {
		t.Fatalf("seed context tiers: %v", err)
	}

	const startQuota = 100_000_000

	// Tiered call: pre-consume estimate picked the base tier (ModelRatio=1,
	// as if the estimate was below 3000), actual usage is 4000 prompt tokens.
	uTiered := &repo.User{Username: "ctx-tier-actual", Quota: startQuota}
	if err := repo.DB.Create(uTiered).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	infoTiered := newRelayInfo(uTiered.Id, 0, constant.APITypeOpenAI)
	infoTiered.OriginModelName = "tier-settle-model"
	infoTiered.IsPlayground = true
	infoTiered.UserQuota = startQuota
	infoTiered.PriceData = types.PriceData{
		ModelRatio:     1.0, // pre-consume estimate's (wrong, now-stale) tier
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		// The frozen snapshot ModelPriceHelper would have written alongside the
		// chosen tier; ResettleContextTier starts from it instead of re-reading
		// the live maps, and skips a PriceData that carries none.
		BaseModelRatio: 1.0,
		BaseRatiosSet:  true,
	}
	cTiered, _ := newJSONContext(http.MethodPost, "/", nil)
	cTiered.Set("token_name", "tkn")
	usageTiered := &dto.Usage{PromptTokens: 4000, CompletionTokens: 0, TotalTokens: 4000}
	postConsumeQuota(cTiered, infoTiered, usageTiered)

	var refreshedTiered repo.User
	if err := repo.DB.First(&refreshedTiered, uTiered.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	debitedTiered := startQuota - refreshedTiered.Quota

	// Control: a model with no tiers at all, PriceData.ModelRatio set
	// directly to the higher tier's ratio (2.0), same usage — this is what
	// the tiered call above should have billed if the resettle applied.
	uFlat := &repo.User{Username: "ctx-tier-flat-control", Quota: startQuota}
	if err := repo.DB.Create(uFlat).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	infoFlat := newRelayInfo(uFlat.Id, 0, constant.APITypeOpenAI)
	infoFlat.OriginModelName = "flat-control-model" // never given tiers
	infoFlat.IsPlayground = true
	infoFlat.UserQuota = startQuota
	infoFlat.PriceData = types.PriceData{
		ModelRatio:     2.0,
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	cFlat, _ := newJSONContext(http.MethodPost, "/", nil)
	cFlat.Set("token_name", "tkn")
	usageFlat := &dto.Usage{PromptTokens: 4000, CompletionTokens: 0, TotalTokens: 4000}
	postConsumeQuota(cFlat, infoFlat, usageFlat)

	var refreshedFlat repo.User
	if err := repo.DB.First(&refreshedFlat, uFlat.Id).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	debitedFlat := startQuota - refreshedFlat.Quota

	if debitedTiered <= 0 {
		t.Fatalf("tiered call debited %d, want > 0", debitedTiered)
	}
	if debitedTiered != debitedFlat {
		t.Errorf("tiered-call debit = %d, flat ModelRatio=2.0 control debit = %d — want equal (settlement must re-evaluate the tier against the actual 4000 prompt tokens, not bill at the stale pre-consume estimate's ModelRatio=1.0)",
			debitedTiered, debitedFlat)
	}

	if infoTiered.PriceData.ModelRatio != 2.0 {
		t.Errorf("infoTiered.PriceData.ModelRatio after settlement = %v, want 2.0 (resettled)", infoTiered.PriceData.ModelRatio)
	}
	if infoTiered.PriceData.ContextTierThreshold != 3000 {
		t.Errorf("infoTiered.PriceData.ContextTierThreshold after settlement = %d, want 3000", infoTiered.PriceData.ContextTierThreshold)
	}
}

// TestContextTierSettlement_FullContextLengthIncludesCachedTokens is the
// cycle-8 plan §8 L5 B-F1 oracle: the tier must be chosen on the FULL context
// length, not on the wire's raw prompt-token field. On the Anthropic wire
// (usage.PromptTokensIncludeCached=false) that field excludes cache-read and
// cache-creation tokens, so a long-context call that is mostly a cache hit
// must still select the surcharge tier once the cached slices are added
// back — a fixed prompt of 1000 tokens plus 4500 cached tokens (5500 total)
// must clear a 5000-token threshold, even though the raw PromptTokens field
// alone (1000) would not.
func TestContextTierSettlement_FullContextLengthIncludesCachedTokens(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	prevTiers := ratio_setting.ContextLengthTiers2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateContextLengthTiersByJSONString(prevTiers) })
	if err := ratio_setting.UpdateContextLengthTiersByJSONString(
		`{"cache-aware-tier-model":[{"threshold_tokens":0,"model_ratio":1.0},{"threshold_tokens":5000,"model_ratio":2.0}]}`,
	); err != nil {
		t.Fatalf("seed context tiers: %v", err)
	}

	const startQuota = 100_000_000
	u := &repo.User{Username: "ctx-tier-cache-aware", Quota: startQuota}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	info := newRelayInfo(u.Id, 0, constant.APITypeOpenAI)
	info.OriginModelName = "cache-aware-tier-model"
	info.IsPlayground = true
	info.UserQuota = startQuota
	info.PriceData = types.PriceData{
		ModelRatio:     1.0, // pre-consume estimate landed on the base tier
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		BaseModelRatio: 1.0,
		BaseRatiosSet:  true,
	}
	c, _ := newJSONContext(http.MethodPost, "/", nil)
	c.Set("token_name", "tkn")
	// Anthropic-wire shape: PromptTokens (1000) EXCLUDES the 4500 cached
	// tokens; the full context length is 1000+4500=5500, which clears the
	// 5000 threshold. If settlement used usage.PromptTokens alone it would
	// stay on the base tier.
	usage := &dto.Usage{
		PromptTokens:              1000,
		CompletionTokens:          0,
		TotalTokens:               1000,
		PromptTokensIncludeCached: false,
		PromptTokensDetails:       dto.InputTokenDetails{CachedTokens: 4500},
	}
	postConsumeQuota(c, info, usage)

	if info.PriceData.ModelRatio != 2.0 {
		t.Errorf("ModelRatio after settlement = %v, want 2.0 (full context length 1000+4500=5500 clears the 5000 threshold)", info.PriceData.ModelRatio)
	}
	if info.PriceData.ContextTierThreshold != 5000 {
		t.Errorf("ContextTierThreshold after settlement = %d, want 5000", info.PriceData.ContextTierThreshold)
	}
}
