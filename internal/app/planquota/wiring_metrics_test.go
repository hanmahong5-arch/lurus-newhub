package planquota

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func cooldownCount(id int, cause string) float64 {
	return testutil.ToFloat64(metrics.ChannelCooldownTotal.WithLabelValues(strconv.Itoa(id), cause))
}

// Removing the RecordChannelCooldown calls in HandleChannelError turns this red.
func TestHandleChannelError_CountsCooldownCause(t *testing.T) {
	reset := time.Now().Add(3 * time.Hour).Unix()

	ch := planChannel(9311, KindKimiCoding)
	withSeams(t, ch)
	defaultStore.Put(context.Background(), &Snapshot{ChannelID: 9311, Kind: KindKimiCoding,
		Windows: []Window{{Window5h, 100, reset}}})
	before := cooldownCount(9311, "plan_window")
	e := apiErr(403, "forbidden", `{"error":{"type":"access_terminated_error","message":"You've reached the usage limit"}}`)
	if !HandleChannelError(types.ChannelError{ChannelId: 9311}, e) {
		t.Fatal("must be handled")
	}
	if got := cooldownCount(9311, "plan_window"); got != before+1 {
		t.Errorf("plan_window: %v -> %v, want +1", before, got)
	}

	ch = planChannel(9312, KindMiniMax)
	withSeams(t, ch)
	before = cooldownCount(9312, "balance_low")
	if !HandleChannelError(types.ChannelError{ChannelId: 9312}, apiErr(429, "x", "insufficient balance")) {
		t.Fatal("must be handled")
	}
	if got := cooldownCount(9312, "balance_low"); got != before+1 {
		t.Errorf("balance_low: %v -> %v, want +1", before, got)
	}
}
