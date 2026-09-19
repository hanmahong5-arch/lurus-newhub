package setting

import (
	"strings"
	"sync"
)

var CheckSensitiveEnabled = true
var CheckSensitiveOnPromptEnabled = true

//var CheckSensitiveOnCompletionEnabled = true

// StopOnSensitiveEnabled 如果检测到敏感词，是否立刻停止生成，否则替换敏感词
var StopOnSensitiveEnabled = true

// StreamCacheQueueLength 流模式缓存队列长度，0表示无缓存
var StreamCacheQueueLength = 0

// sensitiveWordsMu orders the republish in SensitiveWordsFromString against
// the reads in SensitiveWordsSnapshot. The list is rewritten on every
// SyncOptions tick, not only on an admin edit, so a relay request's prompt
// check and a republish overlap routinely.
var sensitiveWordsMu sync.RWMutex

// SensitiveWords 敏感词
//
// Production code writes it through SensitiveWordsFromString and reads it
// through SensitiveWordsSnapshot; it stays exported because several test files outside
// this package seed it directly (internal/app/sensitive_test.go,
// internal/adapter/handler/relay_attribution_test.go,
// relay_sensitive_rejection_test.go and this package's
// cov_boot-settings_setting_test.go — `grep -rn "SensitiveWords" --include=*_test.go .`).
var SensitiveWords = []string{
	"test_sensitive",
}

// SensitiveWordsSnapshot returns the currently published list. The returned
// slice is not written again — SensitiveWordsFromString replaces it rather
// than refilling it — so the caller can hold it for the length of a check.
func SensitiveWordsSnapshot() []string {
	sensitiveWordsMu.RLock()
	defer sensitiveWordsMu.RUnlock()

	return SensitiveWords
}

func SensitiveWordsToString() string {
	return strings.Join(SensitiveWordsSnapshot(), "\n")
}

// SensitiveWordsFromString builds the new list privately and publishes it in
// one assignment. It used to empty the live slice and append into it word by
// word, so a check running during that window saw a shorter list — and, at the
// start of it, an empty one, which SensitiveWordContains reads as "clean".
func SensitiveWordsFromString(s string) {
	words := []string{}
	sw := strings.Split(s, "\n")
	for _, w := range sw {
		w = strings.TrimSpace(w)
		if w != "" {
			words = append(words, w)
		}
	}

	sensitiveWordsMu.Lock()
	SensitiveWords = words
	sensitiveWordsMu.Unlock()
}

func ShouldCheckPromptSensitive() bool {
	return CheckSensitiveEnabled && CheckSensitiveOnPromptEnabled
}

//func ShouldCheckCompletionSensitive() bool {
//	return CheckSensitiveEnabled && CheckSensitiveOnCompletionEnabled
//}
