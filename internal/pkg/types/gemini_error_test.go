package types

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Moved here with GeminiStatus itself (was app/relay/helper). Same table.
func TestGeminiStatus(t *testing.T) {
	for code, want := range map[int]string{
		400: "INVALID_ARGUMENT", 401: "UNAUTHENTICATED", 403: "PERMISSION_DENIED", 404: "NOT_FOUND",
		429: "RESOURCE_EXHAUSTED", 500: "INTERNAL", 502: "UNAVAILABLE", 503: "UNAVAILABLE",
		504: "DEADLINE_EXCEEDED", 418: "UNKNOWN",
	} {
		if got := GeminiStatus(code); got != want {
			t.Errorf("GeminiStatus(%d) = %s, want %s", code, got, want)
		}
	}
}

// TestToGeminiError_SerializedFieldOrder pins the wire bytes, not just the
// values. google-genai recognises an error by the literal `{"error":` prefix,
// and the streaming test in app/relay/helper asserts the whole serialized
// prefix — so reordering the struct fields (a change Go itself would accept
// silently, and which no value-level assertion would catch) breaks SDK
// detection. Struct field order IS the contract here.
func TestToGeminiError_SerializedFieldOrder(t *testing.T) {
	apiErr := NewErrorWithStatusCode(errors.New("upstream went away"), ErrorCodeBadResponse, http.StatusBadGateway)

	raw, err := json.Marshal(apiErr.ToGeminiError())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"error":{"code":502,"message":"upstream went away","status":"UNAVAILABLE"}}`
	if string(raw) != want {
		t.Errorf("serialized envelope =\n  %s\nwant\n  %s\n\ngoogle-genai keys off the "+
			"literal {\"error\": prefix and the code/message/status order; this is a byte "+
			"contract, not a value one.", raw, want)
	}
}

func TestToGeminiError_MapsStatusAndMessage(t *testing.T) {
	apiErr := NewErrorWithStatusCode(errors.New("too many requests"), ErrorCodeBadResponse, http.StatusTooManyRequests)
	frame := apiErr.ToGeminiError()
	if frame.Error.Code != http.StatusTooManyRequests {
		t.Errorf("code = %d, want 429", frame.Error.Code)
	}
	if frame.Error.Status != "RESOURCE_EXHAUSTED" {
		t.Errorf("status = %q, want RESOURCE_EXHAUSTED", frame.Error.Status)
	}
	if !strings.Contains(frame.Error.Message, "too many requests") {
		t.Errorf("message = %q, want the underlying error text", frame.Error.Message)
	}
}

// A nil receiver must not panic: this renders the terminal error of a request,
// so a panic here would turn a handled failure into a dropped connection.
func TestToGeminiError_NilReceiver(t *testing.T) {
	var apiErr *NewAPIError
	frame := apiErr.ToGeminiError()
	if frame.Error.Code != http.StatusInternalServerError || frame.Error.Status != "INTERNAL" {
		t.Errorf("nil receiver = %+v, want a 500/INTERNAL envelope", frame.Error)
	}
}
