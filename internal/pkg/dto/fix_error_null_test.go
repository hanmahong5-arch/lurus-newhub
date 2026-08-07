package dto

import (
	"encoding/json"
	"testing"
)

// TestGeneralErrorResponse_ToMessage_NullErrorFallsThrough guards an upstream
// body that sends an explicit `"error": null`. json.RawMessage keeps the 4
// literal bytes `null`, so the len>0 guard passes and the type switch used to
// hit its default branch and return the string "null", masking every fallback
// field below it.
func TestGeneralErrorResponse_ToMessage_NullErrorFallsThrough(t *testing.T) {
	t.Run("literal null raw message", func(t *testing.T) {
		resp := GeneralErrorResponse{
			Error:   json.RawMessage("null"),
			Message: "real message",
		}
		if got := resp.ToMessage(); got != "real message" {
			t.Errorf("ToMessage() = %q, want %q", got, "real message")
		}
	})

	t.Run("unmarshalled from upstream body", func(t *testing.T) {
		var resp GeneralErrorResponse
		if err := json.Unmarshal([]byte(`{"error":null,"message":"real message"}`), &resp); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		// Precondition of the defect: RawMessage keeps the literal bytes, so the
		// len(e.Error) > 0 guard in ToMessage is entered.
		if string(resp.Error) != "null" {
			t.Fatalf("Error raw bytes = %q, want %q", string(resp.Error), "null")
		}
		if got := resp.ToMessage(); got != "real message" {
			t.Errorf("ToMessage() = %q, want %q", got, "real message")
		}
	})

	t.Run("null error falls through to later fields", func(t *testing.T) {
		resp := GeneralErrorResponse{Error: json.RawMessage("null")}
		resp.Response.Error.Message = "deep message"
		if got := resp.ToMessage(); got != "deep message" {
			t.Errorf("ToMessage() = %q, want %q", got, "deep message")
		}
	})

	t.Run("null error with no fallback returns empty", func(t *testing.T) {
		resp := GeneralErrorResponse{Error: json.RawMessage("null")}
		if got := resp.ToMessage(); got != "" {
			t.Errorf("ToMessage() = %q, want empty string", got)
		}
	})
}

// TestGeneralErrorResponse_ToMessage_NonNullUnchanged pins the branches the fix
// must not alter.
func TestGeneralErrorResponse_ToMessage_NonNullUnchanged(t *testing.T) {
	object := GeneralErrorResponse{
		Error:   json.RawMessage(`{"message":"from error object"}`),
		Message: "ignored",
	}
	if got := object.ToMessage(); got != "from error object" {
		t.Errorf("object: ToMessage() = %q, want %q", got, "from error object")
	}

	str := GeneralErrorResponse{
		Error:   json.RawMessage(`"from error string"`),
		Message: "ignored",
	}
	if got := str.ToMessage(); got != "from error string" {
		t.Errorf("string: ToMessage() = %q, want %q", got, "from error string")
	}

	number := GeneralErrorResponse{
		Error:   json.RawMessage(`429`),
		Message: "ignored",
	}
	if got := number.ToMessage(); got != "429" {
		t.Errorf("number: ToMessage() = %q, want %q", got, "429")
	}
}
