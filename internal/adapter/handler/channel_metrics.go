package handler

// Channel-level gauges for the host Netdata scrape: state, plan window usage,
// expiry, balance and the number of models that lost every route. They are
// refreshed on a timer from the channel cache (no per-scrape query, no
// per-key label) and share one pass with the public model-status view.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/planquota"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

const channelMetricsInterval = 30 * time.Second

// Channel states exported by lurus_channel_state.
const (
	channelStateUsable  = 0
	channelStatePartial = 1
	channelStateDown    = 2
)

// stateOfHealth folds a channel health report into the 0/1/2 gauge value.
func stateOfHealth(h channelHealth) int {
	if !h.Routable {
		return channelStateDown
	}
	for _, k := range h.Keys {
		if !k.Routable {
			return channelStatePartial
		}
	}
	return channelStateUsable
}

// seriesTracker remembers which label tuples a refresh pass wrote so the next
// pass can delete the ones that disappeared (a deleted or switched-off
// channel must not keep exporting its last value forever).
type seriesTracker struct{ prev, cur map[string][]string }

func newSeriesTracker() *seriesTracker { return &seriesTracker{prev: map[string][]string{}} }

func (t *seriesTracker) begin() { t.cur = map[string][]string{} }

func (t *seriesTracker) mark(labels ...string) {
	k := ""
	for _, l := range labels {
		k += l + "\x00"
	}
	t.cur[k] = labels
}

func (t *seriesTracker) end(del func(labels ...string)) {
	for k, labels := range t.prev {
		if _, ok := t.cur[k]; !ok {
			del(labels...)
		}
	}
	t.prev = t.cur
}

var (
	trkState   = newSeriesTracker()
	trkWindow  = newSeriesTracker()
	trkExpires = newSeriesTracker()
	trkBalance = newSeriesTracker()
)

// worst folds v into m[k] keeping the larger (or, with smaller=true, the
// smaller) value: with channel_id trimmed several channels share one label.
func worst(m map[string]float64, k string, v float64, smaller bool) {
	if cur, ok := m[k]; !ok || (smaller && v < cur) || (!smaller && v > cur) {
		m[k] = v
	}
}

// RefreshChannelMetrics runs one gauge pass. It is safe to call from tests.
func RefreshChannelMetrics(now time.Time) {
	chans, err := listChannelsForMonitoring()
	if err != nil {
		common.SysLog("channel metrics: channel list unavailable, gauges kept: " + err.Error())
		return
	}
	nowUnix := now.Unix()
	state := map[string]float64{}
	expires := map[string]float64{}
	balance := map[string]float64{}
	window := map[string]float64{} // key: id + "|" + window
	for _, ch := range chans {
		if ch.Status == common.ChannelStatusManuallyDisabled {
			continue // switched off on purpose: not a fault, no series
		}
		id := metrics.TrimLabel("channel_id", strconv.Itoa(ch.Id))
		worst(state, id, float64(stateOfHealth(buildChannelHealth(ch, nowUnix))), false)
		setting := ch.GetSetting()
		if setting.ExpiresAt > 0 {
			left := float64(setting.ExpiresAt - nowUnix)
			if left < 0 {
				left = 0
			}
			worst(expires, id, left, true)
		}
		if ch.BalanceUpdatedTime > 0 {
			worst(balance, id, ch.Balance, true)
		}
		if planquota.NormalizeKind(setting.PlanKind) != "" {
			if snap, ok := planquota.LatestSnapshot(ch.Id); ok {
				for _, w := range snap.Windows {
					worst(window, id+"|"+w.Name, w.UsedPct/100, false)
				}
			}
		}
	}

	trkState.begin()
	for id, v := range state {
		metrics.ChannelState.WithLabelValues(id).Set(v)
		trkState.mark(id)
	}
	trkState.end(func(l ...string) { metrics.ChannelState.DeleteLabelValues(l...) })
	trkExpires.begin()
	for id, v := range expires {
		metrics.ChannelExpiresInSeconds.WithLabelValues(id).Set(v)
		trkExpires.mark(id)
	}
	trkExpires.end(func(l ...string) { metrics.ChannelExpiresInSeconds.DeleteLabelValues(l...) })
	trkBalance.begin()
	for id, v := range balance {
		metrics.ChannelBalance.WithLabelValues(id).Set(v)
		trkBalance.mark(id)
	}
	trkBalance.end(func(l ...string) { metrics.ChannelBalance.DeleteLabelValues(l...) })
	trkWindow.begin()
	for k, v := range window {
		i := strings.IndexByte(k, '|')
		id, name := k[:i], k[i+1:]
		metrics.ChannelPlanWindowUsedRatio.WithLabelValues(id, name).Set(v)
		trkWindow.mark(id, name)
	}
	trkWindow.end(func(l ...string) { metrics.ChannelPlanWindowUsedRatio.DeleteLabelValues(l...) })

	unroutable := 0
	for _, s := range summarizeModels(chans, nowUnix) {
		if s.Total > 0 && s.Routable == 0 {
			unroutable++
		}
	}
	metrics.ModelsUnroutable.Set(float64(unroutable))
	metrics.RefreshChannelErrorRatios(now)
}

// RunChannelMetricsLoop refreshes the gauges until ctx ends. Every replica
// runs it: each replica is scraped on its own and holds its own cooldown view.
func RunChannelMetricsLoop(ctx context.Context) {
	t := time.NewTicker(channelMetricsInterval)
	defer t.Stop()
	// The snapshot reads inside are 500 ms-bounded cache lookups; ctx only
	// governs the ticker, which is why it is not threaded into them.
	RefreshChannelMetrics(time.Now()) //nolint:contextcheck // see above
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			RefreshChannelMetrics(now) //nolint:contextcheck // see above
		}
	}
}

// listChannelsForMonitoring is the channel source of the gauges and the
// public status view; a seam so tests need no database.
var listChannelsForMonitoring = repo.ChannelsForMonitoring
