package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// InternalAuthLegacyHeaderTotal counts /internal requests that authenticated
// with the deprecated `X-API-Key` header instead of `Authorization: Bearer`
// (middleware.InternalApiAuth). It exists to answer one question before the
// legacy header is removed: is anyone still sending it? A series that has not
// moved across a full release window of every caller is the evidence.
//
// Labels:
//
//	route  — the gin route template (c.FullPath, e.g. /internal/user/:id),
//	         never the raw path, so ids in the URL cannot explode cardinality.
//	key_id — the internal_api_keys row that authenticated, so the remaining
//	         legacy callers can be named. Bounded by the number of issued keys.
//
// Only successful legacy authentications are counted: a rejected request
// says nothing about which header a real caller uses. No series is
// pre-registered — the labels are not known in advance — so an absent series
// means "no legacy-header request since this process started".
var InternalAuthLegacyHeaderTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "internal_auth_legacy_header_total",
		Help:      "Internal API requests authenticated via the deprecated X-API-Key header instead of Authorization: Bearer, by route template and key id",
	},
	[]string{"route", "key_id"},
)

// RecordInternalAuthLegacyHeader increments InternalAuthLegacyHeaderTotal. An
// empty route (no matched template) is recorded as "unmatched".
func RecordInternalAuthLegacyHeader(route string, keyID int) {
	if route == "" {
		route = "unmatched"
	}
	InternalAuthLegacyHeaderTotal.WithLabelValues(route, strconv.Itoa(keyID)).Inc()
}
