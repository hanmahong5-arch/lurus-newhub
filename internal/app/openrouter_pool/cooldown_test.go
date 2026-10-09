package openrouter_pool

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseCooldownUntil(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	nowUnix := now.Unix()

	tests := []struct {
		name      string
		header    http.Header
		body      string
		wantMin   int64 // inclusive lower bound (covers ±jitter from clamp/heuristic)
		wantMax   int64 // inclusive upper bound
		wantExact int64 // 0 means use min/max range
	}{
		{
			name:      "header X-Ratelimit-Reset absolute Unix seconds",
			header:    headerOf("X-Ratelimit-Reset", strconv.FormatInt(nowUnix+300, 10)),
			wantExact: nowUnix + 300,
		},
		{
			name:      "header X-Ratelimit-Reset Unix milliseconds",
			header:    headerOf("X-Ratelimit-Reset", strconv.FormatInt((nowUnix+600)*1000, 10)),
			wantExact: nowUnix + 600,
		},
		{
			name:      "header Retry-After seconds-from-now",
			header:    headerOf("Retry-After", "120"),
			wantExact: nowUnix + 120,
		},
		{
			name:      "body metadata embeds X-RateLimit-Reset (free-tier shape)",
			body:      `{"error":{"message":"Rate limit","metadata":{"headers":{"X-RateLimit-Reset":"` + strconv.FormatInt(nowUnix+900, 10) + `"}}}}`,
			wantExact: nowUnix + 900,
		},
		{
			name:      "daily keyword in message → 24h fallback",
			body:      `{"error":{"message":"Rate limit exceeded: free-models-per-day"}}`,
			wantExact: nowUnix + int64(24*time.Hour/time.Second),
		},
		{
			name:    "no signal → fallback 60s (clamped to floor of 30s, but 60s passes)",
			wantMin: nowUnix + 60,
			wantMax: nowUnix + 60,
		},
		{
			name:      "header with seconds-from-now hint (small int)",
			header:    headerOf("X-Ratelimit-Reset", "45"),
			wantExact: nowUnix + 45,
		},
		{
			name:    "past timestamp ignored, falls through to fallback",
			header:  headerOf("X-Ratelimit-Reset", strconv.FormatInt(nowUnix-100, 10)),
			wantMin: nowUnix + 60, // fallback path
			wantMax: nowUnix + 60,
		},
		{
			name:    "absurdly large value clamped to 24h",
			header:  headerOf("X-Ratelimit-Reset", strconv.FormatInt(nowUnix+int64(72*time.Hour/time.Second), 10)),
			wantMin: nowUnix + int64(24*time.Hour/time.Second),
			wantMax: nowUnix + int64(24*time.Hour/time.Second),
		},
		{
			name:      "value below 30s floor clamped up",
			header:    headerOf("Retry-After", "5"),
			wantExact: nowUnix + 30, // cooldownMin
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseCooldownUntil(tc.header, []byte(tc.body), now, testORType)
			if tc.wantExact != 0 {
				if got != tc.wantExact {
					t.Fatalf("got %d, want %d (delta=%d)", got, tc.wantExact, got-tc.wantExact)
				}
			} else {
				if got < tc.wantMin || got > tc.wantMax {
					t.Fatalf("got %d, want range [%d, %d]", got, tc.wantMin, tc.wantMax)
				}
			}
			if got <= nowUnix {
				t.Errorf("cooldown must be in the future, got %d (now=%d)", got, nowUnix)
			}
			if got > nowUnix+int64(24*time.Hour/time.Second) {
				t.Errorf("cooldown must not exceed 24h, got %d", got-nowUnix)
			}
		})
	}
}

func TestParseCooldownNeverZero(t *testing.T) {
	// Zero would let the reaper recover instantly, defeating the purpose.
	now := time.Now()
	got := ParseCooldownUntil(nil, nil, now, testORType)
	if got <= now.Unix() {
		t.Fatalf("ParseCooldownUntil must always return a future timestamp, got %d (now=%d)", got, now.Unix())
	}
}

func headerOf(k, v string) http.Header {
	h := make(http.Header)
	h.Set(k, v)
	return h
}
