package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// OptionParseRejectedTotal counts option values that could not be parsed,
// labelled by the option key.
//
// Two ways a value gets here, and they mean different things:
//
//   - An admin write (PUT /api/option). repo.UpdateOption parses before it
//     persists, so the row is NOT written and the running value is untouched;
//     the caller gets a 400. One increment per refused write.
//   - The SyncOptions tick re-reading the options table on every replica, for
//     a row that was stored before this check existed or written by some other
//     means. The previous value is kept — before the keep-previous change the
//     numeric keys were parsed as `x, _ = strconv.Parse*(value)`, so a blank or
//     mistyped value stored zero, and QuotaPerUnit 0 makes every
//     quota-to-currency number in the console and in /api/status read 0. This
//     case increments once per key per tick per replica, because the row and
//     the running value really do disagree until somebody fixes the row.
//
// So a single spike is a refused admin write (nothing is broken), and a
// sustained rate is a stored row the engine cannot apply (something is). An
// alarm on this should fire on "nonzero for N minutes", not on any increase.
//
// It counts both the flat numeric keys and a rejected hierarchical value (the
// "<config>.<field>" keys: a malformed JSON map for gemini.safety_settings,
// fetch_setting.domain_list and the rest). It deliberately does NOT count a
// retired key (repo's retiredOptionKeys): that row is ignored by design and
// reported once per process, so counting it would make this counter
// permanently nonzero for a condition nobody needs to act on urgently.
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
