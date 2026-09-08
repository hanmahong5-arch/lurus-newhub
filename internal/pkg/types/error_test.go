package types

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIsUpstreamFailure(t *testing.T) {
	tests := []struct {
		name string
		err  *NewAPIError
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{
			name: "channel:* code (network/key issue) → upstream",
			err:  &NewAPIError{errorCode: ErrorCodeChannelNoAvailableKey, StatusCode: 503},
			want: true,
		},
		{
			name: "client cancellation → not upstream",
			err:  &NewAPIError{Err: context.Canceled, StatusCode: 0},
			want: false,
		},
		{
			name: "wrapped client cancellation → not upstream",
			err:  &NewAPIError{Err: fmt.Errorf("wrap: %w", context.Canceled), StatusCode: 0},
			want: false,
		},
		{name: "500 → upstream", err: &NewAPIError{StatusCode: 500}, want: true},
		{name: "502 → upstream", err: &NewAPIError{StatusCode: 502}, want: true},
		{name: "503 → upstream", err: &NewAPIError{StatusCode: 503}, want: true},
		{name: "504 (gateway timeout) → upstream", err: &NewAPIError{StatusCode: 504}, want: true},
		{name: "524 (CF origin timeout) → upstream", err: &NewAPIError{StatusCode: 524}, want: true},
		{name: "408 (request timeout) → upstream", err: &NewAPIError{StatusCode: 408}, want: true},
		{name: "400 (bad request) → user error", err: &NewAPIError{StatusCode: 400}, want: false},
		{name: "401 (unauthorized) → user error", err: &NewAPIError{StatusCode: 401}, want: false},
		{name: "403 (forbidden) → user error", err: &NewAPIError{StatusCode: 403}, want: false},
		{name: "404 (not found) → user error", err: &NewAPIError{StatusCode: 404}, want: false},
		{name: "429 (rate limit) → user error", err: &NewAPIError{StatusCode: 429}, want: false},
		{
			name: "no status, no cancellation (raw network err) → fail-safe upstream",
			err:  &NewAPIError{Err: errors.New("dial tcp: timeout"), StatusCode: 0},
			want: true,
		},
		{
			name: "channel:* takes precedence over 4xx-shaped status",
			err:  &NewAPIError{errorCode: ErrorCodeChannelInvalidKey, StatusCode: 401},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUpstreamFailure(tt.err); got != tt.want {
				t.Errorf("IsUpstreamFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRelayErrorType(t *testing.T) {
	tests := []struct {
		name string
		err  *NewAPIError
		want string
	}{
		{"nil → internal", nil, "internal"},
		// Quota/credit exhaustion wins on errorCode regardless of status shape.
		{"insufficient user quota", &NewAPIError{errorCode: ErrorCodeInsufficientUserQuota, StatusCode: 500}, "insufficient_quota"},
		{"pre-consume quota failed", &NewAPIError{errorCode: ErrorCodePreConsumeTokenQuotaFailed, StatusCode: 500}, "insufficient_quota"},
		{"tenant quota exceeded (403 overridden by code)", &NewAPIError{errorCode: ErrorCodeTenantQuotaExceeded, StatusCode: 403}, "insufficient_quota"},
		// newhub-internal / request-prep failures carrying synthetic 500s must NOT
		// be counted as upstream provider faults.
		{"invalid request → internal", &NewAPIError{errorCode: ErrorCodeInvalidRequest, StatusCode: 500}, "internal"},
		{"gen relay info failed → internal", &NewAPIError{errorCode: ErrorCodeGenRelayInfoFailed, StatusCode: 500}, "internal"},
		{"count token failed → internal", &NewAPIError{errorCode: ErrorCodeCountTokenFailed, StatusCode: 500}, "internal"},
		{"get channel failed → internal", &NewAPIError{errorCode: ErrorCodeGetChannelFailed, StatusCode: 500}, "internal"},
		// Upstream exchange: status-driven sub-classes.
		{"429 → upstream_rate_limit", &NewAPIError{StatusCode: 429}, "upstream_rate_limit"},
		{"408 → upstream_timeout", &NewAPIError{StatusCode: 408}, "upstream_timeout"},
		{"504 → upstream_timeout", &NewAPIError{StatusCode: 504}, "upstream_timeout"},
		{"524 → upstream_timeout", &NewAPIError{StatusCode: 524}, "upstream_timeout"},
		{"500 → upstream_5xx", &NewAPIError{StatusCode: 500}, "upstream_5xx"},
		{"502 → upstream_5xx", &NewAPIError{StatusCode: 502}, "upstream_5xx"},
		{"400 (provider rejected) → upstream_4xx", &NewAPIError{StatusCode: 400}, "upstream_4xx"},
		{"401 (bad upstream key) → upstream_4xx", &NewAPIError{StatusCode: 401}, "upstream_4xx"},
		// Transport failure (B1's hung-provider path) → synthetic 500 → upstream_5xx.
		{"do_request_failed → upstream_5xx", &NewAPIError{errorCode: ErrorCodeDoRequestFailed, StatusCode: 500}, "upstream_5xx"},
		// Channel capacity exhaustion with no status → fail-safe upstream_5xx.
		{"all-keys-cooling (status 0) → upstream_5xx", &NewAPIError{errorCode: ErrorCodeChannelAllKeysCooling, StatusCode: 0}, "upstream_5xx"},
		{"unclassified status 0 → upstream_5xx", &NewAPIError{StatusCode: 0}, "upstream_5xx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RelayErrorType(tt.err); got != tt.want {
				t.Errorf("RelayErrorType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUpstreamProviderContext_ShapingAndMasking exercises the B4 client-error
// shaping: prefixing the message with provider context (as the relay final-error
// defer does for upstream failures) must survive into the client envelope while
// MaskSensitiveInfo still scrubs secrets. Uses ErrorTypeNewAPIError (the
// do_request_failed / hung-provider path) where SetMessage drives the rendered
// message — same set of errors the existing request-id wrap shapes.
func TestUpstreamProviderContext_ShapingAndMasking(t *testing.T) {
	orig := "connect to https://secret.upstream.example/v1/KEY12345SECRET failed"
	e := NewError(errors.New(orig), ErrorCodeDoRequestFailed)
	e.StatusCode = 500
	e.SetMessage(fmt.Sprintf("upstream provider %s returned %d: %s", "openai", e.StatusCode, e.Error()))

	out := e.ToOpenAIError()

	if !strings.Contains(out.Message, "upstream provider openai returned 500") {
		t.Errorf("expected provider-context prefix in client message, got %q", out.Message)
	}
	if strings.Contains(out.Message, "KEY12345SECRET") {
		t.Errorf("masking guard: sensitive URL path leaked through, got %q", out.Message)
	}
	if e.StatusCode != 500 {
		t.Errorf("status code must be unchanged by message shaping, got %d", e.StatusCode)
	}
}

// TestRelayErrorType_UpstreamInsufficientBalance separates "our provider
// account is unpaid" from upstream_4xx, the bucket that means "the caller sent
// a bad request". Reading an unpaid invoice as customer error points whoever is
// on call at the wrong party, and it is the one failure class here that
// retrying or failing over cannot fix.
func TestRelayErrorType_UpstreamInsufficientBalance(t *testing.T) {
	t.Run("upstream 402", func(t *testing.T) {
		// DeepSeek answers 402 when the account balance is exhausted.
		e := WithOpenAIError(OpenAIError{Message: "Insufficient Balance"}, http.StatusPaymentRequired)
		if got := RelayErrorType(e); got != "upstream_insufficient_balance" {
			t.Errorf("RelayErrorType = %q, want upstream_insufficient_balance", got)
		}
	})

	t.Run("upstream error codes", func(t *testing.T) {
		for _, code := range []string{"insufficient_quota", "billing_not_active", "Arrearage"} {
			e := WithOpenAIError(OpenAIError{Code: code, Message: "no credit"}, http.StatusForbidden)
			if got := RelayErrorType(e); got != "upstream_insufficient_balance" {
				t.Errorf("code %q: RelayErrorType = %q, want upstream_insufficient_balance", code, got)
			}
		}
	})

	// The critical non-regression: OUR OWN 402s are the caller running out of
	// credit and must stay in insufficient_quota. If this classification were
	// placed before the error-code switch it would swallow all of them, and the
	// customer-facing quota signal would silently become an upstream signal.
	t.Run("our own 402s are unaffected", func(t *testing.T) {
		for _, code := range []ErrorCode{
			ErrorCodeInsufficientUserQuota,
			ErrorCodeTokenQuotaExhausted,
			ErrorCodeTenantQuotaExceeded,
			ErrorCodePreConsumeTokenQuotaFailed,
		} {
			e := &NewAPIError{errorCode: code, StatusCode: http.StatusPaymentRequired}
			if got := RelayErrorType(e); got != "insufficient_quota" {
				t.Errorf("%s: RelayErrorType = %q, want insufficient_quota — this is the "+
					"caller's balance, not ours", code, got)
			}
		}
	})

	// An ordinary upstream 4xx must not drift into the new bucket.
	t.Run("plain upstream 4xx stays upstream_4xx", func(t *testing.T) {
		e := WithOpenAIError(OpenAIError{Code: "invalid_request_error"}, http.StatusBadRequest)
		if got := RelayErrorType(e); got != "upstream_4xx" {
			t.Errorf("RelayErrorType = %q, want upstream_4xx", got)
		}
	})
}

// TestWireErrorType drives the real WireErrorType table directly: every
// status this lane's 29+13-site sweep can produce, on both wires, plus the
// three divergence points (402, 503, 529) between them.
func TestWireErrorType(t *testing.T) {
	cases := []struct {
		status int
		wire   ErrorType
		want   string
	}{
		{http.StatusBadRequest, ErrorTypeOpenAIError, "invalid_request_error"},
		{http.StatusBadRequest, ErrorTypeClaudeError, "invalid_request_error"},
		{http.StatusUnauthorized, ErrorTypeOpenAIError, "authentication_error"},
		{http.StatusUnauthorized, ErrorTypeClaudeError, "authentication_error"},
		// 402 diverges: OpenAI's insufficient_quota vs Anthropic's billing_error.
		{http.StatusPaymentRequired, ErrorTypeOpenAIError, "insufficient_quota"},
		{http.StatusPaymentRequired, ErrorTypeClaudeError, "billing_error"},
		{http.StatusForbidden, ErrorTypeOpenAIError, "permission_error"},
		{http.StatusForbidden, ErrorTypeClaudeError, "permission_error"},
		{http.StatusNotFound, ErrorTypeOpenAIError, "not_found_error"},
		{http.StatusNotFound, ErrorTypeClaudeError, "not_found_error"},
		{http.StatusRequestEntityTooLarge, ErrorTypeOpenAIError, "request_too_large"},
		{http.StatusRequestEntityTooLarge, ErrorTypeClaudeError, "request_too_large"},
		{http.StatusTooManyRequests, ErrorTypeOpenAIError, "rate_limit_error"},
		{http.StatusTooManyRequests, ErrorTypeClaudeError, "rate_limit_error"},
		// 529 diverges: Anthropic-only overloaded_error; OpenAI has no 529 in
		// practice, so the fallback (5xx -> api_error) applies on that wire.
		{529, ErrorTypeOpenAIError, "api_error"},
		{529, ErrorTypeClaudeError, "overloaded_error"},
		{http.StatusInternalServerError, ErrorTypeOpenAIError, "api_error"},
		{http.StatusInternalServerError, ErrorTypeClaudeError, "api_error"},
		{http.StatusBadGateway, ErrorTypeOpenAIError, "api_error"},
		// 503 also diverges (L3-CONTRACT-TAXONOMY item 7,
		// channel:all_keys_cooling): OpenAI's 503 stays the plain 5xx
		// api_error bucket, Anthropic's uses overloaded_error like 529 does.
		{http.StatusServiceUnavailable, ErrorTypeOpenAIError, "api_error"},
		{http.StatusServiceUnavailable, ErrorTypeClaudeError, "overloaded_error"},
		{http.StatusGatewayTimeout, ErrorTypeOpenAIError, "api_error"},
		// Unmapped 4xx falls back to invalid_request_error, never the retired
		// new_api_error literal.
		{http.StatusConflict, ErrorTypeOpenAIError, "invalid_request_error"},
		{http.StatusNotImplemented, ErrorTypeOpenAIError, "api_error"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.wire), func(t *testing.T) {
			if got := WireErrorType(tc.status, tc.wire); got != tc.want {
				t.Errorf("WireErrorType(%d, %s) = %q, want %q", tc.status, tc.wire, got, tc.want)
			}
		})
	}
}

// TestToOpenAIError_DefaultBranch_UsesWireErrorType pins the root-converter
// fix through the real NewAPIError/ToOpenAIError path (not a hand-built
// OpenAIError): a gateway-originated (ErrorTypeNewAPIError) rejection must
// report the vendor-taxonomy type keyed off StatusCode, never the bare
// "new_api_error" literal — while a non-NewAPIError default-falling type
// (e.g. midjourney_error) must keep stamping its own literal unchanged.
func TestToOpenAIError_DefaultBranch_UsesWireErrorType(t *testing.T) {
	t.Run("ErrorTypeNewAPIError maps via WireErrorType", func(t *testing.T) {
		e := NewErrorWithStatusCode(errors.New("boom"), ErrorCodeModelBlocked, http.StatusForbidden)
		out := e.ToOpenAIError()
		if out.Type != "permission_error" {
			t.Errorf("Type = %q, want permission_error", out.Type)
		}
		if out.Code != ErrorCodeModelBlocked {
			t.Errorf("Code = %v, want %q", out.Code, ErrorCodeModelBlocked)
		}
	})

	t.Run("other default-falling ErrorTypes keep their own literal", func(t *testing.T) {
		e := &NewAPIError{errorType: ErrorTypeMidjourneyError, errorCode: ErrorCodeBadResponse, StatusCode: http.StatusForbidden, Err: errors.New("mj boom")}
		out := e.ToOpenAIError()
		if out.Type != "midjourney_error" {
			t.Errorf("Type = %q, want midjourney_error (unchanged)", out.Type)
		}
	})
}

// TestToClaudeError_DefaultBranch_UsesWireErrorType mirrors the OpenAI test
// above for the Anthropic converter, including the 402 divergence.
func TestToClaudeError_DefaultBranch_UsesWireErrorType(t *testing.T) {
	e := NewErrorWithStatusCode(errors.New("boom"), ErrorCodeTokenQuotaExhausted, http.StatusPaymentRequired)
	out := e.ToClaudeError()
	if out.Type != "billing_error" {
		t.Errorf("Type = %q, want billing_error", out.Type)
	}
}
