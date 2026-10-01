package ratio_setting

import "slices"

// systemOneInputPriceUSDPerM is the hosted TypeSafe list price: $0.042 per 1M
// input tokens, output free (the relay bills input tokens only).
const systemOneInputPriceUSDPerM = 0.042

// systemOneModels are the names the System One channels serve (kept in step
// with the systemone adaptor's ModelList; this package cannot import it).
var systemOneModels = []string{
	"jev-latest", "jev-preview", "jev-1.13.0",
	"laya-auto", "laya-english", "laya-multilingual", "laya-typed-decisions",
}

// init merges the System One prices into defaultModelRatio from here rather
// than the literal in model_ratio.go, which is under the source-size ratchet.
// Package-level variable initialisation finishes before any init() runs, so
// the map exists. Ratio 1 = $2 / 1M input tokens.
func init() {
	for _, name := range systemOneModels {
		defaultModelRatio[name] = systemOneInputPriceUSDPerM / 2
	}
}

// systemOneDefaultRatio is a System One model's list price when the live table
// has no row for it. The live table is replaced wholesale by the persisted
// ModelRatio option, so on any deployment where an operator has ever saved
// prices the default rows above never reach it, and the first request after a
// System One channel is added would fail with "ratio or price not set". An
// operator's own row still wins: this is consulted only on a miss.
func systemOneDefaultRatio(name string) (float64, bool) {
	if slices.Contains(systemOneModels, name) {
		return systemOneInputPriceUSDPerM / 2, true
	}
	return 0, false
}
