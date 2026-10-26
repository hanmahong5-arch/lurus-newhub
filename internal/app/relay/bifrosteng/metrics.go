package bifrosteng

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	relayEngineTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "lurus",
			Subsystem: "gateway",
			Name:      "relay_engine_total",
			Help:      "Relay requests by dispatch engine (bifrost|legacy), modality, and outcome",
		},
		[]string{"engine", "modality", "status"},
	)

	bifrostDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "lurus",
			Subsystem: "gateway",
			Name:      "bifrost_duration_seconds",
			Help:      "Bifrost upstream round-trip latency in seconds",
			Buckets:   []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
		},
		[]string{"modality", "status"},
	)

	// totals mirrors relay_engine_total for the admin JSON snapshot without
	// scraping Prometheus. Per-replica process memory only.
	totals sync.Map // string("engine|modality|status") -> *atomic.Uint64
)

// EngineTotalRow is one counter cell for the admin UI.
type EngineTotalRow struct {
	Engine   string `json:"engine"`
	Modality string `json:"modality"`
	Status   string `json:"status"`
	Count    uint64 `json:"count"`
}

// ObserveEngine increments the engine counter (call from TextHelper for both paths).
func ObserveEngine(engine, modality, status string) {
	relayEngineTotal.WithLabelValues(engine, modality, status).Inc()
	key := engine + "|" + modality + "|" + status
	v, _ := totals.LoadOrStore(key, &atomic.Uint64{})
	v.(*atomic.Uint64).Add(1)
}

// observeBifrost records Bifrost path latency.
func observeBifrost(modality, status string, started time.Time) {
	bifrostDuration.WithLabelValues(modality, status).Observe(time.Since(started).Seconds())
}

// EngineTotals returns a stable-sorted snapshot of in-process counters.
func EngineTotals() []EngineTotalRow {
	rows := make([]EngineTotalRow, 0, 8)
	totals.Range(func(k, v any) bool {
		key := k.(string)
		parts := split3(key)
		if parts == nil {
			return true
		}
		rows = append(rows, EngineTotalRow{
			Engine:   parts[0],
			Modality: parts[1],
			Status:   parts[2],
			Count:    v.(*atomic.Uint64).Load(),
		})
		return true
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Engine != rows[j].Engine {
			return rows[i].Engine < rows[j].Engine
		}
		if rows[i].Modality != rows[j].Modality {
			return rows[i].Modality < rows[j].Modality
		}
		return rows[i].Status < rows[j].Status
	})
	return rows
}

func split3(s string) []string {
	a, rest, ok := strings.Cut(s, "|")
	if !ok {
		return nil
	}
	b, c, ok := strings.Cut(rest, "|")
	if !ok {
		return nil
	}
	return []string{a, b, c}
}

// ResetTotalsForTest clears in-process counters. Tests only.
func ResetTotalsForTest() {
	totals = sync.Map{}
}
