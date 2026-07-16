package entverify

// Self-contained table tests: the token minter below mirrors internal/pkg/
// entsign's wire format (RS256 compact JWS, base64url, kid header) so this
// package — and its tests — can be copied wholesale into a consumer repo
// (newhub / creator / switch / lumen) with zero platform coupling.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func genKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genKey: %v", err)
	}
	return k
}

// signWithAlg mints a compact JWS exactly the way entsign.Sign does (RS256 over
// SHA-256(signingInput)), but with a caller-chosen alg header so the alg-
// confusion case can inject a lie.
func signWithAlg(t *testing.T, key *rsa.PrivateKey, kid, alg string, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT", "kid": kid})
	pb, _ := json.Marshal(claims)
	in := b64(hb) + "." + b64(pb)
	digest := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return in + "." + b64(sig)
}

func signRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	return signWithAlg(t, key, kid, "RS256", claims)
}

type kn struct {
	kid string
	pub *rsa.PublicKey
}

func jwksDoc(entries ...kn) []byte {
	type jwk struct{ Kty, Use, Alg, Kid, N, E string }
	ks := make([]jwk, 0, len(entries))
	for _, e := range entries {
		ks = append(ks, jwk{"RSA", "sig", "RS256", e.kid,
			b64(e.pub.N.Bytes()), b64(big.NewInt(int64(e.pub.E)).Bytes())})
	}
	body, _ := json.Marshal(map[string]any{"keys": ks})
	return body
}

func newJWKSServer(t *testing.T, doc []byte, fail *bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail != nil && *fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(doc)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerify_Table(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	prev := genKey(t)
	ghost := genKey(t) // signs a token whose kid is absent from the JWKS
	srv := newJWKSServer(t, jwksDoc(kn{"cur", &cur.PublicKey}, kn{"prev", &prev.PublicKey}), nil)

	claims := func(exp time.Time, aud string) map[string]any {
		return map[string]any{
			"iss": "https://identity.lurus.cn",
			"sub": "42",
			"aud": aud,
			"iat": base.Add(-time.Hour).Unix(),
			"exp": exp.Unix(),
			"ent": map[string]string{"plan_code": "pro", "seats": "5"},
		}
	}
	tamper := func(tok string) string {
		p := strings.Split(tok, ".")
		pb, _ := base64.RawURLEncoding.DecodeString(p[1])
		pb[4] ^= 0xFF // flip a payload byte → signature no longer matches
		p[1] = base64.RawURLEncoding.EncodeToString(pb)
		return strings.Join(p, ".")
	}

	cases := []struct {
		name      string
		mk        func() string
		aud       string
		wantErr   error
		wantFresh Freshness
	}{
		{"fresh", func() string { return signRS256(t, cur, "cur", claims(base.Add(time.Hour), "creator")) }, "creator", nil, Fresh},
		{"within skew", func() string { return signRS256(t, cur, "cur", claims(base.Add(-2*time.Minute), "creator")) }, "creator", nil, Fresh},
		{"grace zone", func() string { return signRS256(t, cur, "cur", claims(base.Add(-time.Hour), "creator")) }, "creator", nil, Grace},
		{"past grace", func() string { return signRS256(t, cur, "cur", claims(base.Add(-80*time.Hour), "creator")) }, "creator", ErrExpired, 0},
		{"wrong aud", func() string { return signRS256(t, cur, "cur", claims(base.Add(time.Hour), "creator")) }, "switch", ErrWrongAudience, 0},
		{"rotation window prev kid", func() string { return signRS256(t, prev, "prev", claims(base.Add(time.Hour), "creator")) }, "creator", nil, Fresh},
		{"unknown kid", func() string { return signRS256(t, ghost, "ghost", claims(base.Add(time.Hour), "creator")) }, "creator", ErrUnknownKID, 0},
		{"bad signature", func() string { return tamper(signRS256(t, cur, "cur", claims(base.Add(time.Hour), "creator"))) }, "creator", ErrBadSignature, 0},
		{"alg confusion", func() string { return signWithAlg(t, cur, "cur", "HS256", claims(base.Add(time.Hour), "creator")) }, "creator", ErrWrongAlg, 0},
		{"malformed", func() string { return "only.two" }, "creator", ErrMalformed, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS())
			got, err := v.Verify(context.Background(), tc.mk(), tc.aud)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Freshness != tc.wantFresh {
				t.Errorf("freshness = %v, want %v", got.Freshness, tc.wantFresh)
			}
			if got.Sub != "42" || got.Aud != "creator" {
				t.Errorf("identity claims drifted: sub=%q aud=%q", got.Sub, got.Aud)
			}
			if got.Ent["plan_code"] != "pro" || got.Ent["seats"] != "5" {
				t.Errorf("ent map drifted: %v", got.Ent)
			}
		})
	}
}

// TestVerify_OfflineGrace pins the 72h offline-JWKS behavior: once a key set is
// cached, a subsequent JWKS outage keeps verifying within the grace window and
// only hard-fails (ErrKeysUnavailable) once the cache is older than the grace.
func TestVerify_OfflineGrace(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	clk := base
	cur := genKey(t)
	fail := false
	srv := newJWKSServer(t, jwksDoc(kn{"cur", &cur.PublicKey}), &fail)

	// exp far in the future so the TOKEN never gates this test — we are probing
	// the KEY cache's offline behavior in isolation.
	tok := signRS256(t, cur, "cur", map[string]any{
		"sub": "42", "aud": "creator", "iat": base.Unix(),
		"exp": base.Add(1000 * time.Hour).Unix(), "ent": map[string]string{"plan_code": "pro"},
	})
	v := New(srv.URL, WithClock(func() time.Time { return clk }), WithCacheTTL(time.Minute), WithInsecureJWKS())

	// 1) First verify fetches the JWKS successfully.
	if _, err := v.Verify(context.Background(), tok, "creator"); err != nil {
		t.Fatalf("initial verify: %v", err)
	}

	// 2) JWKS goes down; clock advances past CacheTTL but within grace → the
	//    cached key still verifies.
	fail = true
	clk = base.Add(30 * time.Minute)
	if _, err := v.Verify(context.Background(), tok, "creator"); err != nil {
		t.Fatalf("within-grace offline verify should use cache, got: %v", err)
	}

	// 3) Past the 72h grace since the last good fetch → hard fail.
	clk = base.Add(73 * time.Hour)
	if _, err := v.Verify(context.Background(), tok, "creator"); !errors.Is(err, ErrKeysUnavailable) {
		t.Fatalf("past-grace offline verify: err = %v, want ErrKeysUnavailable", err)
	}
}

// TestVerify_SeedJWKSColdStart proves an offline-first client can verify at cold
// start from a persisted key set, before any successful network fetch (the
// JWKS endpoint is unreachable for the whole test).
func TestVerify_SeedJWKSColdStart(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	seed := jwksDoc(kn{"cur", &cur.PublicKey})
	tok := signRS256(t, cur, "cur", map[string]any{
		"sub": "42", "aud": "lumen", "iat": base.Unix(),
		"exp": base.Add(time.Hour).Unix(), "ent": map[string]string{"plan_code": "pro"},
	})

	// Point at a dead URL; only the seed can satisfy verification.
	v := New("http://127.0.0.1:1/unreachable",
		WithClock(func() time.Time { return base }),
		WithSeedJWKS(seed), WithInsecureJWKS())
	got, err := v.Verify(context.Background(), tok, "lumen")
	if err != nil {
		t.Fatalf("cold-start seeded verify: %v", err)
	}
	if got.Freshness != Fresh || got.Aud != "lumen" {
		t.Errorf("seeded verify drifted: fresh=%v aud=%q", got.Freshness, got.Aud)
	}
}

// TestVerify_FailClosedGuards pins the Wave-1 hardening: an empty expected
// audience, a non-https JWKS URL, and a corrupt seed all fail CLOSED rather
// than silently accepting or degrading; WithAnyAudience is the explicit opt-out.
func TestVerify_FailClosedGuards(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	srv := newJWKSServer(t, jwksDoc(kn{"cur", &cur.PublicKey}), nil)
	tok := signRS256(t, cur, "cur", map[string]any{
		"sub": "42", "aud": "switch", "iat": base.Unix(),
		"exp": base.Add(time.Hour).Unix(), "ent": map[string]string{"plan_code": "pro"},
	})

	// 1) Empty expected audience is rejected — a caller that lost its product id
	//    must not silently accept any product's token.
	v := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS())
	if _, err := v.Verify(context.Background(), tok, ""); !errors.Is(err, ErrEmptyAudience) {
		t.Errorf("empty aud: err = %v, want ErrEmptyAudience", err)
	}

	// 2) WithAnyAudience is the explicit opt-out: the same empty call now passes.
	vAny := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS(), WithAnyAudience())
	if _, err := vAny.Verify(context.Background(), tok, ""); err != nil {
		t.Errorf("WithAnyAudience empty aud: unexpected err %v", err)
	}

	// 3) A non-https JWKS URL without WithInsecureJWKS is a sticky construction
	//    error, surfaced from Err() and from Verify.
	vHTTP := New(srv.URL, WithClock(func() time.Time { return base }))
	if !errors.Is(vHTTP.Err(), ErrInsecureJWKSURL) {
		t.Errorf("http URL: Err() = %v, want ErrInsecureJWKSURL", vHTTP.Err())
	}
	if _, err := vHTTP.Verify(context.Background(), tok, "switch"); !errors.Is(err, ErrInsecureJWKSURL) {
		t.Errorf("http URL Verify: err = %v, want ErrInsecureJWKSURL", err)
	}

	// 4) A corrupt seed is surfaced (not swallowed) so an offline-first client
	//    fails fast; the https URL keeps the scheme check from masking it.
	vSeed := New("https://identity.lurus.cn/jwks",
		WithClock(func() time.Time { return base }),
		WithSeedJWKS([]byte("{not valid jwks")))
	if !errors.Is(vSeed.Err(), ErrBadSeed) {
		t.Errorf("corrupt seed: Err() = %v, want ErrBadSeed", vSeed.Err())
	}
}

// TestVerify_UnknownKidNoAmplification pins the Wave-2 DoS fix: a stream of
// tokens carrying unknown kids must NOT trigger a JWKS fetch per request while
// the cache is fresh. Pre-fix, keyFor refreshed on every unknown kid (under the
// global mutex), letting one cheap attacker request amplify into one platform
// fetch and serialize every concurrent verify.
func TestVerify_UnknownKidNoAmplification(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	ghost := genKey(t) // signs tokens whose kid is absent from the JWKS
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksDoc(kn{"cur", &cur.PublicKey}))
	}))
	t.Cleanup(srv.Close)

	claims := func(aud string) map[string]any {
		return map[string]any{
			"sub": "1", "aud": aud, "iat": base.Unix(),
			"exp": base.Add(time.Hour).Unix(), "ent": map[string]string{},
		}
	}
	v := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS())

	// Prime the cache with one good verify → exactly one fetch.
	if _, err := v.Verify(context.Background(), signRS256(t, cur, "cur", claims("creator")), "creator"); err != nil {
		t.Fatalf("prime verify: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("priming fetches = %d, want 1", got)
	}

	// A storm of unknown-kid tokens must be rejected with ErrUnknownKID and ZERO
	// extra fetches (fresh cache → no refresh).
	for i := 0; i < 50; i++ {
		_, err := v.Verify(context.Background(), signRS256(t, ghost, "ghost", claims("creator")), "creator")
		if !errors.Is(err, ErrUnknownKID) {
			t.Fatalf("unknown kid #%d: err = %v, want ErrUnknownKID", i, err)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("unknown-kid storm caused %d fetches, want 1 (no amplification)", got)
	}
}

// TestVerify_ConcurrentNoDeadlock exercises the singleflight refresh + lock-free
// I/O path under concurrency (meaningful under -race): many goroutines verify
// against a cold verifier at once; they must coalesce into a bounded number of
// fetches, never deadlock, and all succeed.
func TestVerify_ConcurrentNoDeadlock(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(30 * time.Millisecond) // hold the first fetch so the stampede parks on one inflight
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksDoc(kn{"cur", &cur.PublicKey}))
	}))
	t.Cleanup(srv.Close)

	v := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS())
	tok := signRS256(t, cur, "cur", map[string]any{
		"sub": "1", "aud": "creator", "iat": base.Unix(),
		"exp": base.Add(time.Hour).Unix(), "ent": map[string]string{},
	})

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Verify(context.Background(), tok, "creator"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent verify: %v", err)
	}
	// Singleflight must collapse the cold-start stampede onto one in-flight
	// fetch, not one per goroutine.
	if got := atomic.LoadInt32(&hits); got > 2 {
		t.Errorf("concurrent cold start caused %d fetches, want singleflight-coalesced (<=2)", got)
	}
}

// TestVerify_NotYetValid pins the Wave-3 nbf/iat sanity check: a token whose
// validity starts in the future (signer clock fault / tampering) is rejected
// beyond the skew tolerance and accepted within it.
func TestVerify_NotYetValid(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	srv := newJWKSServer(t, jwksDoc(kn{"cur", &cur.PublicKey}), nil)
	v := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS())

	// nbf 1h in the future (beyond the ±5min skew) → reject.
	future := signRS256(t, cur, "cur", map[string]any{
		"sub": "1", "aud": "creator", "iat": base.Add(time.Hour).Unix(),
		"nbf": base.Add(time.Hour).Unix(), "exp": base.Add(25 * time.Hour).Unix(),
		"ent": map[string]string{},
	})
	if _, err := v.Verify(context.Background(), future, "creator"); !errors.Is(err, ErrNotYetValid) {
		t.Errorf("future nbf: err = %v, want ErrNotYetValid", err)
	}

	// nbf 2min ahead (within skew) → accepted.
	nearFuture := signRS256(t, cur, "cur", map[string]any{
		"sub": "1", "aud": "creator", "iat": base.Add(2 * time.Minute).Unix(),
		"nbf": base.Add(2 * time.Minute).Unix(), "exp": base.Add(25 * time.Hour).Unix(),
		"ent": map[string]string{},
	})
	if _, err := v.Verify(context.Background(), nearFuture, "creator"); err != nil {
		t.Errorf("nbf within skew: unexpected err %v", err)
	}
}

// TestVerify_PricingPrecision pins the Wave-3 fix: the pricing claim survives
// verification byte-for-byte, not rounded through float64. 2^53+1 is the
// canonical integer a float64 round-trip mangles (to 2^53).
func TestVerify_PricingPrecision(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	srv := newJWKSServer(t, jwksDoc(kn{"cur", &cur.PublicKey}), nil)
	const bigInt = "9007199254740993" // 2^53+1 — not representable as float64
	tok := signRS256(t, cur, "cur", map[string]any{
		"sub": "1", "aud": "creator", "iat": base.Unix(),
		"exp": base.Add(time.Hour).Unix(), "ent": map[string]string{},
		"pricing": json.RawMessage(`{"credits":` + bigInt + `}`),
	})
	v := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS())
	claims, err := v.Verify(context.Background(), tok, "creator")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(string(claims.Pricing), bigInt) {
		t.Errorf("pricing lost precision: got %s, want it to contain %s", claims.Pricing, bigInt)
	}
}

// TestVerify_ExportJWKSRoundTrip pins the Wave-4 offline-first restart path: the
// fetched key set can be exported, persisted, and re-seeded into a fresh
// verifier that then verifies with NO live endpoint.
func TestVerify_ExportJWKSRoundTrip(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	cur := genKey(t)
	prev := genKey(t)
	srv := newJWKSServer(t, jwksDoc(kn{"cur", &cur.PublicKey}, kn{"prev", &prev.PublicKey}), nil)
	mk := func(key *rsa.PrivateKey, kid string) string {
		return signRS256(t, key, kid, map[string]any{
			"sub": "1", "aud": "creator", "iat": base.Unix(),
			"exp": base.Add(time.Hour).Unix(), "ent": map[string]string{},
		})
	}

	// Prime a verifier so it fetches and caches both kids.
	v := New(srv.URL, WithClock(func() time.Time { return base }), WithInsecureJWKS())
	if _, err := v.Verify(context.Background(), mk(cur, "cur"), "creator"); err != nil {
		t.Fatalf("prime verify: %v", err)
	}
	body, at, ok := v.ExportJWKS()
	if !ok || at != base || len(body) == 0 {
		t.Fatalf("export = (%d bytes, %v, %v), want a non-empty set stamped at base", len(body), at, ok)
	}

	// A fresh verifier seeded from the export, pointed at a DEAD URL, must verify
	// both kids entirely offline.
	v2 := New("https://dead.invalid/jwks",
		WithClock(func() time.Time { return base }),
		WithSeedJWKS(body))
	if v2.Err() != nil {
		t.Fatalf("seed from export errored: %v", v2.Err())
	}
	for _, kc := range []struct {
		key *rsa.PrivateKey
		kid string
	}{{cur, "cur"}, {prev, "prev"}} {
		if _, err := v2.Verify(context.Background(), mk(kc.key, kc.kid), "creator"); err != nil {
			t.Errorf("offline verify from exported seed (kid=%s): %v", kc.kid, err)
		}
	}
}

// panicOnceRT panics on its first RoundTrip, then serves doc — models a transport
// that faults mid-fetch.
type panicOnceRT struct {
	doc      []byte
	panicked bool
}

func (r *panicOnceRT) RoundTrip(*http.Request) (*http.Response, error) {
	if !r.panicked {
		r.panicked = true
		panic("entverify test: transport boom")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(r.doc)),
		Header:     make(http.Header),
	}, nil
}

// TestVerify_RefreshPanicDoesNotWedge pins the acceptance-audit fix: if the JWKS
// fetch panics, the singleflight invariant is still restored (inflight cleared,
// done closed by defer), so a later refresh proceeds instead of every future
// verify hanging forever on a leaked in-flight channel.
func TestVerify_RefreshPanicDoesNotWedge(t *testing.T) {
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	clk := base
	cur := genKey(t)
	rt := &panicOnceRT{doc: jwksDoc(kn{"cur", &cur.PublicKey})}
	v := New("https://identity.lurus.cn/jwks",
		WithClock(func() time.Time { return clk }),
		WithHTTPClient(&http.Client{Transport: rt}))
	tok := signRS256(t, cur, "cur", map[string]any{
		"sub": "1", "aud": "creator", "iat": base.Unix(),
		"exp": base.Add(time.Hour).Unix(), "ent": map[string]string{},
	})

	// First verify triggers a refresh whose transport panics; recover it.
	func() {
		defer func() { _ = recover() }()
		_, _ = v.Verify(context.Background(), tok, "creator")
	}()

	// Advance past CacheTTL so the rate-limit allows a retry, then a second
	// verify must COMPLETE (transport now healthy) rather than wedge on a leaked
	// inflight. A timeout means the invariant leaked.
	clk = base.Add(2 * time.Hour)
	done := make(chan error, 1)
	go func() {
		_, err := v.Verify(context.Background(), tok, "creator")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("post-panic verify: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("post-panic verify WEDGED — singleflight inflight/done leaked on panic")
	}
}
