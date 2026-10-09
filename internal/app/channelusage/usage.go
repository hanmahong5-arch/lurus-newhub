// Package channelusage is the pure core of the per-account cost and
// utilization report (docs/plans/account-pool-ops-2026-10-09.md section 3):
// window parsing, the utilization formula and the idle/overuse verdict. It
// does no I/O, so every rule is testable without a database.
package channelusage

import (
	"sort"
	"time"
)

// Window is one of the report windows an operator can ask for.
type Window struct {
	Name     string
	Duration time.Duration
}

var windows = []Window{
	{"5h", 5 * time.Hour},
	{"24h", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
}

// DefaultWindow is used when the caller names none.
const DefaultWindow = "30d"

// SummaryWindow is the fixed window of the all-channels summary.
const SummaryWindow = "30d"

// ParseWindow resolves a window name; ok is false for anything unlisted.
func ParseWindow(name string) (Window, bool) {
	if name == "" {
		name = DefaultWindow
	}
	for _, w := range windows {
		if w.Name == name {
			return w, true
		}
	}
	return Window{}, false
}

// Verdict thresholds for the summary: below IdleBelow the plan is paid for but
// barely used; above OverBeyond the account burns more than its fee covers.
const (
	IdleBelow  = 0.2
	OverBeyond = 1.0
)

// Verdicts.
const (
	VerdictNone = ""
	VerdictIdle = "idle"
	VerdictOver = "overuse"
)

// daysPerMonth is the fixed divisor that turns a monthly fee into a daily one.
const daysPerMonth = 30.0

// ProratedFeeCNY4 is the share of the monthly fee that falls on the window:
// fee / 30 * window-in-days. Zero when the channel has no plan fee.
func ProratedFeeCNY4(monthlyFeeCNY4 int64, w Window) float64 {
	if monthlyFeeCNY4 <= 0 {
		return 0
	}
	days := w.Duration.Hours() / 24
	return float64(monthlyFeeCNY4) / daysPerMonth * days
}

// Utilization is window cost divided by the prorated plan fee (1.0 = the
// window consumed exactly what the plan costs). ok is false when there is no
// plan fee, in which case no ratio is reported at all.
func Utilization(costCNY4, monthlyFeeCNY4 int64, w Window) (ratio float64, ok bool) {
	fee := ProratedFeeCNY4(monthlyFeeCNY4, w)
	if fee <= 0 {
		return 0, false
	}
	if costCNY4 < 0 {
		costCNY4 = 0
	}
	return float64(costCNY4) / fee, true
}

// Classify turns a utilization into the operator-facing verdict.
func Classify(ratio float64, ok bool) string {
	switch {
	case !ok:
		return VerdictNone
	case ratio < IdleBelow:
		return VerdictIdle
	case ratio > OverBeyond:
		return VerdictOver
	}
	return VerdictNone
}

// SortSummary orders summary rows for the operator: channels with a plan fee
// first, by utilization descending (overused at the top, idle at the bottom of
// that block); fee-less channels after, by cost descending. Ties break on id so
// the order is deterministic.
func SortSummary(rows []SummaryRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.Utilization != nil) != (b.Utilization != nil) {
			return a.Utilization != nil
		}
		if a.Utilization != nil && *a.Utilization != *b.Utilization {
			return *a.Utilization > *b.Utilization
		}
		if a.CostCNY4 != b.CostCNY4 {
			return a.CostCNY4 > b.CostCNY4
		}
		return a.ChannelId < b.ChannelId
	})
}

// Totals is the additive usage figure of one slice (a key, or a channel).
type Totals struct {
	Requests         int64 `json:"requests"`
	Errors           int64 `json:"errors"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	Quota            int64 `json:"quota"`
	CostCNY4         int64 `json:"cost_cny4"`
}

// Add accumulates o into t.
func (t *Totals) Add(o Totals) {
	t.Requests += o.Requests
	t.Errors += o.Errors
	t.PromptTokens += o.PromptTokens
	t.CompletionTokens += o.CompletionTokens
	t.Quota += o.Quota
	t.CostCNY4 += o.CostCNY4
}

// KeyRow is one upstream key's usage inside a channel. KeyIdx -1 collects
// requests that carry no key index (single-key channel, or rows older than
// migration 051).
type KeyRow struct {
	KeyIdx int64 `json:"key_idx"`
	Totals
}

// SummaryRow is one channel's 30-day line in the summary.
type SummaryRow struct {
	ChannelId int    `json:"channel_id"`
	Name      string `json:"name"`
	Status    int    `json:"status"`
	Totals
	PlanMonthlyFeeCNY4 int64    `json:"plan_monthly_fee_cny4"`
	Utilization        *float64 `json:"utilization,omitempty"`
	Verdict            string   `json:"verdict,omitempty"`
}
