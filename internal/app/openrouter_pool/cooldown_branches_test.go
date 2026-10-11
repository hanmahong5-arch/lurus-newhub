package openrouter_pool

import (
	"net/http"
	"testing"
	"time"
)

// These tests reach the residual branches of ParseCooldownUntil /
// interpretResetValue / extractFromBodyMetadata that cooldown_test.go's
// happy-path table does not: the float-formatted reset header, the
// non-positive reset value, the invalid-JSON body, and the Retry-After header
// embedded in the body metadata block. Each branch changes how long a
// rate-limited key is parked, so getting them wrong directly harms capacity
// (over-long park) or triggers 429 storms (too-short park).

// TestInterpretResetValue_FloatHeader covers the ParseInt-fails/ParseFloat-ok
// path: OpenRouter is documented to *possibly* return a decimal reset. A
// "45.9" seconds-from-now hint must be truncated to 45 and treated as a valid
// future deadline (clamped up to the 30s floor is not needed here since 45>30).
func TestInterpretResetValue_FloatHeader(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	h := http.Header{}
	h.Set("X-Ratelimit-Reset", "45.9") // float, not integer

	got := ParseCooldownUntil(h, nil, now, testORType)
	// int64(45.9) == 45 → seconds-from-now → now+45.
	if got != nowUnix+45 {
		t.Fatalf("float reset header: got %d, want %d", got, nowUnix+45)
	}
}

// TestInterpretResetValue_GarbageHeader_FallsThrough covers the branch where
// the header parses as neither int nor float (ParseFloat also fails) so
// interpretResetValue returns ok=false and ParseCooldownUntil falls through to
// the 60s fallback. A malformed header must never lock a key.
func TestInterpretResetValue_GarbageHeader_FallsThrough(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	h := http.Header{}
	h.Set("X-Ratelimit-Reset", "not-a-number")

	got := ParseCooldownUntil(h, nil, now, testORType)
	if got != nowUnix+60 {
		t.Fatalf("garbage header must fall through to 60s fallback: got %d, want %d", got, nowUnix+60)
	}
}

// TestInterpretResetValue_ZeroReset_FallsThrough covers the `n <= 0` guard:
// a reset value of "0" is not a usable future deadline, so the parser must
// reject it and the fallback (60s) applies. This prevents a bogus "0" from
// being misinterpreted as an epoch or an instant re-enable.
func TestInterpretResetValue_ZeroReset_FallsThrough(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	h := http.Header{}
	h.Set("X-Ratelimit-Reset", "0")

	got := ParseCooldownUntil(h, nil, now, testORType)
	if got != nowUnix+60 {
		t.Fatalf("zero reset must fall through to fallback: got %d, want %d", got, nowUnix+60)
	}
}

// TestExtractFromBodyMetadata_InvalidJSON_FallsThrough covers the
// json.Unmarshal error branch in extractFromBodyMetadata. A non-empty body that
// is not valid JSON must not panic; ParseCooldownUntil skips the metadata path,
// runs the keyword scan (no match here), and returns the 60s fallback.
func TestExtractFromBodyMetadata_InvalidJSON_FallsThrough(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	// No header (so we reach the body path), body is malformed JSON with no
	// daily keyword → fallback.
	body := []byte(`{this is not json`)
	got := ParseCooldownUntil(nil, body, now, testORType)
	if got != nowUnix+60 {
		t.Fatalf("invalid-JSON body must fall through to fallback: got %d, want %d", got, nowUnix+60)
	}
}

// TestExtractFromBodyMetadata_RetryAfterInMetadata covers the Retry-After
// branch inside the body's metadata.headers block (line 153-156). OpenRouter
// free-tier responses embed the upstream provider's headers here; a Retry-After
// of 90s must yield now+90 without needing a top-level header.
func TestExtractFromBodyMetadata_RetryAfterInMetadata(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	body := []byte(`{"error":{"message":"rate limited","metadata":{"headers":{"Retry-After":"90"}}}}`)
	got := ParseCooldownUntil(nil, body, now, testORType)
	if got != nowUnix+90 {
		t.Fatalf("Retry-After in body metadata: got %d, want %d", got, nowUnix+90)
	}
}

// TestExtractFromBodyMetadata_RetryAfterZero_FallsThrough covers the guard on
// the metadata Retry-After (secs > 0). A zero/negative Retry-After in metadata
// must be ignored so the parser falls through to the fallback rather than
// parking the key for an invalid duration.
func TestExtractFromBodyMetadata_RetryAfterZero_FallsThrough(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	body := []byte(`{"error":{"message":"rate limited","metadata":{"headers":{"Retry-After":"0"}}}}`)
	got := ParseCooldownUntil(nil, body, now, testORType)
	if got != nowUnix+60 {
		t.Fatalf("zero Retry-After in metadata must fall through to fallback: got %d, want %d", got, nowUnix+60)
	}
}

// TestExtractFromBodyMetadata_ResetHeaderCaseInsensitive covers the
// x-rate-limit-reset (hyphenated variant) case-insensitive match inside the
// metadata headers loop, distinct from the x-ratelimit-reset already covered.
func TestExtractFromBodyMetadata_ResetHeaderVariant(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	body := []byte(`{"error":{"metadata":{"headers":{"X-Rate-Limit-Reset":"300"}}}}`)
	got := ParseCooldownUntil(nil, body, now, testORType)
	// 300 < 86400 → seconds-from-now → now+300.
	if got != nowUnix+300 {
		t.Fatalf("hyphenated reset header variant: got %d, want %d", got, nowUnix+300)
	}
}
