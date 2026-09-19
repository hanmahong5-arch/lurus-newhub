package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// OptionParseRejectedTotal counts option values that could not be applied and
// were therefore left at their previous value, labelled by the option key.
//
// Two ways a value reaches repo.updateOptionMap: an admin write
// (PUT /api/option) and the SyncOptions tick re-reading the options table on
// every replica. Before the keep-previous change the numeric keys were parsed
// as `x, _ = strconv.Parse*(value)`, so a blank or mistyped value stored zero
// — QuotaPerUnit 0 makes every quota-to-currency number the console and
// /api/status show read 0 — and the write path recorded nothing about it.
// It now keeps the old value and increments this.
//
// It also counts a rejected hierarchical value (the "<config>.<field>" keys
// handled by handleConfigUpdate: a malformed JSON map for gemini.safety_
// settings, fetch_setting.domain_list and the rest) and a write to a retired
// key that is refused outright.
//
// A non-zero value means the persisted row and the running value disagree for
// that key, on this replica, until someone writes a value that parses.
var OptionParseRejectedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "option_parse_rejected_total",
		Help:      "Option values that failed to parse and left the previous value in place, by option key",
	},
	[]string{"key"},
)

// RecordOptionParseRejected increments the rejected-value counter for one
// option key.
func RecordOptionParseRejected(key string) {
	OptionParseRejectedTotal.WithLabelValues(key).Inc()
}
