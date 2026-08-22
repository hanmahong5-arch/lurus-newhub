package ratio_setting

import "strings"

// ── Workstream 0: product / source attribution ──────────────────────────────
//
// Every relay used to report productId="lurus-api", making "which product spent
// the money" unknowable. We resolve a source tag per relay and propagate it to
// the wallet (productId) and the log row (Other.source_product).

// SourceProductHeader is the inbound header a caller sets to attribute its
// LLM spend to a product. Validated against the allow-list below — an
// unrecognized value is never trusted (CLAUDE.md boundary validation).
const SourceProductHeader = "X-Lurus-Product"

// DefaultSourceProduct is the fallback used when no allow-listed header is
// present. It must name a row in the platform's identity.products — the value
// is transmitted as the wallet debit's product_id, and an id the catalog does
// not know is rejected as unattributed once the platform's
// billing.require_product_attribution ratchet is on. The relay's historical
// hardcoded "lurus-api" was never such a row; the seeded one is "llm-api"
// (platform migrations/003_seed_products.sql).
const DefaultSourceProduct = "llm-api"

// allowedSourceProducts is the newhub-side allow-list of product ids that may
// appear on relay traffic. The platform wallet already accepts arbitrary
// product_id (lucrum/creator pass their own); this set bounds what newhub will
// emit so a forged header can't inject an unknown attribution key. Extend as
// products onboard to the hub.
var allowedSourceProducts = map[string]bool{
	DefaultSourceProduct: true,
	"kova":               true,
	"lutu":               true,
	"lucrum":             true,
	"switch":             true,
	"creator":            true,
	"memorus":            true,
	"tally":              true,
}

// ResolveSourceProduct maps an inbound X-Lurus-Product header value to a trusted
// product id. Priority per the attribution plan is (1) allow-listed header,
// (2) token-level default, (3) DefaultSourceProduct. Tier (2) is deferred until
// a token column exists (no migration in this plan), so callers pass only the
// header today; an empty or unknown header falls back to the default.
func ResolveSourceProduct(header string) string {
	h := strings.ToLower(strings.TrimSpace(header))
	if h != "" && allowedSourceProducts[h] {
		return h
	}
	return DefaultSourceProduct
}

// ── Phase 1: model equivalence (savings analyzer) ───────────────────────────

// ModelEquivalence describes a curated, clearly-heuristic mapping from a model
// to substitutes the cost-aware router could use. Quality tiers are NOT
// eval-backed — they exist to motivate building the eval-backed quality
// observer, and are surfaced as heuristic in the UI caveat.
type ModelEquivalence struct {
	Family      string // provider family, e.g. "gpt", "claude", "gemini"
	QualityTier string // heuristic grade, "S" > "A" > "B" > "C"
	// Reasoning marks extended-thinking models. They are excluded as a
	// re-pricing SOURCE (their output format makes a what-if substitution
	// unsafe), so they carry no substitutes.
	Reasoning bool
	// ConservativeSub is a same-family cheaper substitute (e.g. gpt-4o→gpt-4o-mini).
	// "" = no cheaper same-family model.
	ConservativeSub string
	// AggressiveSub is a same-tier cross-vendor cheaper substitute
	// (e.g. gpt-4o→deepseek-chat). "" = none.
	AggressiveSub string
}

// modelEquivalence seeds the top models by spend. Mirrors model_family.go's
// static-map precedent. Invariant (asserted in model_equivalence_test.go):
// every non-empty substitute resolves to a real (non-zero) GetModelRatio and is
// never the model itself.
var modelEquivalence = map[string]ModelEquivalence{
	// gpt family
	"gpt-4o":            {Family: "gpt", QualityTier: "A", ConservativeSub: "gpt-4o-mini", AggressiveSub: "deepseek-chat"},
	"gpt-4o-2024-11-20": {Family: "gpt", QualityTier: "A", ConservativeSub: "gpt-4o-mini", AggressiveSub: "deepseek-chat"},
	"gpt-4o-2024-08-06": {Family: "gpt", QualityTier: "A", ConservativeSub: "gpt-4o-mini", AggressiveSub: "deepseek-chat"},
	"gpt-4o-mini":       {Family: "gpt", QualityTier: "B", AggressiveSub: "gemini-2.0-flash"},
	"gpt-4.1":           {Family: "gpt", QualityTier: "A", ConservativeSub: "gpt-4.1-mini", AggressiveSub: "deepseek-chat"},
	"gpt-4.1-mini":      {Family: "gpt", QualityTier: "B", ConservativeSub: "gpt-4.1-nano", AggressiveSub: "gemini-2.0-flash"},
	"gpt-5":             {Family: "gpt", QualityTier: "A", ConservativeSub: "gpt-5-mini", AggressiveSub: "deepseek-chat"},
	"gpt-5-mini":        {Family: "gpt", QualityTier: "B", ConservativeSub: "gpt-5-nano", AggressiveSub: "gemini-2.0-flash"},

	// claude family
	"claude-3-5-sonnet-20241022": {Family: "claude", QualityTier: "A", ConservativeSub: "claude-3-5-haiku-20241022", AggressiveSub: "deepseek-chat"},
	"claude-3-7-sonnet-20250219": {Family: "claude", QualityTier: "A", ConservativeSub: "claude-3-5-haiku-20241022", AggressiveSub: "deepseek-chat"},
	"claude-sonnet-4-5-20250929": {Family: "claude", QualityTier: "A", ConservativeSub: "claude-haiku-4-5-20251001", AggressiveSub: "gemini-2.5-pro"},
	"claude-opus-4-5-20251101":   {Family: "claude", QualityTier: "S", ConservativeSub: "claude-sonnet-4-5-20250929", AggressiveSub: "gpt-4.1"},
	"claude-3-5-haiku-20241022":  {Family: "claude", QualityTier: "B", AggressiveSub: "gemini-2.0-flash"},

	// gemini family
	"gemini-2.5-pro":   {Family: "gemini", QualityTier: "A", ConservativeSub: "gemini-2.5-flash", AggressiveSub: "deepseek-chat"},
	"gemini-2.5-flash": {Family: "gemini", QualityTier: "B", ConservativeSub: "gemini-2.5-flash-lite-preview-06-17", AggressiveSub: "gpt-4o-mini"},

	// deepseek family (chat is already a cheap tier-A; reasoner is reasoning)
	"deepseek-chat":     {Family: "deepseek", QualityTier: "A"},
	"deepseek-reasoner": {Family: "deepseek", QualityTier: "A", Reasoning: true},

	// reasoning models — recognized so they're excluded with the right reason
	"o1": {Family: "o1", QualityTier: "S", Reasoning: true},
	"o3": {Family: "o3", QualityTier: "A", Reasoning: true},
}

// GetModelEquivalence returns the curated equivalence for a model (after name
// normalization), or (zero, false) if the model is not in the curated map.
func GetModelEquivalence(model string) (ModelEquivalence, bool) {
	eq, ok := modelEquivalence[FormatMatchingModelName(model)]
	return eq, ok
}

// ModelEquivalenceMap returns a copy of the curated map for read-only
// iteration (used by tests and any future introspection endpoint).
func ModelEquivalenceMap() map[string]ModelEquivalence {
	out := make(map[string]ModelEquivalence, len(modelEquivalence))
	for k, v := range modelEquivalence {
		out[k] = v
	}
	return out
}
