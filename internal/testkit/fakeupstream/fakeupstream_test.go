package fakeupstream

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// reply is what a test needs from a response once its body has been read
// and closed inside post.
type reply struct {
	StatusCode int
	Header     http.Header
}

func post(t *testing.T, url, key, body string) (reply, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}, string(b)
}

func TestNonStreamUsageInEachWire(t *testing.T) {
	u := Usage{PromptTokens: 1200, CompletionTokens: 345, CachedTokens: 200}
	_, base := NewTest(t, Config{Usage: u})

	cases := []struct {
		path, body string
		check      func(t *testing.T, got map[string]any)
	}{
		{"/v1/chat/completions", `{"model":"m"}`, func(t *testing.T, got map[string]any) {
			usage := got["usage"].(map[string]any)
			wantNum(t, usage, "prompt_tokens", 1200)
			wantNum(t, usage, "completion_tokens", 345)
			wantNum(t, usage["prompt_tokens_details"].(map[string]any), "cached_tokens", 200)
		}},
		{"/v1/responses", `{"model":"m"}`, func(t *testing.T, got map[string]any) {
			usage := got["usage"].(map[string]any)
			wantNum(t, usage, "input_tokens", 1200)
			wantNum(t, usage, "output_tokens", 345)
		}},
		{"/anthropic/v1/messages", `{"model":"m"}`, func(t *testing.T, got map[string]any) {
			usage := got["usage"].(map[string]any)
			// Anthropic input_tokens excludes the cache read.
			wantNum(t, usage, "input_tokens", 1000)
			wantNum(t, usage, "cache_read_input_tokens", 200)
			wantNum(t, usage, "output_tokens", 345)
		}},
		{"/v1beta/models/gemini-x:generateContent", `{}`, func(t *testing.T, got map[string]any) {
			usage := got["usageMetadata"].(map[string]any)
			wantNum(t, usage, "promptTokenCount", 1200)
			wantNum(t, usage, "candidatesTokenCount", 345)
			wantNum(t, usage, "cachedContentTokenCount", 200)
		}},
		{"/v1/systemone", `{"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"?"}}}`, func(t *testing.T, got map[string]any) {
			wantNum(t, got["usage"].(map[string]any), "input_tokens", 1200)
			if _, ok := got["answers"].(map[string]any)["q"]; !ok {
				t.Errorf("no answer for question q: %v", got)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, body := post(t, base+tc.path, "", tc.body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", resp.StatusCode, body)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("decode: %v: %s", err, body)
			}
			tc.check(t, got)
		})
	}
}

func wantNum(t *testing.T, m map[string]any, key string, want float64) {
	t.Helper()
	if got, _ := m[key].(float64); got != want {
		t.Errorf("%s = %v, want %v (in %v)", key, m[key], want, m)
	}
}

// Each stream ends with its wire's terminator and carries usage where the
// wire puts it.
func TestStreamsTerminateAndCarryUsage(t *testing.T) {
	_, base := NewTest(t, Config{Usage: Usage{PromptTokens: 7, CompletionTokens: 3}})

	cases := []struct {
		path, body, terminator, usage string
	}{
		{"/v1/chat/completions", `{"model":"m","stream":true,"stream_options":{"include_usage":true}}`, "data: [DONE]", `"completion_tokens":3`},
		{"/v1/responses", `{"model":"m","stream":true}`, "event: response.completed", `"output_tokens":3`},
		{"/v1/messages", `{"model":"m","stream":true}`, "event: message_stop", `"output_tokens":3`},
		{"/v1beta/models/g:streamGenerateContent?alt=sse", `{}`, `"finishReason":"STOP"`, `"candidatesTokenCount":3`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp, body := post(t, base+tc.path, "", tc.body)
			if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
				t.Errorf("Content-Type = %q", ct)
			}
			if !strings.Contains(body, tc.terminator) {
				t.Errorf("stream has no terminator %q:\n%s", tc.terminator, body)
			}
			if !strings.Contains(body, tc.usage) {
				t.Errorf("stream has no usage %q:\n%s", tc.usage, body)
			}
		})
	}
}

// The real API streams usage only on request; a relay that stops asking
// must see a stream without it.
func TestOpenAIStreamUsageOnlyWhenRequested(t *testing.T) {
	_, base := NewTest(t, Config{})
	_, body := post(t, base+"/v1/chat/completions", "", `{"model":"m","stream":true}`)
	if strings.Contains(body, `"usage"`) {
		t.Errorf("usage streamed without include_usage:\n%s", body)
	}
}

func TestKeyIsEnforcedOnEveryWire(t *testing.T) {
	_, base := NewTest(t, Config{Key: "k1"})
	for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses", "/v1beta/models/g:generateContent", "/v1/systemone"} {
		if resp, _ := post(t, base+path, "wrong", `{"model":"m"}`); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with wrong key: status %d, want 401", path, resp.StatusCode)
		}
		if resp, body := post(t, base+path, "k1", `{"model":"m"}`); resp.StatusCode != http.StatusOK {
			t.Errorf("%s with right key: status %d: %s", path, resp.StatusCode, body)
		}
	}
}

func TestFaultModesByModelName(t *testing.T) {
	_, base := NewTest(t, Config{})
	cases := map[string]int{
		FaultHTTP500:             http.StatusInternalServerError,
		FaultRateLimit429:        http.StatusTooManyRequests,
		FaultInsufficientBalance: http.StatusPaymentRequired,
		FaultHTTP401:             http.StatusUnauthorized,
	}
	for mode, want := range cases {
		resp, body := post(t, base+"/v1/chat/completions", "", `{"model":"`+mode+`"}`)
		if resp.StatusCode != want {
			t.Errorf("%s: status %d, want %d: %s", mode, resp.StatusCode, want, body)
		}
		if mode == FaultRateLimit429 && resp.Header.Get("Retry-After") != "30" {
			t.Errorf("429 Retry-After = %q, want 30", resp.Header.Get("Retry-After"))
		}
	}
}

func TestMidStreamAbortHasNoTerminatorInAnyWire(t *testing.T) {
	_, base := NewTest(t, Config{})
	cases := map[string]string{
		"/v1/chat/completions": "[DONE]",
		"/v1/messages":         "message_stop",
		"/v1/responses":        "response.completed",
		"/v1beta/models/mid_stream_abort:streamGenerateContent": "finishReason",
	}
	for path, terminator := range cases {
		_, body := post(t, base+path+"?frames=2", "", `{"model":"mid_stream_abort","stream":true}`)
		if !strings.Contains(body, "part-1") {
			t.Errorf("%s: frames were not delivered before the abort:\n%s", path, body)
		}
		if strings.Contains(body, terminator) {
			t.Errorf("%s: aborted stream carries its terminator %q:\n%s", path, terminator, body)
		}
		// Anthropic's message_start legitimately carries the input count; what
		// an abort must not deliver is the final (output) usage.
		final := "usage"
		if path == "/v1/messages" {
			final = "message_delta"
		}
		if strings.Contains(body, final) {
			t.Errorf("%s: aborted stream carries final usage (%q):\n%s", path, final, body)
		}
	}
}

func TestDisconnectBeforeFirstByte(t *testing.T) {
	_, base := NewTest(t, Config{})
	resp, err := http.Post(base+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"disconnect_before_first_byte"}`))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("got a response (status %d), want a transport error", resp.StatusCode)
	}
}

func TestSlowHeadersHonoursDelay(t *testing.T) {
	_, base := NewTest(t, Config{})
	start := time.Now()
	resp, _ := post(t, base+"/v1/chat/completions?delay_ms=150", "", `{"model":"slow_headers"}`)
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Errorf("answered after %v, want >= 150ms", d)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
}

// A queued fault breaks the next request for a REAL model name, then the
// server is healthy again — and every request is recorded either way.
func TestQueuedFaultAndRecording(t *testing.T) {
	s, base := NewTest(t, Config{})
	resp, _ := post(t, base+"/_fake/faults", "", `{"mode":"http_500","count":1}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("queue fault: %d", resp.StatusCode)
	}
	if resp, _ := post(t, base+"/v1/chat/completions", "", `{"model":"deepseek-chat"}`); resp.StatusCode != 500 {
		t.Errorf("first request: %d, want 500", resp.StatusCode)
	}
	if resp, _ := post(t, base+"/v1/chat/completions", "", `{"model":"deepseek-chat"}`); resp.StatusCode != 200 {
		t.Errorf("second request: %d, want 200", resp.StatusCode)
	}
	recs := s.Requests()
	if len(recs) != 2 || recs[0].Fault != FaultHTTP500 || recs[0].Status != 500 || recs[1].Status != 200 || recs[1].Model != "deepseek-chat" {
		t.Errorf("records = %+v", recs)
	}

	getResp, err := http.Get(base + "/_fake/requests")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = getResp.Body.Close() }()
	var listed struct{ Requests []Record }
	if err := json.NewDecoder(getResp.Body).Decode(&listed); err != nil || len(listed.Requests) != 2 {
		t.Errorf("GET /_fake/requests = %+v (%v)", listed, err)
	}
}

func TestRuntimeUsageChange(t *testing.T) {
	_, base := NewTest(t, Config{})
	if resp, body := post(t, base+"/_fake/usage", "", `{"prompt_tokens":11,"completion_tokens":22}`); resp.StatusCode != 200 {
		t.Fatalf("set usage: %d %s", resp.StatusCode, body)
	}
	_, body := post(t, base+"/v1/chat/completions", "", `{"model":"m"}`)
	if !strings.Contains(body, `"prompt_tokens":11`) || !strings.Contains(body, `"completion_tokens":22`) {
		t.Errorf("usage not applied: %s", body)
	}
	if resp, _ := post(t, base+"/_fake/usage", "", `{"prompt_tokens":1,"cached_tokens":2}`); resp.StatusCode != 400 {
		t.Errorf("cached > prompt accepted: %d", resp.StatusCode)
	}
}

func TestFaultOnlyRejectsOrdinaryModels(t *testing.T) {
	_, base := NewTest(t, Config{FaultOnly: true})
	resp, body := post(t, base+"/v1/chat/completions", "", `{"model":"deepseek-chat"}`)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "unknown fault mode") {
		t.Errorf("status %d body %s, want 400 unknown fault mode", resp.StatusCode, body)
	}
	if resp, _ := post(t, base+"/v1/chat/completions", "", `{"model":"http_500"}`); resp.StatusCode != 500 {
		t.Errorf("fault model under FaultOnly: %d, want 500", resp.StatusCode)
	}
}

func TestSSEBuilders(t *testing.T) {
	if got := SSEData(`{"a":1}`, "[DONE]"); got != "data: {\"a\":1}\n\ndata: [DONE]\n\n" {
		t.Errorf("SSEData = %q", got)
	}
	if got := SSEEvents(SSEEvent{Event: "x", Data: "{}"}); got != "event: x\ndata: {}\n\n" {
		t.Errorf("SSEEvents = %q", got)
	}
	resp := SSEResponse("data: 1\n\n")
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(b, []byte("data: 1\n\n")) || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("SSEResponse = %q %v", b, resp.Header)
	}
}
