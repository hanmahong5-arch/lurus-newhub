package types

// LurusUsageExtension carries cost and billing metadata in API responses.
// Attached to usage.x_lurus in OpenAI-compatible responses.
type LurusUsageExtension struct {
	CostLB           float64 `json:"cost_lb"`
	ModelRatio       float64 `json:"model_ratio"`
	GroupRatio       float64 `json:"group_ratio"`
	CachedTokens     int     `json:"cached_tokens,omitempty"`
	BalanceRemaining float64 `json:"balance_remaining"`
	BillingMode      string  `json:"billing_mode"`

	// ChargedUSD is the same charge as CostLB in USD credits (quota /
	// QuotaPerUnit), the unit /api/v1/models prices in. It is written to the
	// OpenRouter-standard usage.cost field, not to x_lurus.
	ChargedUSD float64 `json:"-"`
}
