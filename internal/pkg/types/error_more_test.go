package types

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// --- Accessors on nil / populated receivers -------------------------------

func TestNewAPIError_NilReceivers(t *testing.T) {
	var e *NewAPIError
	if got := e.Unwrap(); got != nil {
		t.Errorf("nil.Unwrap() = %v, want nil", got)
	}
	if got := e.GetErrorCode(); got != "" {
		t.Errorf("nil.GetErrorCode() = %q, want empty", got)
	}
	if got := e.GetErrorType(); got != "" {
		t.Errorf("nil.GetErrorType() = %q, want empty", got)
	}
	if got := e.Error(); got != "" {
		t.Errorf("nil.Error() = %q, want empty", got)
	}
	if got := e.MaskSensitiveError(); got != "" {
		t.Errorf("nil.MaskSensitiveError() = %q, want empty", got)
	}
}

func TestNewAPIError_Accessors(t *testing.T) {
	inner := errors.New("boom")
	e := &NewAPIError{
		Err:        inner,
		errorType:  ErrorTypeOpenAIError,
		errorCode:  ErrorCodeBadResponse,
		StatusCode: 502,
	}
	if !errors.Is(e.Unwrap(), inner) {
		t.Errorf("Unwrap() did not return underlying error")
	}
	if e.GetErrorCode() != ErrorCodeBadResponse {
		t.Errorf("GetErrorCode() = %q", e.GetErrorCode())
	}
	if e.GetErrorType() != ErrorTypeOpenAIError {
		t.Errorf("GetErrorType() = %q", e.GetErrorType())
	}
	if e.Error() != "boom" {
		t.Errorf("Error() = %q, want boom", e.Error())
	}
	// errors.Is chains through Unwrap.
	if !errors.Is(e, inner) {
		t.Errorf("errors.Is(e, inner) = false, want true")
	}
}

func TestNewAPIError_Error_FallbackToCode(t *testing.T) {
	// Err nil → Error() falls back to the errorCode string.
	e := &NewAPIError{errorCode: ErrorCodeModelNotFound}
	if got := e.Error(); got != string(ErrorCodeModelNotFound) {
		t.Errorf("Error() fallback = %q, want %q", got, ErrorCodeModelNotFound)
	}
}

func TestNewAPIError_SetMessage(t *testing.T) {
	e := &NewAPIError{Err: errors.New("orig"), errorCode: ErrorCodeBadResponse}
	e.SetMessage("replaced")
	if e.Error() != "replaced" {
		t.Errorf("after SetMessage Error() = %q, want replaced", e.Error())
	}
}

// --- MaskSensitiveError ----------------------------------------------------

func TestMaskSensitiveError(t *testing.T) {
	t.Run("nil Err falls back to code", func(t *testing.T) {
		e := &NewAPIError{errorCode: ErrorCodeBadResponse}
		if got := e.MaskSensitiveError(); got != string(ErrorCodeBadResponse) {
			t.Errorf("got %q, want %q", got, ErrorCodeBadResponse)
		}
	})

	t.Run("count_token_failed bypasses masking", func(t *testing.T) {
		raw := "count failed for https://secret.example/v1/KEYABCDEF"
		e := &NewAPIError{Err: errors.New(raw), errorCode: ErrorCodeCountTokenFailed}
		if got := e.MaskSensitiveError(); got != raw {
			t.Errorf("count_token_failed must be verbatim, got %q", got)
		}
	})

	t.Run("credential URL is masked for tenant safety", func(t *testing.T) {
		raw := "auth to https://api.provider.example/v1/SECRETKEY123 failed"
		e := &NewAPIError{Err: errors.New(raw), errorCode: ErrorCodeBadResponse}
		got := e.MaskSensitiveError()
		if strings.Contains(got, "SECRETKEY123") {
			t.Errorf("secret leaked through mask: %q", got)
		}
		if got == raw {
			t.Errorf("expected masking to alter the string, got verbatim %q", got)
		}
	})
}

// --- IsChannelError / IsSkipRetryError / IsRecordErrorLog ------------------

func TestIsChannelError(t *testing.T) {
	cases := []struct {
		name string
		err  *NewAPIError
		want bool
	}{
		{"nil", nil, false},
		{"channel:no_available_key", &NewAPIError{errorCode: ErrorCodeChannelNoAvailableKey}, true},
		{"channel:invalid_key", &NewAPIError{errorCode: ErrorCodeChannelInvalidKey}, true},
		{"non-channel code", &NewAPIError{errorCode: ErrorCodeBadResponse}, false},
		{"empty code", &NewAPIError{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsChannelError(tc.err); got != tc.want {
				t.Errorf("IsChannelError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsSkipRetryError(t *testing.T) {
	if IsSkipRetryError(nil) {
		t.Errorf("nil must not be skip-retry")
	}
	if IsSkipRetryError(&NewAPIError{}) {
		t.Errorf("default (unset) must not be skip-retry")
	}
	e := NewError(errors.New("x"), ErrorCodeBadResponse, ErrOptionWithSkipRetry())
	if !IsSkipRetryError(e) {
		t.Errorf("ErrOptionWithSkipRetry() must set skip-retry")
	}
}

func TestIsRecordErrorLog(t *testing.T) {
	if IsRecordErrorLog(nil) {
		t.Errorf("nil must not be recorded")
	}
	// Default (recordErrorLog nil) → true.
	if !IsRecordErrorLog(&NewAPIError{}) {
		t.Errorf("default must record error log (true)")
	}
	// Option explicitly disables it.
	e := NewError(errors.New("x"), ErrorCodeBadResponse, ErrOptionWithNoRecordErrorLog())
	if IsRecordErrorLog(e) {
		t.Errorf("ErrOptionWithNoRecordErrorLog() must set record=false")
	}
}

// --- Option composition ----------------------------------------------------

func TestErrOptions_Compose(t *testing.T) {
	e := NewError(
		errors.New("original secret"),
		ErrorCodeInsufficientUserQuota,
		ErrOptionWithSkipRetry(),
		ErrOptionWithNoRecordErrorLog(),
		ErrOptionWithHideErrMsg("balance too low"),
		ErrOptionWithTopupURL(),
	)
	if !IsSkipRetryError(e) {
		t.Errorf("skip-retry not applied")
	}
	if IsRecordErrorLog(e) {
		t.Errorf("no-record-error-log not applied")
	}
	if e.Error() != "balance too low" {
		t.Errorf("hide-err-msg not applied, Error() = %q", e.Error())
	}
	// Topup URL landed in Metadata as {"topup_url": ...}.
	var meta map[string]string
	if err := json.Unmarshal(e.Metadata, &meta); err != nil {
		t.Fatalf("Metadata not valid JSON: %v", err)
	}
	want := common.IdentityPublicURL + "/wallet/topup"
	if meta["topup_url"] != want {
		t.Errorf("topup_url = %q, want %q", meta["topup_url"], want)
	}
}

func TestErrOptionWithHideErrMsg_DebugPath(t *testing.T) {
	// Exercise the DebugEnabled branch of ErrOptionWithHideErrMsg.
	old := common.DebugEnabled
	common.DebugEnabled = true
	defer func() { common.DebugEnabled = old }()
	e := NewError(errors.New("origin"), ErrorCodeBadResponse, ErrOptionWithHideErrMsg("hidden"))
	if e.Error() != "hidden" {
		t.Errorf("Error() = %q, want hidden", e.Error())
	}
}

func TestErrOptionWithUpgradeURL(t *testing.T) {
	e := NewError(errors.New("cap"), ErrorCodeTenantQuotaExceeded, ErrOptionWithUpgradeURL())
	var meta map[string]string
	if err := json.Unmarshal(e.Metadata, &meta); err != nil {
		t.Fatalf("Metadata not valid JSON: %v", err)
	}
	want := common.IdentityPublicURL + "/pricing"
	if meta["upgrade_url"] != want {
		t.Errorf("upgrade_url = %q, want %q", meta["upgrade_url"], want)
	}
}

// --- Constructors ----------------------------------------------------------

func TestNewError_DefaultsAndPassthrough(t *testing.T) {
	t.Run("fresh error gets defaults", func(t *testing.T) {
		e := NewError(errors.New("x"), ErrorCodeBadResponse)
		if e.errorType != ErrorTypeNewAPIError {
			t.Errorf("errorType = %q, want new_api_error", e.errorType)
		}
		if e.StatusCode != http.StatusInternalServerError {
			t.Errorf("StatusCode = %d, want 500", e.StatusCode)
		}
		if e.errorCode != ErrorCodeBadResponse {
			t.Errorf("errorCode = %q", e.errorCode)
		}
	})

	t.Run("preserves deep-wrapped NewAPIError and applies new options", func(t *testing.T) {
		inner := NewError(errors.New("deep"), ErrorCodeChannelInvalidKey)
		inner.StatusCode = 418
		wrapped := NewError(inner, ErrorCodeBadResponse, ErrOptionWithSkipRetry())
		// Same underlying error instance is preserved (errorCode unchanged).
		if wrapped != inner {
			t.Errorf("expected passthrough of the wrapped NewAPIError instance")
		}
		if wrapped.errorCode != ErrorCodeChannelInvalidKey {
			t.Errorf("errorCode should stay the inner one, got %q", wrapped.errorCode)
		}
		if !IsSkipRetryError(wrapped) {
			t.Errorf("options must still apply to the passthrough error")
		}
	})
}

func TestNewErrorWithStatusCode(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("bad"), ErrorCodeBadRequestBody, 422)
	if e.StatusCode != 422 {
		t.Errorf("StatusCode = %d, want 422", e.StatusCode)
	}
	if e.errorType != ErrorTypeNewAPIError {
		t.Errorf("errorType = %q", e.errorType)
	}
	oa, ok := e.RelayError.(OpenAIError)
	if !ok {
		t.Fatalf("RelayError not OpenAIError: %T", e.RelayError)
	}
	if oa.Message != "bad" || oa.Type != string(ErrorCodeBadRequestBody) {
		t.Errorf("RelayError = %+v", oa)
	}
}

func TestWithOpenAIError(t *testing.T) {
	t.Run("string code + defaults type", func(t *testing.T) {
		e := WithOpenAIError(OpenAIError{Message: "m", Code: "some_code"}, 400)
		if e.errorType != ErrorTypeOpenAIError {
			t.Errorf("errorType = %q", e.errorType)
		}
		if e.errorCode != ErrorCode("some_code") {
			t.Errorf("errorCode = %q, want some_code", e.errorCode)
		}
		oa := e.RelayError.(OpenAIError)
		if oa.Type != "upstream_error" {
			t.Errorf("empty Type should default to upstream_error, got %q", oa.Type)
		}
	})

	t.Run("non-string code stringified", func(t *testing.T) {
		e := WithOpenAIError(OpenAIError{Message: "m", Type: "t", Code: 42}, 400)
		if e.errorCode != ErrorCode("42") {
			t.Errorf("errorCode = %q, want 42", e.errorCode)
		}
	})

	t.Run("nil code → unknown_error", func(t *testing.T) {
		e := WithOpenAIError(OpenAIError{Message: "m", Type: "t"}, 400)
		if e.errorCode != ErrorCode("unknown_error") {
			t.Errorf("errorCode = %q, want unknown_error", e.errorCode)
		}
	})

	t.Run("metadata merged into message and error metadata", func(t *testing.T) {
		md := json.RawMessage(`{"reason":"rl"}`)
		e := WithOpenAIError(OpenAIError{Message: "hit", Type: "t", Code: "c", Metadata: md}, 429)
		if len(e.Metadata) == 0 {
			t.Errorf("expected Metadata to be carried onto the error")
		}
		if !strings.Contains(e.Error(), `{"reason":"rl"}`) {
			t.Errorf("metadata should be appended to message, got %q", e.Error())
		}
	})
}

func TestNewOpenAIError(t *testing.T) {
	t.Run("fresh error builds OpenAI relay error", func(t *testing.T) {
		e := NewOpenAIError(errors.New("upstream boom"), ErrorCodeBadResponse, 502)
		if e.errorType != ErrorTypeOpenAIError {
			t.Errorf("errorType = %q", e.errorType)
		}
		if e.StatusCode != 502 {
			t.Errorf("StatusCode = %d, want 502", e.StatusCode)
		}
		oa := e.RelayError.(OpenAIError)
		if oa.Message != "upstream boom" {
			t.Errorf("relay message = %q", oa.Message)
		}
	})

	t.Run("wrapped NewAPIError without RelayError gets one synthesized", func(t *testing.T) {
		inner := NewError(errors.New("deep msg"), ErrorCodeBadResponse) // RelayError nil
		if inner.RelayError != nil {
			t.Fatalf("precondition: inner.RelayError should be nil")
		}
		out := NewOpenAIError(inner, ErrorCodeModelNotFound, 404, ErrOptionWithSkipRetry())
		if out != inner {
			t.Errorf("expected passthrough of wrapped error")
		}
		oa, ok := out.RelayError.(OpenAIError)
		if !ok {
			t.Fatalf("RelayError not synthesized: %T", out.RelayError)
		}
		if oa.Message != "deep msg" {
			t.Errorf("synthesized message = %q", oa.Message)
		}
		if !IsSkipRetryError(out) {
			t.Errorf("options must apply to passthrough error")
		}
	})
}

func TestInitOpenAIError(t *testing.T) {
	e := InitOpenAIError(ErrorCodeModelNotFound, 404)
	if e.errorType != ErrorTypeOpenAIError {
		t.Errorf("errorType = %q", e.errorType)
	}
	if e.StatusCode != 404 {
		t.Errorf("StatusCode = %d, want 404", e.StatusCode)
	}
	if e.errorCode != ErrorCodeModelNotFound {
		t.Errorf("errorCode = %q", e.errorCode)
	}
}

func TestWithClaudeError(t *testing.T) {
	t.Run("empty type defaults to upstream_error", func(t *testing.T) {
		e := WithClaudeError(ClaudeError{Message: "m"}, 500)
		if e.errorType != ErrorTypeClaudeError {
			t.Errorf("errorType = %q", e.errorType)
		}
		if e.errorCode != ErrorCode("upstream_error") {
			t.Errorf("errorCode = %q, want upstream_error", e.errorCode)
		}
	})
	t.Run("explicit type kept as code", func(t *testing.T) {
		e := WithClaudeError(ClaudeError{Type: "overloaded_error", Message: "busy"}, 529)
		if e.errorCode != ErrorCode("overloaded_error") {
			t.Errorf("errorCode = %q", e.errorCode)
		}
		if e.StatusCode != 529 {
			t.Errorf("StatusCode = %d", e.StatusCode)
		}
	})
}

// --- ToOpenAIError / ToClaudeError serialization ---------------------------

func TestToOpenAIError_Serialization(t *testing.T) {
	t.Run("openai relay error passed through", func(t *testing.T) {
		oa := OpenAIError{Message: "plain msg", Type: "invalid_request_error", Param: "model", Code: "x"}
		e := WithOpenAIError(oa, 400)
		out := e.ToOpenAIError()
		if out.Type != "invalid_request_error" || out.Param != "model" {
			t.Errorf("openai error not passed through: %+v", out)
		}
	})

	t.Run("claude relay error converted to openai shape", func(t *testing.T) {
		e := WithClaudeError(ClaudeError{Type: "overloaded_error", Message: "busy"}, 529)
		out := e.ToOpenAIError()
		if out.Type != "overloaded_error" {
			t.Errorf("claude type should map to openai Type, got %q", out.Type)
		}
		if out.Message == "" {
			t.Errorf("expected a message")
		}
	})

	t.Run("default type uses errorType string and empty→errorType", func(t *testing.T) {
		e := &NewAPIError{errorType: ErrorTypeUpstreamError, errorCode: ErrorCodeBadResponse}
		out := e.ToOpenAIError()
		// Err nil → Error() = errorCode; masked. Non-empty so Type is upstream_error.
		if out.Type != string(ErrorTypeUpstreamError) {
			t.Errorf("Type = %q, want upstream_error", out.Type)
		}
	})

	t.Run("empty message falls back to errorType", func(t *testing.T) {
		// errorCode empty + Err nil → Error() returns "" → message falls back to errorType.
		e := &NewAPIError{errorType: ErrorTypeGeminiError}
		out := e.ToOpenAIError()
		if out.Message != string(ErrorTypeGeminiError) {
			t.Errorf("empty message should fall back to errorType, got %q", out.Message)
		}
	})

	t.Run("metadata forwarded and not masked", func(t *testing.T) {
		e := NewError(errors.New("balance low"), ErrorCodeInsufficientUserQuota, ErrOptionWithTopupURL())
		out := e.ToOpenAIError()
		if len(out.Metadata) == 0 {
			t.Fatalf("Metadata must be forwarded to client envelope")
		}
		if !strings.Contains(string(out.Metadata), "topup_url") {
			t.Errorf("topup_url should survive verbatim, got %s", out.Metadata)
		}
	})

	t.Run("count_token_failed message not masked", func(t *testing.T) {
		raw := "count for https://api.example/v1/SECRET failed"
		e := NewError(errors.New(raw), ErrorCodeCountTokenFailed)
		out := e.ToOpenAIError()
		if out.Message != raw {
			t.Errorf("count_token_failed message must be verbatim, got %q", out.Message)
		}
	})
}

func TestToClaudeError_Serialization(t *testing.T) {
	t.Run("openai relay error mapped to claude", func(t *testing.T) {
		e := WithOpenAIError(OpenAIError{Message: "boom", Type: "t", Code: "rate_limit_error"}, 429)
		out := e.ToClaudeError()
		if out.Type != "rate_limit_error" {
			t.Errorf("Type should be openai Code stringified, got %q", out.Type)
		}
		if out.Message == "" {
			t.Errorf("expected message")
		}
	})

	t.Run("claude relay error passed through", func(t *testing.T) {
		e := WithClaudeError(ClaudeError{Type: "overloaded_error", Message: "busy"}, 529)
		out := e.ToClaudeError()
		if out.Type != "overloaded_error" {
			t.Errorf("Type = %q", out.Type)
		}
	})

	t.Run("default type maps the status via WireErrorType", func(t *testing.T) {
		// L3-CONTRACT-TAXONOMY: NewError defaults StatusCode to 500, so the
		// default branch now reports the Anthropic vendor type for a 5xx
		// (api_error) instead of the retired new_api_error literal.
		e := NewError(errors.New("internal boom"), ErrorCodeBadResponse)
		out := e.ToClaudeError()
		if out.Type != "api_error" {
			t.Errorf("Type = %q, want api_error", out.Type)
		}
	})

	t.Run("empty message falls back to errorType", func(t *testing.T) {
		e := &NewAPIError{errorType: ErrorTypeRerankError}
		out := e.ToClaudeError()
		if out.Message != string(ErrorTypeRerankError) {
			t.Errorf("message fallback = %q, want rerank_error", out.Message)
		}
	})

	t.Run("count_token_failed not masked", func(t *testing.T) {
		raw := "https://api.example/v1/SECRETXYZ count fail"
		e := NewError(errors.New(raw), ErrorCodeCountTokenFailed)
		out := e.ToClaudeError()
		if out.Message != raw {
			t.Errorf("verbatim expected, got %q", out.Message)
		}
	})

	t.Run("masking scrubs secrets in message", func(t *testing.T) {
		e := WithClaudeError(ClaudeError{Type: "api_error", Message: "auth https://x.example/v1/SECRETKEY"}, 401)
		out := e.ToClaudeError()
		if strings.Contains(out.Message, "SECRETKEY") {
			t.Errorf("secret leaked: %q", out.Message)
		}
	})
}

// Guard: wrapped-cancellation classification is stable through constructors.
func TestNewError_ContextCanceledClassification(t *testing.T) {
	e := NewError(context.Canceled, ErrorCodeDoRequestFailed)
	e.StatusCode = 0
	if IsUpstreamFailure(e) {
		t.Errorf("context.Canceled must not be an upstream failure")
	}
}
