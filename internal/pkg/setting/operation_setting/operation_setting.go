package operation_setting

import (
	"strings"
	"sync"
)

var DemoSiteEnabled = false
var SelfUseModeEnabled = false

// ModelFallbackMarkup is a multiplier applied to family-based fallback ratios
// for models not in the explicit ratio map. 1.25 = 25% markup over base cost.
var ModelFallbackMarkup float64 = 1.25

// automaticDisableKeywordsMu orders the republish in
// AutomaticDisableKeywordsFromString against the reads in
// AutomaticDisableKeywordsSnapshot. The list is rewritten on every SyncOptions
// tick, and it is read while classifying an upstream error, so the two overlap
// whenever a channel is failing during a tick.
var automaticDisableKeywordsMu sync.RWMutex

// AutomaticDisableKeywords 自动停用渠道的上游错误关键词。
//
// Production code writes it through AutomaticDisableKeywordsFromString and
// reads it through AutomaticDisableKeywordsSnapshot; it stays exported because test files
// outside this package seed it directly (internal/app/channel_test.go and this
// package's cov_boot-settings_operation_setting_test.go —
// `grep -rn "AutomaticDisableKeywords" --include=*_test.go .`).
var AutomaticDisableKeywords = []string{
	"Your credit balance is too low",
	"This organization has been disabled.",
	"You exceeded your current quota",
	"Permission denied",
	"The security token included in the request is invalid",
	"Operation not allowed",
	"Your account is not authorized",
}

// AutomaticDisableKeywordsSnapshot returns the currently published list. The
// returned slice is not written again — AutomaticDisableKeywordsFromString
// replaces it rather than refilling it.
func AutomaticDisableKeywordsSnapshot() []string {
	automaticDisableKeywordsMu.RLock()
	defer automaticDisableKeywordsMu.RUnlock()

	return AutomaticDisableKeywords
}

func AutomaticDisableKeywordsToString() string {
	return strings.Join(AutomaticDisableKeywordsSnapshot(), "\n")
}

// AutomaticDisableKeywordsFromString builds the new list privately and
// publishes it in one assignment; it used to empty the live slice first, so a
// channel failing during that window was classified against a partial list and
// kept serving errors instead of being disabled.
func AutomaticDisableKeywordsFromString(s string) {
	keywords := []string{}
	ak := strings.Split(s, "\n")
	for _, k := range ak {
		k = strings.TrimSpace(k)
		k = strings.ToLower(k)
		if k != "" {
			keywords = append(keywords, k)
		}
	}

	automaticDisableKeywordsMu.Lock()
	AutomaticDisableKeywords = keywords
	automaticDisableKeywordsMu.Unlock()
}
