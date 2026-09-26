package types

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// l5_wire_envelope_test.go pins the two rendering invariants an UPSTREAM HTTP
// error (the errors app.RelayErrorHandler builds via WithOpenAIError /
// WithClaudeError — i.e. every vendor 4xx/5xx, the most common failure a
// paying customer ever sees) must satisfy on each wire this gateway speaks:
//
//  1. the Anthropic envelope's error.type is always a value from Anthropic's
//     own vocabulary, never the vendor's "code" — and never the literal
//     "<nil>" that fmt.Sprintf("%v", nil) rendered when the upstream body
//     carried no "code" key at all (Anthropic-shaped bodies never do);
//  2. the message the relay handler rewrote through SetMessage (the
//     upstream-provider attribution and the "(request id: ...)" suffix
//     handler/relay.go appends) is what the client actually reads, on the
//     OpenAI, Anthropic and Gemini wires alike.
//
// Why the pre-existing suite could not see either: every "(request id:"
// assertion in the tree runs through ErrorTypeNewAPIError, whose converter
// branch always read e.Err. The two upstream branches returned the stored
// vendor struct verbatim instead, so a rewrite of e.Err never reached the
// wire from those — and the one ToClaudeError case that did cover the
// upstream branch hardcoded Code: "rate_limit_error", a string that happens
// to also be a legal Anthropic type.
//
// BLIND SPOT: these are unit assertions on the three converters only. They
// say nothing about whether a given HTTP route reaches the right converter
// (that is middleware/wire_format.go's relayFormatForPath and relay.go's
// relayFormat switch), and nothing about the streaming/in-band error path in
// app/relay/helper.

// TestToClaudeError_UpstreamError_NeverRendersVendorCodeAsType is the nil-Code
// and foreign-code oracle for defect (1).
func TestToClaudeError_UpstreamError_NeverRendersVendorCodeAsType(t *testing.T) {
	cases := []struct {
		name   string
		oa     OpenAIError
		status int
		want   string
	}{
		{
			// The common case: an Anthropic-shaped upstream body decodes into
			// OpenAIError with Code left nil, so "%v" printed "<nil>".
			name:   "nil code, 529",
			oa:     OpenAIError{Message: "Overloaded", Type: "overloaded_error"},
			status: 529,
			want:   "overloaded_error",
		},
		{
			name:   "nil code, 429",
			oa:     OpenAIError{Message: "slow down"},
			status: http.StatusTooManyRequests,
			want:   "rate_limit_error",
		},
		{
			// A vendor code that is NOT part of Anthropic's type vocabulary
			// must not be smuggled into the type slot.
			name:   "foreign string code",
			oa:     OpenAIError{Message: "too long", Type: "invalid_request_error", Code: "context_length_exceeded"},
			status: http.StatusBadRequest,
			want:   "invalid_request_error",
		},
		{
			name:   "numeric vendor code",
			oa:     OpenAIError{Message: "nope", Type: "t", Code: 40303},
			status: http.StatusForbidden,
			want:   "permission_error",
		},
		{
			// The case the old test pinned: a code that coincidentally IS a
			// legal Anthropic type. Keeping it proves the fix did not change
			// the answer where the old behaviour happened to be right.
			name:   "code that is coincidentally a legal type",
			oa:     OpenAIError{Message: "boom", Type: "t", Code: "rate_limit_error"},
			status: http.StatusTooManyRequests,
			want:   "rate_limit_error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := WithOpenAIError(tc.oa, tc.status)
			if e.GetErrorType() != ErrorTypeOpenAIError {
				t.Fatalf("precondition: errorType = %q, want openai_error", e.GetErrorType())
			}
			out := e.ToClaudeError()
			if out.Type != tc.want {
				t.Errorf("error.type = %q, want %q (Anthropic taxonomy for status %d)", out.Type, tc.want, tc.status)
			}
			if strings.Contains(out.Type, "<nil>") {
				t.Errorf("error.type = %q — a Go nil must never reach the wire", out.Type)
			}
			if out.Message == "" {
				t.Errorf("error.message must not be empty")
			}
		})
	}
}

// TestToOpenAIError_ClaudeUpstreamError_TranslatesTypeToTheOpenAIWire is the
// mirror of the test above, for the opposite direction: an Anthropic-shaped
// upstream error (types.WithClaudeError — the in-band error frames
// provider/claude/relay-claude.go:692/:824 build out of a Claude response
// body) rendered into an OpenAI envelope, which is what happens whenever a
// caller speaking OpenAI's wire is routed to an Anthropic channel.
//
// The two vocabularies overlap almost completely — compare the values
// WireErrorType returns for each wire — and differ in exactly two members:
// Anthropic has overloaded_error where OpenAI's bucket is api_error, and
// OpenAI has insufficient_quota where Anthropic's is billing_error. Only
// those two (plus any type outside both vocabularies, e.g. WithClaudeError's
// "upstream_error" default for a body with no type) may be rewritten; every
// shared name must survive, because it is strictly more specific than the
// HTTP status — both call sites above hardcode 500 — and because
// app.ShouldDisableChannel keys its channel auto-ban decision off exactly
// these strings (internal/app/channel.go:85,111-125).
//
// BLIND SPOT: this is a unit assertion on the converter. It does not prove
// which requests reach it, and the "want" column is a hand-maintained list of
// the two vocabularies, not something derived from Anthropic's or OpenAI's
// published taxonomy — a type either vendor adds later is invisible to it
// until a human adds a row.
func TestToOpenAIError_ClaudeUpstreamError_TranslatesTypeToTheOpenAIWire(t *testing.T) {
	// The Anthropic-only names that must never appear in an OpenAI envelope's
	// type slot.
	anthropicOnly := map[string]bool{"overloaded_error": true, "billing_error": true}

	cases := []struct {
		name       string
		claudeType string
		status     int
		wantType   string
	}{
		// --- the two that diverge -------------------------------------
		{"overloaded_error has no OpenAI equivalent", "overloaded_error", http.StatusInternalServerError, "api_error"},
		{"billing_error is OpenAI's insufficient_quota", "billing_error", http.StatusInternalServerError, "insufficient_quota"},

		// --- shared names, which must NOT be flattened to the status ---
		// (these were already right before the fix; they are here as
		// non-regression pins, not as oracles — in particular the first two
		// are what app.ShouldDisableChannel matches on.)
		{"authentication_error survives", "authentication_error", http.StatusInternalServerError, "authentication_error"},
		{"permission_error survives", "permission_error", http.StatusInternalServerError, "permission_error"},
		{"invalid_request_error survives", "invalid_request_error", http.StatusInternalServerError, "invalid_request_error"},
		{"rate_limit_error survives", "rate_limit_error", http.StatusInternalServerError, "rate_limit_error"},
		{"api_error survives", "api_error", http.StatusInternalServerError, "api_error"},

		// --- outside both vocabularies: fall back to the status table ---
		{"typeless body (WithClaudeError's upstream_error default)", "", http.StatusInternalServerError, "api_error"},
		{"a type neither vocabulary names", "some_future_error", http.StatusTooManyRequests, "rate_limit_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := WithClaudeError(ClaudeError{Type: tc.claudeType, Message: "busy"}, tc.status)
			if e.GetErrorType() != ErrorTypeClaudeError {
				t.Fatalf("precondition: errorType = %q, want claude_error", e.GetErrorType())
			}
			out := e.ToOpenAIError()
			if out.Type != tc.wantType {
				t.Errorf("OpenAI wire type = %q, want %q (status %d, vendor type %q)",
					out.Type, tc.wantType, tc.status, tc.claudeType)
			}
			if anthropicOnly[out.Type] {
				t.Errorf("OpenAI wire type = %q — an Anthropic-only type name must not reach the OpenAI wire", out.Type)
			}
			// The vendor's own classification is not lost: the OpenAI
			// envelope has a "code" slot (the Anthropic one has none, which
			// is why the mirror defect had to drop it), and that is where it
			// stays. WithClaudeError sets errorCode from the vendor type,
			// defaulting to "upstream_error" for a typeless body.
			wantCode := tc.claudeType
			if wantCode == "" {
				wantCode = "upstream_error"
			}
			if fmt.Sprintf("%v", out.Code) != wantCode {
				t.Errorf("OpenAI wire code = %v, want the vendor type %q preserved there", out.Code, wantCode)
			}
			if out.Message == "" {
				t.Errorf("error.message must not be empty")
			}
		})
	}
}

// TestUpstreamError_SetMessageReachesEveryWire is the oracle for defect (2):
// the request-id suffix and upstream-provider attribution handler/relay.go
// writes with SetMessage must survive into the rendered body of all three
// wires, while type/code/param keep coming from the stored vendor error.
func TestUpstreamError_SetMessageReachesEveryWire(t *testing.T) {
	const requestID = "req-l5-abc123"

	t.Run("openai-shaped upstream error", func(t *testing.T) {
		e := WithOpenAIError(OpenAIError{
			Message: "Rate limit reached",
			Type:    "rate_limit_error",
			Param:   "model",
			Code:    "rate_limit_exceeded",
		}, http.StatusTooManyRequests)
		if e.GetErrorType() != ErrorTypeOpenAIError {
			t.Fatalf("precondition: errorType = %q, want openai_error", e.GetErrorType())
		}
		rewritten := fmt.Sprintf("upstream provider deepseek returned 429: Rate limit reached (request id: %s)", requestID)
		e.SetMessage(rewritten)

		oa := e.ToOpenAIError()
		if !strings.Contains(oa.Message, requestID) {
			t.Errorf("OpenAI wire message = %q, want it to carry the request id %q", oa.Message, requestID)
		}
		if !strings.Contains(oa.Message, "upstream provider deepseek returned 429") {
			t.Errorf("OpenAI wire message = %q, want the upstream-provider attribution", oa.Message)
		}
		// The vendor's own type/code/param still belong to the OpenAI wire —
		// only the message has one source of truth.
		if oa.Type != "rate_limit_error" {
			t.Errorf("OpenAI wire type = %q, want the vendor type rate_limit_error", oa.Type)
		}
		if oa.Param != "model" {
			t.Errorf("OpenAI wire param = %q, want model", oa.Param)
		}
		if fmt.Sprintf("%v", oa.Code) != "rate_limit_exceeded" {
			t.Errorf("OpenAI wire code = %v, want the vendor code rate_limit_exceeded", oa.Code)
		}

		cl := e.ToClaudeError()
		if !strings.Contains(cl.Message, requestID) {
			t.Errorf("Anthropic wire message = %q, want it to carry the request id %q", cl.Message, requestID)
		}

		gm := e.ToGeminiError()
		if !strings.Contains(gm.Error.Message, requestID) {
			t.Errorf("Gemini wire message = %q, want it to carry the request id %q", gm.Error.Message, requestID)
		}
		raw, err := json.Marshal(gm)
		if err != nil {
			t.Fatalf("marshal gemini frame: %v", err)
		}
		if !strings.Contains(string(raw), requestID) {
			t.Errorf("Gemini wire bytes = %s, want the request id on the wire", raw)
		}
	})

	t.Run("claude-shaped upstream error", func(t *testing.T) {
		e := WithClaudeError(ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, 529)
		if e.GetErrorType() != ErrorTypeClaudeError {
			t.Fatalf("precondition: errorType = %q, want claude_error", e.GetErrorType())
		}
		rewritten := fmt.Sprintf("upstream provider anthropic returned 529: Overloaded (request id: %s)", requestID)
		e.SetMessage(rewritten)

		cl := e.ToClaudeError()
		if !strings.Contains(cl.Message, requestID) {
			t.Errorf("Anthropic wire message = %q, want it to carry the request id %q", cl.Message, requestID)
		}
		if cl.Type != "overloaded_error" {
			t.Errorf("Anthropic wire type = %q, want the vendor type overloaded_error", cl.Type)
		}

		oa := e.ToOpenAIError()
		if !strings.Contains(oa.Message, requestID) {
			t.Errorf("OpenAI wire message = %q, want it to carry the request id %q", oa.Message, requestID)
		}

		gm := e.ToGeminiError()
		if !strings.Contains(gm.Error.Message, requestID) {
			t.Errorf("Gemini wire message = %q, want it to carry the request id %q", gm.Error.Message, requestID)
		}
	})
}
