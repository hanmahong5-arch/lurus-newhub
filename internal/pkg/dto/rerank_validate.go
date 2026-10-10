package dto

import "fmt"

// UsageContradictory reports a usage block that cannot be true: a negative
// count, or prompt tokens with a zero total. Billing such a block would charge
// from numbers the vendor itself disowns, so callers reject it (502) instead
// of settling on whichever field happens to be read.
func UsageContradictory(u Usage) bool {
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 {
		return true
	}
	return u.TotalTokens == 0 && u.PromptTokens > 0
}

// CheckRerankOrder enforces the rerank contract: results come back by
// relevance_score descending. Ties are fine; a higher score after a lower one
// means the vendor (or a proxy in front of it) scrambled the ranking and the
// caller would act on a wrong top-N.
func CheckRerankOrder(results []RerankResponseResult) error {
	for i := 1; i < len(results); i++ {
		if results[i].RelevanceScore > results[i-1].RelevanceScore {
			return fmt.Errorf("rerank results not sorted by relevance_score descending at position %d (%v after %v)",
				i, results[i].RelevanceScore, results[i-1].RelevanceScore)
		}
	}
	return nil
}

// StripRerankDocuments drops the document field from every result.
func StripRerankDocuments(results []RerankResponseResult) {
	for i := range results {
		results[i].Document = nil
	}
}
