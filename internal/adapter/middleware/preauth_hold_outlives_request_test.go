package middleware

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
)

// TestPreAuthHold_OutlivesTheLongestRequestPlusRetries: a pre-auth hold the
// platform expires before we settle it is usage nobody pays for (the
// platform refuses to settle an expired hold). The hold was 300s while a
// request may run for the concurrency lease (1800s) and the settle outbox
// retried for ~2.8h. Whoever raises the lease, the retry count or the
// backoff has to raise app.PreAuthHoldTTL with it.
func TestPreAuthHold_OutlivesTheLongestRequestPlusRetries(t *testing.T) {
	longestRequest := time.Duration(concurrencyDefaultLeaseTTLSeconds) * time.Second
	need := longestRequest + app.OutboxRetryHorizon()
	if app.PreAuthHoldTTL <= need {
		t.Fatalf("PreAuthHoldTTL = %v, but a request may run %v and its settle is retried for %v more (%v total): "+
			"late settles would hit an expired hold and go unbilled", app.PreAuthHoldTTL, longestRequest, app.OutboxRetryHorizon(), need)
	}
}
