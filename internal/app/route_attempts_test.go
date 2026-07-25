package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func routeAttemptCtx(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func TestRouteAttempts_RecordAndRead(t *testing.T) {
	c := routeAttemptCtx(t)

	if got := GetRouteAttempts(c); got != nil {
		t.Errorf("fresh context must have no attempts, got %v", got)
	}

	RecordRouteAttempt(c, RouteAttempt{ChannelID: 1, Provider: "openai", Outcome: RouteAttemptOutcomeBreakerOpen})
	RecordRouteAttempt(c, RouteAttempt{
		ChannelID: 2, ChannelName: "backup", Provider: "anthropic",
		Outcome: RouteAttemptOutcomeUpstreamErr, ErrorCode: "channel:timeout", StatusCode: 502, DurationMs: 1200,
	})
	RecordRouteAttempt(c, RouteAttempt{ChannelID: 3, Outcome: RouteAttemptOutcomeSuccess, DurationMs: 340})

	got := GetRouteAttempts(c)
	if len(got) != 3 {
		t.Fatalf("expected 3 attempts in order, got %d: %+v", len(got), got)
	}
	if got[0].ChannelID != 1 || got[1].ChannelID != 2 || got[2].ChannelID != 3 {
		t.Errorf("attempts must preserve try order, got %+v", got)
	}
	if got[1].ErrorCode != "channel:timeout" || got[1].StatusCode != 502 || got[1].DurationMs != 1200 {
		t.Errorf("failure detail lost: %+v", got[1])
	}
}

func TestRouteAttempts_NilContextIsSafe(t *testing.T) {
	// The relay's error paths can run with a half-built context; recording a
	// trace must never be the thing that panics a request.
	RecordRouteAttempt(nil, RouteAttempt{ChannelID: 1})
	if got := GetRouteAttempts(nil); got != nil {
		t.Errorf("nil context must yield no attempts, got %v", got)
	}
}

func TestRouteAttempts_BoundedPerRequest(t *testing.T) {
	c := routeAttemptCtx(t)
	for i := 0; i < maxRecordedRouteAttempts+10; i++ {
		RecordRouteAttempt(c, RouteAttempt{ChannelID: i})
	}
	if got := len(GetRouteAttempts(c)); got != maxRecordedRouteAttempts {
		t.Errorf("attempts per request must be bounded at %d, got %d", maxRecordedRouteAttempts, got)
	}
}

func TestRouteAttempts_WrongTypeInContextIsIgnored(t *testing.T) {
	c := routeAttemptCtx(t)
	c.Set("route_attempts", "not-a-slice")
	if got := GetRouteAttempts(c); got != nil {
		t.Errorf("unexpected context value must be ignored, got %v", got)
	}
	// And recording must still work (starting a fresh slice) rather than panic.
	RecordRouteAttempt(c, RouteAttempt{ChannelID: 7})
	if got := GetRouteAttempts(c); len(got) != 1 || got[0].ChannelID != 7 {
		t.Errorf("recording after a poisoned context failed: %+v", got)
	}
}

// TestRouteAttempts_JSONShape locks the wire format, since the console and any
// support tooling read these fields out of the log row's other_info blob.
func TestRouteAttempts_JSONShape(t *testing.T) {
	blob, err := json.Marshal(RouteAttempt{
		ChannelID: 4, ChannelName: "eu-1", Provider: "openai",
		Outcome: RouteAttemptOutcomeUpstreamErr, ErrorCode: "channel:rate_limit",
		StatusCode: 429, DurationMs: 87,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"channel_id", "channel_name", "provider", "outcome", "error_code", "status_code", "duration_ms"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing field %q in %s", key, blob)
		}
	}

	// Optional fields must disappear on a clean success so single-attempt rows
	// stay small.
	success, err := json.Marshal(RouteAttempt{ChannelID: 4, Outcome: RouteAttemptOutcomeSuccess, DurationMs: 12})
	if err != nil {
		t.Fatalf("marshal success: %v", err)
	}
	var decodedSuccess map[string]any
	if err := json.Unmarshal(success, &decodedSuccess); err != nil {
		t.Fatalf("unmarshal success: %v", err)
	}
	for _, key := range []string{"error_code", "status_code", "channel_name", "provider"} {
		if _, ok := decodedSuccess[key]; ok {
			t.Errorf("field %q must be omitted when empty: %s", key, success)
		}
	}
}
