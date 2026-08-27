package types

// r2_token_quota_code_test.go — R2 (B2/G1): pins the wire-level contract of
// the new per-TOKEN-cap error code introduced to stop routing token spending
// caps to the wallet top-up guidance (see middleware.TokenAuth and
// PreConsumeQuota's ErrTokenQuotaInsufficient branch).
//
// Three things must never silently drift apart again:
//  1. ErrorCodeTokenQuotaExhausted MUST classify as "insufficient_quota" in
//     RelayErrorType's metric bucket — omitting it from that switch does not
//     fail loudly, it silently reclassifies every token-cap 402 as
//     "upstream_4xx" in the relay_errors_total metric (verified: the switch
//     at error.go:437-441 is the only classifier).
//  2. ErrOptionWithTokenQuotaHint's metadata shape (reason + remaining
//     quota under the token_remain_quota_units key, no topup_url, no
//     management URL, no Retry-After — operator decisions
//     D-B2/D-B2b/D-B2c) is the exact JSON a client parses. The key is
//     deliberately NOT "token_remain_quota" (see error.go's doc comment on
//     ErrOptionWithTokenQuotaHint): that name collided with a currency-
//     formatted number of a different magnitude in the same response's
//     .message field.
//  3. remainQuota is forwarded to the client uninterpreted — including
//     negative values, which a concurrent settlement can legitimately
//     produce between the read and the 402 being built.

import (
	"encoding/json"
	"testing"
)

func TestR2RelayErrorType_TokenQuotaExhausted_IsInsufficientQuota(t *testing.T) {
	err := &NewAPIError{errorCode: ErrorCodeTokenQuotaExhausted, StatusCode: 402}
	if got := RelayErrorType(err); got != "insufficient_quota" {
		t.Errorf("RelayErrorType(token_quota_exhausted) = %q, want %q", got, "insufficient_quota")
	}
}

func TestR2ErrOptionWithTokenQuotaHint_MetadataShape(t *testing.T) {
	e := &NewAPIError{}
	ErrOptionWithTokenQuotaHint(1234)(e)

	if len(e.Metadata) == 0 {
		t.Fatal("expected non-empty metadata from ErrOptionWithTokenQuotaHint")
	}

	var got map[string]any
	if err := json.Unmarshal(e.Metadata, &got); err != nil {
		t.Fatalf("unmarshal metadata: %v; raw=%s", err, e.Metadata)
	}

	if got["reason"] != "token_quota_exhausted" {
		t.Errorf(`metadata["reason"] = %v, want "token_quota_exhausted"`, got["reason"])
	}
	remain, ok := got["token_remain_quota_units"].(float64)
	if !ok || remain != 1234 {
		t.Errorf(`metadata["token_remain_quota_units"] = %v, want 1234`, got["token_remain_quota_units"])
	}
	if _, present := got["token_remain_quota"]; present {
		t.Errorf("metadata must use the unambiguous token_remain_quota_units key, not the old token_remain_quota name, got: %s", e.Metadata)
	}
	if _, present := got["topup_url"]; present {
		t.Errorf("metadata must NOT carry topup_url for a per-token cap 402, got: %s", e.Metadata)
	}
	if _, present := got["upgrade_url"]; present {
		t.Errorf("metadata must NOT carry upgrade_url for a per-token cap 402, got: %s", e.Metadata)
	}
}

func TestR2ErrOptionWithTokenQuotaHint_ZeroRemainQuota(t *testing.T) {
	e := &NewAPIError{}
	ErrOptionWithTokenQuotaHint(0)(e)

	var got map[string]any
	if err := json.Unmarshal(e.Metadata, &got); err != nil {
		t.Fatalf("unmarshal metadata: %v; raw=%s", err, e.Metadata)
	}
	remain, ok := got["token_remain_quota_units"].(float64)
	if !ok || remain != 0 {
		t.Errorf(`metadata["token_remain_quota_units"] = %v, want 0`, got["token_remain_quota_units"])
	}
}

// TestR2ErrOptionWithTokenQuotaHint_NegativeRemainQuota pins TODAY's behavior
// (operator decision, not a fix): a concurrent settlement can drain a
// token's remain_quota below zero between PreConsumeTokenQuota's rejection
// read and this hint being attached, and the negative figure is disclosed
// to the client as-is — no flooring at zero.
func TestR2ErrOptionWithTokenQuotaHint_NegativeRemainQuota(t *testing.T) {
	e := &NewAPIError{}
	ErrOptionWithTokenQuotaHint(-50)(e)

	var got map[string]any
	if err := json.Unmarshal(e.Metadata, &got); err != nil {
		t.Fatalf("unmarshal metadata: %v; raw=%s", err, e.Metadata)
	}
	remain, ok := got["token_remain_quota_units"].(float64)
	if !ok || remain != -50 {
		t.Errorf(`metadata["token_remain_quota_units"] = %v, want -50 (negative values pass through unfloored)`, got["token_remain_quota_units"])
	}
}

// TestR2ErrOptionWithTokenDisabledHint_MetadataShape pins the sibling hint
// used when Status==TokenStatusExhausted but RemainQuota is actually
// positive/unlimited (B2 follow-up, repo/token.go's ValidateUserToken): the
// reason must be distinguishable from the genuine quota-cap case, since the
// remedy (re-enable the token) is different from "edit remain_quota".
func TestR2ErrOptionWithTokenDisabledHint_MetadataShape(t *testing.T) {
	e := &NewAPIError{}
	ErrOptionWithTokenDisabledHint(5000)(e)

	var got map[string]any
	if err := json.Unmarshal(e.Metadata, &got); err != nil {
		t.Fatalf("unmarshal metadata: %v; raw=%s", err, e.Metadata)
	}
	if got["reason"] != "token_disabled" {
		t.Errorf(`metadata["reason"] = %v, want "token_disabled"`, got["reason"])
	}
	if got["reason"] == "token_quota_exhausted" {
		t.Error("token_disabled hint must not reuse token_quota_exhausted's reason — the remedies differ")
	}
	remain, ok := got["token_remain_quota_units"].(float64)
	if !ok || remain != 5000 {
		t.Errorf(`metadata["token_remain_quota_units"] = %v, want 5000`, got["token_remain_quota_units"])
	}
}
