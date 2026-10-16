package routingfallback

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func policy(models ...string) *entity.RoutingPolicy {
	p := &entity.RoutingPolicy{TenantID: "t1", PublicModel: "primary-a", Strategy: entity.RoutingStrategyFallback, Enabled: true}
	for i, m := range models {
		p.Candidates = append(p.Candidates, entity.RoutingCandidate{ID: "c" + string(rune('a'+i)), Model: m})
	}
	return p
}

// fakeWorld is a channel map plus a set of channel ids whose breaker is open.
type fakeWorld struct {
	channels map[string][]int
	open     map[int]bool
}

func (w fakeWorld) install(t *testing.T) func(string) []int {
	t.Helper()
	prev := BreakerAllOpen
	BreakerAllOpen = func(ids []int) (bool, time.Duration) {
		if len(ids) == 0 {
			return false, 0
		}
		for _, id := range ids {
			if !w.open[id] {
				return false, 0
			}
		}
		return true, 30 * time.Second
	}
	t.Cleanup(func() { BreakerAllOpen = prev })
	return func(model string) []int { return w.channels[model] }
}

func TestResolve_EveryReason(t *testing.T) {
	permitAll := func(string) bool { return true }
	cases := []struct {
		name       string
		world      fakeWorld
		pol        *entity.RoutingPolicy
		permitted  func(string) bool
		wantReason string
		wantTarget string
	}{
		{
			name:       "primary healthy -> no rewrite",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1}, "model-a": {2}}},
			pol:        policy("model-a"),
			wantReason: ReasonPrimaryOK,
		},
		{
			name:       "one of two primary channels still closed -> primary_ok",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1, 3}, "model-a": {2}}, open: map[int]bool{1: true}},
			pol:        policy("model-a"),
			wantReason: ReasonPrimaryOK,
		},
		{
			name:       "no channel at all for the primary",
			world:      fakeWorld{channels: map[string][]int{"model-a": {2}}},
			pol:        policy("model-a"),
			wantReason: ReasonNoChannel,
			wantTarget: "model-a",
		},
		{
			name:       "every primary channel open",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1, 3}, "model-a": {2}}, open: map[int]bool{1: true, 3: true}},
			pol:        policy("model-a"),
			wantReason: ReasonPrimaryUnavailable,
			wantTarget: "model-a",
		},
		{
			name:       "first candidate is itself down -> second wins, order respected",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1}, "model-a": {2}, "model-b": {4}, "model-c": {5}}, open: map[int]bool{1: true, 2: true}},
			pol:        policy("model-a", "model-b", "model-c"),
			wantReason: ReasonPrimaryUnavailable,
			wantTarget: "model-b",
		},
		{
			name:       "first candidate has no channel -> skipped",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1}, "model-b": {4}}, open: map[int]bool{1: true}},
			pol:        policy("model-a", "model-b"),
			wantReason: ReasonPrimaryUnavailable,
			wantTarget: "model-b",
		},
		{
			name:       "candidate the caller may not use is skipped",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1}, "model-a": {2}, "model-b": {4}}, open: map[int]bool{1: true}},
			pol:        policy("model-a", "model-b"),
			permitted:  func(m string) bool { return m != "model-a" },
			wantReason: ReasonPrimaryUnavailable,
			wantTarget: "model-b",
		},
		{
			name:       "primary down and every candidate down -> no_candidate",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1}, "model-a": {2}}, open: map[int]bool{1: true, 2: true}},
			pol:        policy("model-a"),
			wantReason: ReasonNoCandidate,
		},
		{
			name:       "no channel and no candidate has one -> no_candidate",
			world:      fakeWorld{channels: map[string][]int{}},
			pol:        policy("model-a", "model-b"),
			wantReason: ReasonNoCandidate,
		},
		{
			name:       "a candidate equal to the primary is never chosen",
			world:      fakeWorld{channels: map[string][]int{"primary-a": {1}}, open: map[int]bool{1: true}},
			pol:        policy("primary-a"),
			wantReason: ReasonNoCandidate,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := tc.world.install(t)
			perm := tc.permitted
			if perm == nil {
				perm = permitAll
			}
			out := Resolve(tc.pol, "primary-a", ids, perm)
			if out.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", out.Reason, tc.wantReason)
			}
			if out.Target != tc.wantTarget {
				t.Fatalf("target = %q, want %q", out.Target, tc.wantTarget)
			}
		})
	}
}

func TestResolve_NoBreakerSeamMeansPrimaryOK(t *testing.T) {
	prev := BreakerAllOpen
	BreakerAllOpen = nil
	t.Cleanup(func() { BreakerAllOpen = prev })
	out := Resolve(policy("model-a"), "primary-a", func(m string) []int { return []int{1} }, func(string) bool { return true })
	if out.Reason != ReasonPrimaryOK || out.Target != "" {
		t.Fatalf("with no breaker view wired nothing may be rewritten, got %+v", out)
	}
}

func TestGroupsFor(t *testing.T) {
	if got := GroupsFor("default", "default", nil); len(got) != 1 || got[0] != "default" {
		t.Fatalf("plain group: %v", got)
	}
	auto := func(userGroup string) []string { return []string{"g1", "g2"} }
	if got := GroupsFor("auto", "vip", auto); len(got) != 2 || got[0] != "g1" || got[1] != "g2" {
		t.Fatalf("auto group must expand to the user's auto groups: %v", got)
	}
	if got := GroupsFor("", "vip", auto); len(got) != 0 {
		t.Fatalf("empty group must expand to nothing: %v", got)
	}
}

func TestChannelIDsAcrossGroupsDedupes(t *testing.T) {
	look := func(tenant, group, model string) []int {
		switch group {
		case "g1":
			return []int{1, 2}
		case "g2":
			return []int{2, 3}
		}
		return nil
	}
	got := UnionChannelIDs("t1", []string{"g1", "g2"}, "m", look)
	if len(got) != 3 {
		t.Fatalf("want 3 distinct ids, got %v", got)
	}
}

func TestRecordCountsPerReason(t *testing.T) {
	before := testutil.ToFloat64(fallbackTotal.WithLabelValues(ReasonNoChannel))
	Record(ReasonNoChannel)
	if got := testutil.ToFloat64(fallbackTotal.WithLabelValues(ReasonNoChannel)); got != before+1 {
		t.Fatalf("counter = %v, want %v", got, before+1)
	}
	// Unknown reasons collapse into one bucket instead of minting a series.
	Record("tenant-controlled-garbage")
	if testutil.CollectAndCount(fallbackTotal) != len(Reasons) {
		t.Fatalf("series count = %d, want the closed set of %d", testutil.CollectAndCount(fallbackTotal), len(Reasons))
	}
}
