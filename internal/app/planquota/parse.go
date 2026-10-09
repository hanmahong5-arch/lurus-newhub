package planquota

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// Adapted from sub2api cn_provider_quota_service.go (LGPL-3.0). Differences:
// reset times are unix seconds, and the result type is Window.

func parseF64(v gjson.Result) (float64, bool) {
	switch v.Type {
	case gjson.Number:
		return v.Float(), true
	case gjson.String:
		f, err := strconv.ParseFloat(strings.TrimSpace(v.String()), 64)
		return f, err == nil
	}
	return 0, false
}

func epochToUnix(n int64) int64 {
	if n <= 0 {
		return 0
	}
	if n >= 1_000_000_000_000 { // milliseconds
		return n / 1000
	}
	return n
}

// toUnix converts a reset time given as epoch seconds, epoch milliseconds or
// an RFC3339 string into unix seconds; 0 when absent or unparsable.
func toUnix(v gjson.Result) int64 {
	switch v.Type {
	case gjson.Number:
		return epochToUnix(v.Int())
	case gjson.String:
		s := strings.TrimSpace(v.String())
		if s == "" {
			return 0
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Unix()
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return epochToUnix(n)
		}
	}
	return 0
}

func usedFromLimitRemaining(node gjson.Result) float64 {
	limit, _ := parseF64(node.Get("limit"))
	remaining, _ := parseF64(node.Get("remaining"))
	if limit <= 0 {
		return 0
	}
	return clampNonNeg(limit-remaining) / limit * 100
}

func clampNonNeg(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

// ParseKimi parses the Kimi coding usages response: limits[0].detail is the
// 5-hour window, top-level usage is the weekly one.
func ParseKimi(body []byte) []Window {
	var out []Window
	for _, item := range gjson.GetBytes(body, "limits").Array() {
		d := item.Get("detail")
		if !d.Exists() {
			continue
		}
		out = append(out, Window{Name: Window5h, UsedPct: usedFromLimitRemaining(d), ResetAt: toUnix(d.Get("resetTime"))})
		break
	}
	if u := gjson.GetBytes(body, "usage"); u.Exists() {
		out = append(out, Window{Name: WindowWeekly, UsedPct: usedFromLimitRemaining(u), ResetAt: toUnix(u.Get("resetTime"))})
	}
	return out
}

// ParseMiniMax parses coding_plan/remains: the model_name=="general" entry
// carries the interval (5h) remaining percent and, when current_weekly_status
// is 1, the weekly remaining percent.
func ParseMiniMax(body []byte) []Window {
	var general gjson.Result
	for _, item := range gjson.GetBytes(body, "model_remains").Array() {
		if strings.EqualFold(strings.TrimSpace(item.Get("model_name").String()), "general") {
			general = item
			break
		}
	}
	if !general.Exists() {
		return nil
	}
	var out []Window
	if rem, ok := parseF64(general.Get("current_interval_remaining_percent")); ok {
		out = append(out, Window{Name: Window5h, UsedPct: clampNonNeg(100 - rem), ResetAt: toUnix(general.Get("end_time"))})
	}
	if general.Get("current_weekly_status").Int() == 1 {
		if rem, ok := parseF64(general.Get("current_weekly_remaining_percent")); ok {
			out = append(out, Window{Name: WindowWeekly, UsedPct: clampNonNeg(100 - rem), ResetAt: toUnix(general.Get("weekly_end_time"))})
		}
	}
	return out
}

// ParseZhipu parses data.limits of /api/monitor/usage/quota/limit. TOKENS_LIMIT
// entries are classified by unit (3 = 5 hours, 6 = weekly); entries without a
// usable unit fall back to ordering by reset time (earliest = 5h). CREDIT_LIMIT
// entries are only used when there is no TOKENS_LIMIT at all.
func ParseZhipu(data gjson.Result) []Window {
	var (
		five, week     Window
		fiveOK, weekOK bool
		loose, credit  []Window
		hasTokens      bool
	)
	for _, item := range data.Get("limits").Array() {
		typ := strings.ToUpper(strings.TrimSpace(item.Get("type").String()))
		if typ != "TOKENS_LIMIT" && typ != "CREDIT_LIMIT" {
			continue
		}
		pct, _ := parseF64(item.Get("percentage"))
		w := Window{UsedPct: pct, ResetAt: toUnix(item.Get("nextResetTime"))}
		if typ == "CREDIT_LIMIT" {
			credit = append(credit, w)
			continue
		}
		hasTokens = true
		switch item.Get("unit").Int() {
		case 3:
			if !fiveOK {
				five, fiveOK = w, true
				continue
			}
		case 6:
			if !weekOK {
				week, weekOK = w, true
				continue
			}
		}
		loose = append(loose, w)
	}
	if !hasTokens {
		loose = append(loose, credit...)
	}
	// Unknown reset first (as the original), then earliest reset first.
	sort.SliceStable(loose, func(i, j int) bool {
		a, b := loose[i].ResetAt > 0, loose[j].ResetAt > 0
		if a != b {
			return !a
		}
		return loose[i].ResetAt < loose[j].ResetAt
	})
	for _, w := range loose {
		switch {
		case !fiveOK:
			five, fiveOK = w, true
		case !weekOK:
			week, weekOK = w, true
		}
	}
	var out []Window
	if fiveOK {
		five.Name = Window5h
		out = append(out, five)
	}
	if weekOK {
		week.Name = WindowWeekly
		out = append(out, week)
	}
	return out
}

// Parse dispatches on plan kind. It also surfaces vendor-level error envelopes
// (zhipu success=false, minimax base_resp.status_code != 0) as an error.
func Parse(kind string, body []byte) ([]Window, error) {
	switch kind {
	case KindKimiCoding:
		return ParseKimi(body), nil
	case KindZhipuCoding:
		if s := gjson.GetBytes(body, "success"); s.Exists() && !s.Bool() {
			return nil, fmt.Errorf("zhipu quota error: %s", strings.TrimSpace(gjson.GetBytes(body, "msg").String()))
		}
		return ParseZhipu(gjson.GetBytes(body, "data")), nil
	case KindMiniMax:
		if s := gjson.GetBytes(body, "base_resp.status_code"); s.Exists() && s.Int() != 0 {
			return nil, fmt.Errorf("minimax quota error %d: %s", s.Int(), strings.TrimSpace(gjson.GetBytes(body, "base_resp.status_msg").String()))
		}
		return ParseMiniMax(body), nil
	}
	return nil, fmt.Errorf("unsupported plan kind %q", kind)
}
