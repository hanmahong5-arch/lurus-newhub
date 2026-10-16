package handler

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/app/routingfallback"
	"github.com/LurusTech/lurus-hub/internal/pkg/resilience"
)

// bvA..bvC are channel ids no other test in the package uses; channelBreakers
// is a process-global registry.
const (
	bvA = 987001
	bvB = 987002
	bvC = 987003
)

func bvReset(t *testing.T) {
	t.Helper()
	channelBreakers.Cleanup(map[int]struct{}{})
	t.Cleanup(func() { channelBreakers.Cleanup(map[int]struct{}{}) })
}

func bvTrip(id int) {
	for i := 0; i < resilience.DefaultConfig().Threshold; i++ {
		channelBreakers.RecordFailure(id)
	}
}

func TestBreakerView_IsRegisteredForTheFallbackStrategy(t *testing.T) {
	if routingfallback.BreakerAllOpen == nil {
		t.Fatal("handler did not register the breaker view: fallback routing would never see an open breaker")
	}
}

func TestBreakerView_AllOpenSemantics(t *testing.T) {
	bvReset(t)
	view := routingfallback.BreakerAllOpen

	if open, _ := view(nil); open {
		t.Error("an empty channel list is never all-open")
	}
	if open, _ := view([]int{bvA}); open {
		t.Error("a channel that never failed is not open")
	}

	bvTrip(bvA)
	if channelBreakers.GetState(bvA) != resilience.StateOpen {
		t.Fatal("setup: breaker did not trip")
	}
	open, retry := view([]int{bvA})
	if !open {
		t.Error("a tripped channel must read as open")
	}
	if retry <= 0 || retry > 2*resilience.DefaultConfig().Timeout {
		t.Errorf("retry-after = %v, want within the breaker cooldown", retry)
	}
	if open, _ := view([]int{bvA, bvB}); open {
		t.Error("one channel with no breaker means the set is not all-open")
	}

	bvTrip(bvB)
	if open, _ := view([]int{bvA, bvB}); !open {
		t.Error("every channel tripped: the set must be all-open")
	}
	channelBreakers.RecordInconclusive(bvC)
	if open, _ := view([]int{bvA, bvB, bvC}); open {
		t.Error("a closed breaker in the set means not all-open")
	}
	channelBreakers.RecordSuccess(bvA)
	if open, _ := view([]int{bvA, bvB}); open {
		t.Error("a recovered channel must end the all-open state")
	}
}

// Reading the view is a status read: it must not create breakers or move a
// state machine (that would consume the half-open probe slot).
func TestBreakerView_IsSideEffectFree(t *testing.T) {
	bvReset(t)
	before := len(channelBreakers.Snapshot())
	routingfallback.BreakerAllOpen([]int{bvA, bvB, bvC})
	if after := len(channelBreakers.Snapshot()); after != before {
		t.Fatalf("view created %d breaker(s)", after-before)
	}
}
