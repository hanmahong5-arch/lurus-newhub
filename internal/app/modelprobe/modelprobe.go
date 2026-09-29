// Package modelprobe is the active per-MODEL health prober for pooled
// channels (migration 044, entity.ModelHealth). The channel-level test
// (channels.test_time / response_time, internal/adapter/handler/channel-test.go)
// only ever exercises a channel's first or TestModel model, so the rest of a
// multi-model channel's models — an OpenRouter free pool carries ~20 — were
// never tested at all.
//
// This package holds only the decision logic (SelectDue, Apply) and the
// orchestration around it (RunOnce, Loop); the HTTP mechanics of actually
// calling a model live in internal/adapter/handler (channel-test.go's
// probeChannel machinery) and are injected in as a ProbeFunc so this package
// does not import internal/adapter/handler — the wiring in
// internal/adapter/handler/model_probe_wiring.go builds the real ProbeFunc and
// starts Loop.
package modelprobe

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
)

// Result is one probe's outcome. StatusCode is the upstream HTTP status when
// known (0 when the probe never got a response, e.g. a transport error).
type Result struct {
	OK         bool
	LatencyMs  int
	StatusCode int
	Err        string
}

// ProbeFunc actually probes one model on one channel. Injected so this
// package stays free of the relay/HTTP machinery (layering: modelprobe must
// not import internal/adapter/handler).
type ProbeFunc func(ctx context.Context, ch *repo.Channel, model string) Result

// Pair is one (channel, model) SelectDue decided is due for a probe.
type Pair struct {
	Channel *repo.Channel
	Model   string
}

// splitCSV trims and drops empty entries from a comma-separated list.
func splitCSV(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// intersects reports whether a and b share at least one element.
func intersects(a, b []string) bool {
	set := make(map[string]bool, len(b))
	for _, v := range b {
		set[v] = true
	}
	for _, v := range a {
		if set[v] {
			return true
		}
	}
	return false
}

// modelHealthKey is the map key SelectDue uses to look up the last probe
// time for a (channel, model) pair.
func modelHealthKey(channelID int, model string) string {
	return strconv.Itoa(channelID) + "\x00" + model
}

// dueCandidate is SelectDue's working record before ordering/capping.
type dueCandidate struct {
	pair        Pair
	everProbed  bool
	lastProbeAt int64
}

// SelectDue returns the ordered, capped list of (channel, model) pairs that
// are due for a probe: only channels with Status enabled whose Group CSV
// intersects setting.Groups, every trimmed/non-empty/deduped model in
// channel.Models, due when never probed or last_probe_at is at least
// IntervalHours old. Never-probed pairs sort first, then oldest
// last_probe_at first; ties break on (channel id, model) for a
// deterministic order. Capped at setting.MaxProbesPerRun.
func SelectDue(channels []*repo.Channel, health []entity.ModelHealth, cfg operation_setting.ModelProbeSetting, now time.Time) []Pair {
	allowedGroups := splitCSV(cfg.Groups)
	if len(allowedGroups) == 0 {
		return nil
	}

	lastProbe := make(map[string]int64, len(health))
	probed := make(map[string]bool, len(health))
	for _, h := range health {
		key := modelHealthKey(h.ChannelId, h.Model)
		lastProbe[key] = h.LastProbeAt
		probed[key] = true
	}

	cutoff := now.Unix() - int64(cfg.IntervalHours*3600)

	var candidates []dueCandidate
	for _, ch := range channels {
		if ch == nil || ch.Status != common.ChannelStatusEnabled {
			continue
		}
		if !intersects(splitCSV(ch.Group), allowedGroups) {
			continue
		}
		seen := make(map[string]bool)
		for _, raw := range strings.Split(ch.Models, ",") {
			model := strings.TrimSpace(raw)
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true

			key := modelHealthKey(ch.Id, model)
			last := lastProbe[key]
			wasProbed := probed[key]
			due := !wasProbed || last <= cutoff
			if !due {
				continue
			}
			candidates = append(candidates, dueCandidate{
				pair:        Pair{Channel: ch, Model: model},
				everProbed:  wasProbed,
				lastProbeAt: last,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.everProbed != b.everProbed {
			return !a.everProbed // never-probed first
		}
		if a.everProbed && a.lastProbeAt != b.lastProbeAt {
			return a.lastProbeAt < b.lastProbeAt // oldest first
		}
		if a.pair.Channel.Id != b.pair.Channel.Id {
			return a.pair.Channel.Id < b.pair.Channel.Id
		}
		return a.pair.Model < b.pair.Model
	})

	maxProbes := cfg.MaxProbesPerRun
	if maxProbes < 0 {
		maxProbes = 0
	}
	if len(candidates) > maxProbes {
		candidates = candidates[:maxProbes]
	}

	out := make([]Pair, len(candidates))
	for i, c := range candidates {
		out[i] = c.pair
	}
	return out
}
