package app

// r2_channel_token_quota_test.go — R2 (B2 mutation proof): app/channel.go's
// ShouldDisableChannel must keep auto-disabling a channel that reports the
// per-token spending-cap 402 introduced by B2.
//
// This is a ROUND-TRIP probe, not a hand-built OpenAIError: the original
// version of this test constructed types.WithOpenAIError with Type set
// directly to "token_quota_exhausted", a shape production never emits — both
// production sources (middleware.TokenAuth and PreConsumeQuota's
// ErrTokenQuotaInsufficient branch) build the error via
// types.NewErrorWithStatusCode(..., types.ErrorCodeTokenQuotaExhausted, ...),
// whose ToOpenAIError() default branch ALWAYS sets Type to the ErrorType
// constant ("new_api_error"), never to the ErrorCode. So the wire body this
// service actually sends is {"type":"new_api_error","code":"token_quota_exhausted"},
// not the reverse. The hand-built shape made the old test pass while the
// real ShouldDisableChannel(channelType, realErr) call — reachable when this
// newhub relays through ANOTHER newhub/newapi instance as an upstream
// channel — returned false, a silent regression of channel auto-disable.
//
// This test instead: (1) builds the error exactly like auth.go/
// pre_consume_quota.go do, (2) marshals it into the same envelope
// TokenAuth's c.JSON(...) sends, (3) feeds that body through
// app.RelayErrorHandler (the real parser a downstream instance uses when
// THIS error arrives as an upstream HTTP response), and only then (4) calls
// ShouldDisableChannel on the round-tripped result — so the assertion can
// only pass if the production Code (not Type) switch case in channel.go
// actually fires.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// r2BuildTokenQuotaExhaustedResponse constructs the exact NewAPIError shape
// auth.go's ErrTokenQuotaExhausted branch and PreConsumeQuota's
// ErrTokenQuotaInsufficient branch both emit, and marshals it into the same
// {"error": {...}} envelope TokenAuth's c.JSON(...) sends over the wire.
func r2BuildTokenQuotaExhaustedResponse(t *testing.T, remainQuota int) *http.Response {
	t.Helper()
	apiErr := types.NewErrorWithStatusCode(errors.New("该令牌额度已用尽"), types.ErrorCodeTokenQuotaExhausted,
		http.StatusPaymentRequired, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog(),
		types.ErrOptionWithTokenQuotaHint(remainQuota))
	oaiErr := apiErr.ToOpenAIError()
	body, err := json.Marshal(map[string]any{"error": oaiErr})
	if err != nil {
		t.Fatalf("marshal wire body: %v", err)
	}
	return &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func TestR2ShouldDisableChannel_TokenQuotaExhausted_RoundTrip_ReturnsTrue(t *testing.T) {
	saveAndRestoreAutoBanFlags(t)
	common.AutomaticDisableChannelEnabled = true

	resp := r2BuildTokenQuotaExhaustedResponse(t, 500)
	defer func() { _ = resp.Body.Close() }()
	parsed := RelayErrorHandler(context.Background(), resp, true)
	if parsed == nil {
		t.Fatal("RelayErrorHandler returned nil for a well-formed token_quota_exhausted body")
	}

	if !ShouldDisableChannel(1, parsed) {
		t.Error("expected true: a channel that is itself an upstream newhub reporting its own token-cap 402 must be auto-disabled, same as insufficient_user_quota")
	}
}

// TestR2ShouldDisableChannel_TokenQuotaExhausted_HandBuiltShapeAlsoTrue keeps
// the ORIGINAL construction alive as a documented negative control: it must
// NOT be mistaken for proof the production path works (see the file header),
// but the Code-switch case this fix adds also happens to match a hand-built
// OpenAIError whose Code (not Type) is set to "token_quota_exhausted" — this
// only pins that the case exists in the switch actually reachable by the
// round-trip above (oaiErr.Code), not the switch that never fires
// (oaiErr.Type).
func TestR2ShouldDisableChannel_TokenQuotaExhausted_HandBuiltShapeAlsoTrue(t *testing.T) {
	saveAndRestoreAutoBanFlags(t)
	common.AutomaticDisableChannelEnabled = true

	apiErr := types.WithOpenAIError(types.OpenAIError{
		Message: "该令牌额度已用尽",
		Type:    "new_api_error",
		Code:    "token_quota_exhausted",
	}, http.StatusPaymentRequired)

	if !ShouldDisableChannel(1, apiErr) {
		t.Error("expected true for token_quota_exhausted code")
	}
}
