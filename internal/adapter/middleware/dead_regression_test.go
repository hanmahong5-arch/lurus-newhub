package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewJWKSManagerWithContext_StartsBackgroundRefresh locks the wiring that
// makes a separate context-less auto-refresh entry point unnecessary: the
// constructor itself must start the periodic refresher, not merely perform the
// one-off initial fetch.
//
// Why this matters. If the background refresher is not started, a JWKSManager
// only ever re-fetches lazily, from getKeyWithRefresh, and only when it sees a
// kid it does not already hold. That recovers from a key ROTATION (new kid),
// but not from a key REVOCATION, and not from an identity provider that
// re-keys under an unchanged kid — in those cases the process keeps verifying
// signatures against a stale public key until it is restarted.
//
// The pre-existing constructor test (TestNewJWKSManagerWithContext_FetchAndStop)
// asserts only the INITIAL fetch, so removing the background-goroutine line
// from the constructor leaves it green. This test is the one that turns red.
func TestNewJWKSManagerWithContext_StartsBackgroundRefresh(t *testing.T) {
	_, pub := generateTestRSAKeyPair(t)
	jwk := rsaPublicKeyToJWK(pub, "bg-refresh-kid")

	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","use":"sig","kid":"` + jwk.Kid +
			`","alg":"RS256","n":"` + jwk.N + `","e":"` + jwk.E + `"}]}`))
	}))
	defer srv.Close()

	// The refresher reads this package global exactly once, when it builds its
	// ticker on entry. It is restored below only after the goroutine has been
	// observed ticking (ticker already built) and then quiesced, so no later
	// test races the restore.
	prevInterval := jwksRefreshInterval
	jwksRefreshInterval = 15 * time.Millisecond
	restored := false
	restore := func() {
		if !restored {
			jwksRefreshInterval = prevInterval
			restored = true
		}
	}
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewJWKSManagerWithContext(ctx, srv.URL)
	if mgr == nil {
		t.Fatalf("NewJWKSManagerWithContext returned nil")
	}
	// Sanity: the constructor's own initial fetch landed the key.
	if key, err := mgr.getKey("bg-refresh-kid"); err != nil || key == nil {
		t.Fatalf("initial fetch did not populate the key: err=%v key=%v", err, key)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("initial fetch count = %d, want exactly 1", got)
	}

	// The assertion: fetches must keep arriving with NO further getKey call,
	// i.e. driven by the constructor-started background refresher alone.
	deadline := time.After(3 * time.Second)
	for atomic.LoadInt64(&hits) < 3 {
		select {
		case <-deadline:
			t.Fatalf("JWKS was fetched only %d time(s) with no lazy trigger; "+
				"NewJWKSManagerWithContext must start the periodic refresher, "+
				"otherwise a revoked or same-kid-rotated signing key stays "+
				"trusted until process restart", atomic.LoadInt64(&hits))
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Cancelling must stop the refresher: fetch count has to go stable.
	cancel()
	stableFor := 0
	last := atomic.LoadInt64(&hits)
	for i := 0; i < 100 && stableFor < 3; i++ {
		time.Sleep(20 * time.Millisecond)
		cur := atomic.LoadInt64(&hits)
		if cur == last {
			stableFor++
		} else {
			stableFor = 0
			last = cur
		}
	}
	if stableFor < 3 {
		t.Errorf("JWKS refresher kept fetching after context cancel (hits still climbing at %d)", last)
	}
	restore()
}
