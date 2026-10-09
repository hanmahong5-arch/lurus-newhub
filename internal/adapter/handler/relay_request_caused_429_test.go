package handler

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// A 429 provoked by the request itself must go straight back to the caller:
// retrying on another channel would hit the same limit and burn upstream calls.
func TestShouldRetry_RequestCaused429_NoFailover(t *testing.T) {
	c, _ := newTestCtx()
	tooLarge := types.WithOpenAIError(types.OpenAIError{
		Message: "Request too large for model x: The input or output tokens must be reduced",
		Type:    "tokens",
	}, 429)
	if shouldRetry(c, tooLarge, 3) {
		t.Fatal("request-too-large 429 must not be retried on another channel")
	}
	plain := types.WithOpenAIError(types.OpenAIError{Message: "Rate limit reached", Type: "rate_limit_error"}, 429)
	if !shouldRetry(c, plain, 3) {
		t.Fatal("ordinary rate-limit 429 must still fail over")
	}
}
