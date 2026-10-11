package channelusage

import (
	"math"
	"testing"
)

func TestParseWindow(t *testing.T) {
	for _, n := range []string{"5h", "24h", "7d", "30d"} {
		if _, ok := ParseWindow(n); !ok {
			t.Errorf("%s should parse", n)
		}
	}
	if w, ok := ParseWindow(""); !ok || w.Name != DefaultWindow {
		t.Errorf("empty must default to %s, got %+v ok=%v", DefaultWindow, w, ok)
	}
	for _, n := range []string{"1h", "90d", "30D", "x"} {
		if _, ok := ParseWindow(n); ok {
			t.Errorf("%s must be rejected", n)
		}
	}
}

func TestUtilization(t *testing.T) {
	const fee = 300_0000 // 300 CNY / month, in 0.0001 CNY
	w30, _ := ParseWindow("30d")
	w5h, _ := ParseWindow("5h")
	w7d, _ := ParseWindow("7d")

	// 30d window: prorated fee == monthly fee.
	if r, ok := Utilization(150_0000, fee, w30); !ok || math.Abs(r-0.5) > 1e-9 {
		t.Errorf("30d half spend: r=%v ok=%v", r, ok)
	}
	// 7d window: fee * 7/30 = 70 CNY; spending 70 CNY is exactly 1.0.
	if r, ok := Utilization(70_0000, fee, w7d); !ok || math.Abs(r-1) > 1e-9 {
		t.Errorf("7d exact: r=%v ok=%v", r, ok)
	}
	// 5h window: fee * (5/24)/30 = 2.0833 CNY.
	if f := ProratedFeeCNY4(fee, w5h); math.Abs(f-300_0000.0/30*5/24) > 1e-6 {
		t.Errorf("5h prorated = %v", f)
	}
	// Over-use is reported above 1, not clamped.
	if r, _ := Utilization(600_0000, fee, w30); r != 2 {
		t.Errorf("overuse r=%v want 2", r)
	}
	// No fee => no ratio at all.
	for _, f := range []int64{0, -5} {
		if _, ok := Utilization(100, f, w30); ok {
			t.Errorf("fee %d must yield no utilization", f)
		}
	}
	// Zero spend with a fee is 0 and ok (idle), distinct from "no fee".
	if r, ok := Utilization(0, fee, w30); !ok || r != 0 {
		t.Errorf("zero spend: r=%v ok=%v", r, ok)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		r    float64
		ok   bool
		want string
	}{
		{0, true, VerdictIdle},
		{0.19, true, VerdictIdle},
		{0.2, true, VerdictNone},
		{1.0, true, VerdictNone},
		{1.01, true, VerdictOver},
		{5, false, VerdictNone},
	}
	for _, c := range cases {
		if got := Classify(c.r, c.ok); got != c.want {
			t.Errorf("Classify(%v,%v)=%q want %q", c.r, c.ok, got, c.want)
		}
	}
}

func fp(f float64) *float64 { return &f }

func TestSortSummary(t *testing.T) {
	rows := []SummaryRow{
		{ChannelId: 1, Totals: Totals{CostCNY4: 50}},                       // no fee
		{ChannelId: 2, Utilization: fp(0.1)},                               // idle
		{ChannelId: 3, Utilization: fp(2.5)},                               // overused
		{ChannelId: 4, Totals: Totals{CostCNY4: 900}},                      // no fee, bigger
		{ChannelId: 5, Utilization: fp(0.1), Totals: Totals{CostCNY4: 10}}, // ties with 2 on util, higher cost
	}
	SortSummary(rows)
	var ids []int
	for _, r := range rows {
		ids = append(ids, r.ChannelId)
	}
	want := []int{3, 5, 2, 4, 1}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order = %v, want %v", ids, want)
		}
	}
}

func TestTotalsAdd(t *testing.T) {
	a := Totals{Requests: 1, Errors: 1, PromptTokens: 2, CompletionTokens: 3, Quota: 4, CostCNY4: 5}
	a.Add(Totals{Requests: 10, Errors: 10, PromptTokens: 20, CompletionTokens: 30, Quota: 40, CostCNY4: 50})
	if a != (Totals{Requests: 11, Errors: 11, PromptTokens: 22, CompletionTokens: 33, Quota: 44, CostCNY4: 55}) {
		t.Errorf("Add = %+v", a)
	}
}
