package entverify

// Covers the low-level helpers that TestVerify_Table et al. never exercise
// directly: WithSkew / WithGrace (verifier construction options, previously
// 0% since every existing test relies on the package defaults), the
// Freshness.String() formatter, the asUnix claim decoder's non-numeric
// branches, decodeSegment's malformed-input paths and parseJWKS's
// weak-key / malformed-entry filtering.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func r5coreGenKey(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("genKey(%d): %v", bits, err)
	}
	return k
}

func r5coreB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// r5coreSignRS256 mints a compact JWS in the same wire format entsign/
// entverify use elsewhere in this package's tests (RS256 over
// SHA-256(header.payload)).
func r5coreSignRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	pb, _ := json.Marshal(claims)
	in := r5coreB64(hb) + "." + r5coreB64(pb)
	digest := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return in + "." + r5coreB64(sig)
}

func TestFreshness_String(t *testing.T) {
	if got := Fresh.String(); got != "fresh" {
		t.Errorf("Fresh.String() = %q, want %q", got, "fresh")
	}
	if got := Grace.String(); got != "grace" {
		t.Errorf("Grace.String() = %q, want %q", got, "grace")
	}
	// Any other value (e.g. a hypothetical future grade) falls back to "fresh"
	// per the implementation's default branch.
	if got := Freshness(99).String(); got != "fresh" {
		t.Errorf("Freshness(99).String() = %q, want the default %q", got, "fresh")
	}
}

func TestAsUnix_NonNumericAndUnsupportedTypesYieldZero(t *testing.T) {
	cases := []struct {
		name string
		in   any
	}{
		{"nil", nil},
		{"bool", true},
		{"non-numeric string", "not-a-timestamp"},
		{"empty string", ""},
		{"map", map[string]any{"x": 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := asUnix(c.in)
			if !got.IsZero() {
				t.Errorf("asUnix(%#v) = %v, want zero time", c.in, got)
			}
		})
	}
}

func TestAsUnix_NumericVariants(t *testing.T) {
	want := time.Unix(1700000000, 0)

	if got := asUnix(float64(1700000000)); !got.Equal(want) {
		t.Errorf("asUnix(float64) = %v, want %v", got, want)
	}
	if got := asUnix(json.Number("1700000000")); !got.Equal(want) {
		t.Errorf("asUnix(json.Number) = %v, want %v", got, want)
	}
	if got := asUnix("1700000000"); !got.Equal(want) {
		t.Errorf("asUnix(numeric string) = %v, want %v", got, want)
	}
}

func TestDecodeSegment_MalformedInputs(t *testing.T) {
	var dst struct{ A string }

	// Not valid base64url at all (contains a '+' which RawURLEncoding rejects).
	if err := decodeSegment("not base64!!", &dst); err == nil {
		t.Fatal("expected an error for a non-base64url segment")
	}

	// Valid base64url but the decoded bytes are not valid JSON.
	garbage := r5coreB64([]byte("{not json"))
	if err := decodeSegment(garbage, &dst); err == nil {
		t.Fatal("expected an error for base64-valid but JSON-invalid payload")
	}

	// Happy path sanity check so the two failure cases above are meaningful.
	good := r5coreB64([]byte(`{"A":"hello"}`))
	if err := decodeSegment(good, &dst); err != nil {
		t.Fatalf("decodeSegment(valid) unexpected error: %v", err)
	}
	if dst.A != "hello" {
		t.Fatalf("decodeSegment(valid) = %+v, want A=hello", dst)
	}
}

func TestParseJWKS_DropsWeakAndMalformedEntries(t *testing.T) {
	strong := r5coreGenKey(t, 2048)
	weak := r5coreGenKey(t, 1024) // below minRSABits(2048) — must be dropped

	doc, _ := json.Marshal(map[string]any{
		"keys": []map[string]string{
			{"kty": "RSA", "kid": "strong", "n": r5coreB64(strong.N.Bytes()), "e": r5coreB64(big.NewInt(int64(strong.PublicKey.E)).Bytes())},
			{"kty": "RSA", "kid": "weak", "n": r5coreB64(weak.N.Bytes()), "e": r5coreB64(big.NewInt(int64(weak.PublicKey.E)).Bytes())},
			{"kty": "EC", "kid": "wrong-kty", "n": r5coreB64(strong.N.Bytes()), "e": "AQAB"},
			{"kty": "RSA", "kid": "bad-n", "n": "not-base64url!!", "e": "AQAB"},
			{"kty": "RSA", "kid": "missing-e", "n": r5coreB64(strong.N.Bytes()), "e": ""},
		},
	})

	keys, err := parseJWKS(doc)
	if err != nil {
		t.Fatalf("parseJWKS: unexpected error: %v", err)
	}
	if _, ok := keys["strong"]; !ok {
		t.Errorf("expected the 2048-bit key to survive filtering, got keys=%v", keySet(keys))
	}
	if len(keys) != 1 {
		t.Errorf("expected exactly 1 surviving key (weak/malformed dropped), got %v", keySet(keys))
	}
}

func keySet(m map[string]*rsa.PublicKey) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestParseJWKS_AllWeakYieldsNoUsableKeysError(t *testing.T) {
	weak := r5coreGenKey(t, 1024)
	doc, _ := json.Marshal(map[string]any{
		"keys": []map[string]string{
			{"kty": "RSA", "kid": "weak", "n": r5coreB64(weak.N.Bytes()), "e": r5coreB64(big.NewInt(int64(weak.PublicKey.E)).Bytes())},
		},
	})
	_, err := parseJWKS(doc)
	if err == nil {
		t.Fatal("expected an error when every key in the set is below the minimum bit length")
	}
}

func TestParseJWKS_MalformedTopLevelJSON(t *testing.T) {
	_, err := parseJWKS([]byte("not json at all"))
	if err == nil {
		t.Fatal("expected an error for malformed top-level JWKS JSON")
	}
}

// TestWithSkew_NarrowsClockToleranceForNotYetValid seeds a Verifier with a
// tiny skew and a token whose nbf is a few seconds in the future: under the
// default 5-minute skew this would verify, but with a near-zero skew it must
// be rejected as not-yet-valid — proving WithSkew's value actually reaches
// Verify's freshness/tolerance check instead of being ignored.
func TestWithSkew_NarrowsClockToleranceForNotYetValid(t *testing.T) {
	key := r5coreGenKey(t, 2048)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	nbf := now.Add(3 * time.Second) // just barely in the future

	claims := map[string]any{
		"iss": "https://identity.lurus.cn",
		"sub": "acct_1",
		"aud": "product-x",
		"nbf": nbf.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	token := r5coreSignRS256(t, key, "k1", claims)

	doc, _ := json.Marshal(map[string]any{"keys": []map[string]string{
		{"kty": "RSA", "kid": "k1", "n": r5coreB64(key.N.Bytes()), "e": r5coreB64(big.NewInt(int64(key.PublicKey.E)).Bytes())},
	}})

	v := New("https://jwks.invalid/", WithSeedJWKS(doc), WithClock(func() time.Time { return now }), WithSkew(1*time.Second))
	if _, err := v.Verify(context.Background(), token, "product-x"); !errors.Is(err, ErrNotYetValid) {
		t.Fatalf("with a 1s skew and nbf 3s in the future, expected ErrNotYetValid, got %v", err)
	}

	// The default skew (5m) would have tolerated the same 3s gap — confirms
	// the narrow skew above is what caused the rejection, not something else.
	v2 := New("https://jwks.invalid/", WithSeedJWKS(doc), WithClock(func() time.Time { return now }))
	if _, err := v2.Verify(context.Background(), token, "product-x"); err != nil {
		t.Fatalf("with the default 5m skew, expected the token to verify, got %v", err)
	}
}

// TestWithGrace_ShortensOfflineGraceWindow proves WithGrace's value actually
// governs the Fresh/Grace/Expired boundary in Verify.
func TestWithGrace_ShortensOfflineGraceWindow(t *testing.T) {
	key := r5coreGenKey(t, 2048)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	exp := now.Add(-1 * time.Hour) // already expired by an hour

	claims := map[string]any{
		"iss": "https://identity.lurus.cn",
		"sub": "acct_1",
		"aud": "product-x",
		"iat": now.Add(-2 * time.Hour).Unix(),
		"exp": exp.Unix(),
	}
	token := r5coreSignRS256(t, key, "k1", claims)

	doc, _ := json.Marshal(map[string]any{"keys": []map[string]string{
		{"kty": "RSA", "kid": "k1", "n": r5coreB64(key.N.Bytes()), "e": r5coreB64(big.NewInt(int64(key.PublicKey.E)).Bytes())},
	}})

	// Grace shorter than the 1h-past-exp gap: must hard-fail as expired.
	vShort := New("https://jwks.invalid/", WithSeedJWKS(doc), WithClock(func() time.Time { return now }), WithGrace(10*time.Minute))
	if _, err := vShort.Verify(context.Background(), token, "product-x"); !errors.Is(err, ErrExpired) {
		t.Fatalf("with a 10m grace and exp 1h in the past, expected ErrExpired, got %v", err)
	}

	// Grace longer than the gap: must verify with Freshness==Grace.
	vLong := New("https://jwks.invalid/", WithSeedJWKS(doc), WithClock(func() time.Time { return now }), WithGrace(24*time.Hour))
	claimsOut, err := vLong.Verify(context.Background(), token, "product-x")
	if err != nil {
		t.Fatalf("with a 24h grace and exp 1h in the past, expected success, got %v", err)
	}
	if claimsOut.Freshness != Grace {
		t.Errorf("Freshness = %v, want Grace", claimsOut.Freshness)
	}
}

// TestFetchJWKS_HTTPErrorStatus covers fetchJWKS's non-200 branch (the
// existing table tests only exercise a hard connection failure, not a
// well-formed error response).
func TestFetchJWKS_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	v := New(srv.URL, WithInsecureJWKS())
	_, err := v.fetchJWKS(context.Background())
	if err == nil {
		t.Fatal("expected an error for a non-200 JWKS response")
	}
}
