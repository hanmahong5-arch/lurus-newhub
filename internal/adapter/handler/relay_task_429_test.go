package handler

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// A task-relay 429 caused by the request (too large) must not fail over;
// an ordinary supply-side 429 still does.
func TestShouldRetryTaskRelay_RequestCaused429(t *testing.T) {
	c, _ := newTestCtx()
	tooLarge := &dto.TaskError{StatusCode: http.StatusTooManyRequests, Message: "Request too large: tokens must be reduced"}
	if shouldRetryTaskRelay(c, 1, tooLarge, 3) {
		t.Fatal("request-caused 429 must not be retried on another channel")
	}
	plain := &dto.TaskError{StatusCode: http.StatusTooManyRequests, Message: "Rate limit reached"}
	if !shouldRetryTaskRelay(c, 1, plain, 3) {
		t.Fatal("ordinary 429 must still fail over")
	}
}
