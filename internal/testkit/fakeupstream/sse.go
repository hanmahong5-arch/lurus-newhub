package fakeupstream

import (
	"encoding/json"
	"net/http"
	"strings"
)

// SSEData renders data-only server-sent-event frames — the OpenAI and Gemini
// stream shape. Each argument is one frame's payload, written verbatim
// (pass "[DONE]" for the OpenAI terminator).
//
// This is the shared builder that test packages should use instead of
// declaring their own; internal/pkg/gates counts the private ones that remain.
func SSEData(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: ")
		b.WriteString(f)
		b.WriteString("\n\n")
	}
	return b.String()
}

// SSEEvent is one named server-sent event — the Anthropic and OpenAI
// Responses stream shape.
type SSEEvent struct {
	Event string
	Data  string
}

// SSEEvents renders named events.
func SSEEvents(events ...SSEEvent) string {
	var b strings.Builder
	for _, e := range events {
		if e.Event != "" {
			b.WriteString("event: ")
			b.WriteString(e.Event)
			b.WriteString("\n")
		}
		b.WriteString("data: ")
		b.WriteString(e.Data)
		b.WriteString("\n\n")
	}
	return b.String()
}

// SSEResponse wraps an SSE body in an *http.Response, for adaptor tests that
// feed a stream handler directly instead of going over the network.
func SSEResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       nopCloser{strings.NewReader(body)},
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

// sseStream writes a live stream frame by frame, flushing each one so that it
// genuinely leaves the process — a fault that aborts after frame N must have
// delivered frames 1..N.
type sseStream struct {
	w http.ResponseWriter
}

func startSSE(w http.ResponseWriter) *sseStream {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return &sseStream{w: w}
}

// data writes one data-only frame. v is JSON-encoded unless it is a string.
func (s *sseStream) data(v any) error {
	return s.write("", v)
}

// event writes one named event.
func (s *sseStream) event(name string, v any) error {
	return s.write(name, v)
}

func (s *sseStream) write(name string, v any) error {
	payload, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		payload = string(b)
	}
	var frame string
	if name == "" {
		frame = SSEData(payload)
	} else {
		frame = SSEEvents(SSEEvent{Event: name, Data: payload})
	}
	if _, err := s.w.Write([]byte(frame)); err != nil {
		return err
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
