package modelprobe

import (
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

const threshold = 3

func TestApply_SuccessOnNeverProbedPair(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	next, transition := Apply(nil, Result{OK: true, LatencyMs: 42}, now, threshold)

	if !next.Ok || next.ConsecutiveFailures != 0 || next.LastError != "" {
		t.Fatalf("got %+v, want a clean success row", next)
	}
	if next.LatencyMs != 42 || next.LastProbeAt != now.Unix() || next.UpdatedAt != now.Unix() {
		t.Errorf("timestamps/latency not stamped: %+v", next)
	}
	if transition != "" {
		t.Errorf("transition = %q, want empty (never disabled, nothing to recover from)", transition)
	}
}

func TestApply_Recovery(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	prev := &entity.ModelHealth{
		ChannelId: 1, Model: "m", Ok: false, ConsecutiveFailures: 5,
		AutoDisabled: true, AutoDisabledAt: 900000, LastError: "boom",
	}
	next, transition := Apply(prev, Result{OK: true, LatencyMs: 10}, now, threshold)

	if !next.Ok || next.ConsecutiveFailures != 0 || next.LastError != "" {
		t.Fatalf("got %+v, want a clean success row", next)
	}
	if next.AutoDisabled {
		t.Error("AutoDisabled must clear on recovery")
	}
	if next.AutoDisabledAt != 0 {
		t.Errorf("AutoDisabledAt = %d, want reset to 0", next.AutoDisabledAt)
	}
	if transition != "recovered" {
		t.Errorf("transition = %q, want \"recovered\"", transition)
	}
}

func TestApply_ThresholdDisable(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	prev := &entity.ModelHealth{ChannelId: 1, Model: "m", ConsecutiveFailures: 2}
	next, transition := Apply(prev, Result{OK: false, StatusCode: 500, Err: "upstream 500"}, now, threshold)

	if next.ConsecutiveFailures != 3 {
		t.Fatalf("ConsecutiveFailures = %d, want 3", next.ConsecutiveFailures)
	}
	if !next.AutoDisabled {
		t.Error("must auto-disable once the streak reaches threshold")
	}
	if next.AutoDisabledAt != now.Unix() {
		t.Errorf("AutoDisabledAt = %d, want %d", next.AutoDisabledAt, now.Unix())
	}
	if transition != "disabled" {
		t.Errorf("transition = %q, want \"disabled\"", transition)
	}
	if next.LastError != "upstream 500" {
		t.Errorf("LastError = %q", next.LastError)
	}
}

func TestApply_BelowThresholdDoesNotDisable(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	prev := &entity.ModelHealth{ChannelId: 1, Model: "m", ConsecutiveFailures: 1}
	next, transition := Apply(prev, Result{OK: false, StatusCode: 500, Err: "boom"}, now, threshold)

	if next.ConsecutiveFailures != 2 {
		t.Fatalf("ConsecutiveFailures = %d, want 2", next.ConsecutiveFailures)
	}
	if next.AutoDisabled || transition != "" {
		t.Errorf("must not disable below threshold: AutoDisabled=%v transition=%q", next.AutoDisabled, transition)
	}
}

func TestApply_AlreadyDisabledDoesNotRepeatTransition(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	prev := &entity.ModelHealth{
		ChannelId: 1, Model: "m", ConsecutiveFailures: 5,
		AutoDisabled: true, AutoDisabledAt: 500,
	}
	next, transition := Apply(prev, Result{OK: false, StatusCode: 500, Err: "still down"}, now, threshold)

	if !next.AutoDisabled {
		t.Error("must stay disabled")
	}
	if next.AutoDisabledAt != 500 {
		t.Errorf("AutoDisabledAt = %d, want unchanged 500 (no re-disable transition)", next.AutoDisabledAt)
	}
	if transition != "" {
		t.Errorf("transition = %q, want empty — already disabled, this is not a new transition", transition)
	}
}

func TestApply_429NotCountedAndNotDisabled(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	prev := &entity.ModelHealth{ChannelId: 1, Model: "m", ConsecutiveFailures: threshold - 1}
	next, transition := Apply(prev, Result{OK: false, StatusCode: 429, Err: "Too Many Requests"}, now, threshold)

	if next.ConsecutiveFailures != threshold-1 {
		t.Fatalf("ConsecutiveFailures = %d, want unchanged at %d (429 must not count)", next.ConsecutiveFailures, threshold-1)
	}
	if next.AutoDisabled || transition != "" {
		t.Errorf("429 must never disable: AutoDisabled=%v transition=%q", next.AutoDisabled, transition)
	}
	if next.Ok {
		t.Error("Ok must still be false for a 429")
	}
	if next.LastError == "" {
		t.Error("LastError must still be recorded for a 429")
	}
}

func TestApply_RateLimitKeywordInErrNotCounted(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	prev := &entity.ModelHealth{ChannelId: 1, Model: "m", ConsecutiveFailures: threshold - 1}
	next, transition := Apply(prev, Result{OK: false, StatusCode: 200, Err: "free-tier quota exhausted for today"}, now, threshold)

	if next.ConsecutiveFailures != threshold-1 {
		t.Fatalf("ConsecutiveFailures = %d, want unchanged (quota-exhausted phrasing must not count)", next.ConsecutiveFailures)
	}
	if next.AutoDisabled || transition != "" {
		t.Errorf("quota exhaustion must never disable: AutoDisabled=%v transition=%q", next.AutoDisabled, transition)
	}
}

func TestApply_OtherFailureTruncatesLastErrorTo500Chars(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	longErr := strings.Repeat("x", 900)
	next, _ := Apply(nil, Result{OK: false, StatusCode: 500, Err: longErr}, now, threshold)

	if len(next.LastError) != 500 {
		t.Fatalf("LastError length = %d, want truncated to 500", len(next.LastError))
	}
}

func TestApply_AlwaysStampsLastProbeAtLatencyUpdatedAt(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	prev := &entity.ModelHealth{ChannelId: 1, Model: "m", LastProbeAt: 1, UpdatedAt: 1}
	next, _ := Apply(prev, Result{OK: false, StatusCode: 500, Err: "x", LatencyMs: 777}, now, threshold)

	if next.LastProbeAt != now.Unix() || next.UpdatedAt != now.Unix() || next.LatencyMs != 777 {
		t.Errorf("got %+v, want LastProbeAt/UpdatedAt=%d LatencyMs=777", next, now.Unix())
	}
}
