// Vendored from lurus-platform pkg/entverify (the source of truth and its
// conformance suite live there) at platform master bc3d32a. Update by
// re-copying the pair of files; do not edit here.
//
// Package entverify is the reference OFFLINE verifier for platform entitlement
// tokens (Track B). It is the consumer-side twin of internal/pkg/entsign: a
// backend (newhub) or desktop/CLI client (creator, switch, lumen) verifies an
// RS256 entitlement JWS against the platform JWKS with ZERO IdP round-trip and
// zero shared secret — the token itself carries sub/aud/ent/exp, so a service
// that has cached the (public) JWKS can authorize entirely offline.
//
// Contract (api/openapi.yaml → /api/v1/entitlements/{product_id} + /jwks):
//   - RS256 compact JWS; header {alg:RS256, typ:JWT, kid}.
//   - claims iss / sub / aud / iat / exp(24h) / ent(map[string]string) /
//     pricing(optional per-model price table).
//   - JWKS cacheable max-age 3600; during a signing-key rotation the set
//     carries BOTH kids for a 96h overlap window.
//   - freshness: honor exp, allow a further 72h offline grace, ±5min clock
//     skew; there is deliberately no revocation list.
//
// Stdlib-only (mirrors entsign) so it drops into a desktop binary with no JOSE
// dependency. A *Verifier is safe for concurrent use.
package entverify

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults are the Track B contract numbers; override via options for tests or
// customer installs.
const (
	DefaultSkew     = 5 * time.Minute
	DefaultGrace    = 72 * time.Hour
	DefaultCacheTTL = 3600 * time.Second
)

// minRSABits is the modulus floor for a JWKS key: a smaller key is factorable,
// so a key that weak in a fetched/seeded set is dropped (mirrors the signer's
// entsign.MinSigningKeyBits, without importing it — this package is standalone).
const minRSABits = 2048

// Freshness grades a still-verifiable token so a client can DEGRADE rather than
// hard-fail while offline — e.g. switch's license heartbeat drops to a reduced
// tier in Grace instead of locking the app.
type Freshness int

const (
	Fresh Freshness = iota // now <= exp + skew
	Grace                  // exp + skew < now <= exp + 72h offline grace
)

func (f Freshness) String() string {
	if f == Grace {
		return "grace"
	}
	return "fresh"
}

// Typed errors let a caller separate "reject outright" from "degrade". Every
// return path from Verify is one of these (wrapped) or nil.
var (
	ErrMalformed       = errors.New("entverify: malformed token")
	ErrWrongAlg        = errors.New("entverify: unexpected alg (want RS256)")
	ErrUnknownKID      = errors.New("entverify: no JWKS key matches token kid")
	ErrBadSignature    = errors.New("entverify: signature does not verify")
	ErrWrongAudience   = errors.New("entverify: aud does not match product")
	ErrEmptyAudience   = errors.New("entverify: expected audience is empty (use WithAnyAudience to opt out of audience binding)")
	ErrExpired         = errors.New("entverify: token past exp + offline grace")
	ErrNotYetValid     = errors.New("entverify: token not valid yet (nbf/iat in the future)")
	ErrKeysUnavailable = errors.New("entverify: JWKS unreachable and cache empty/stale")
	ErrInsecureJWKSURL = errors.New("entverify: JWKS URL must be https (use WithInsecureJWKS to allow http)")
	ErrBadSeed         = errors.New("entverify: seed JWKS could not be parsed")
)

// Claims is a decoded, signature-verified entitlement token.
type Claims struct {
	Iss       string
	Sub       string
	Aud       string
	IssuedAt  time.Time
	NotBefore time.Time
	ExpiresAt time.Time
	Ent       map[string]string
	Pricing   json.RawMessage
	Freshness Freshness
	Raw       map[string]any
}

// Verifier verifies tokens against a cached JWKS. It performs zero IdP calls:
// at most one GET to the JWKS URL, refreshed no more often than CacheTTL and
// reused across the offline grace window when the endpoint is unreachable.
type Verifier struct {
	jwksURL  string
	hc       *http.Client
	now      func() time.Time
	skew     time.Duration
	grace    time.Duration
	cacheTTL time.Duration

	seed []byte // optional persisted JWKS, applied at construction (offline-first)

	anyAud   bool  // caller opted out of audience binding (WithAnyAudience)
	insecure bool  // caller allows a non-https JWKS URL (WithInsecureJWKS)
	initErr  error // sticky construction error (non-https URL / corrupt seed)

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time     // last SUCCESSFUL fetch (or seed) — anchors freshness/grace
	lastAttempt time.Time     // last refresh ATTEMPT (success or failure) — rate-limits fetches
	inflight    chan struct{} // non-nil while a refresh runs; refreshers coalesce on it
}

// Option configures a Verifier.
type Option func(*Verifier)

// WithClock injects the time source (tests).
func WithClock(fn func() time.Time) Option { return func(v *Verifier) { v.now = fn } }

// WithSkew overrides the ±clock-skew tolerance (default 5m).
func WithSkew(d time.Duration) Option { return func(v *Verifier) { v.skew = d } }

// WithGrace overrides the offline freshness grace past exp (default 72h).
func WithGrace(d time.Duration) Option { return func(v *Verifier) { v.grace = d } }

// WithCacheTTL overrides how long a fetched JWKS is reused before a refresh is
// attempted (default 3600s, matching the endpoint's Cache-Control).
func WithCacheTTL(d time.Duration) Option { return func(v *Verifier) { v.cacheTTL = d } }

// WithHTTPClient supplies a client (proxy / mTLS / custom timeout).
func WithHTTPClient(hc *http.Client) Option { return func(v *Verifier) { v.hc = hc } }

// WithSeedJWKS preloads a persisted key set so an offline-first client (lumen,
// creator) can verify at cold start, before its first successful fetch. It is
// applied after all other options so the seed's timestamp uses the final clock.
func WithSeedJWKS(body []byte) Option {
	return func(v *Verifier) { v.seed = body }
}

// WithAnyAudience opts OUT of audience binding: Verify will not compare the
// token aud against an expected value, and an empty expected audience is no
// longer an error. Use only for a deliberately product-agnostic verifier.
func WithAnyAudience() Option { return func(v *Verifier) { v.anyAud = true } }

// WithInsecureJWKS allows a non-https JWKS URL (loopback tests, a plaintext
// internal address). Without it, New records a sticky error for a non-https
// URL so a network attacker cannot swap in its own signing keys.
func WithInsecureJWKS() Option { return func(v *Verifier) { v.insecure = true } }

// New builds a Verifier for the given JWKS URL.
func New(jwksURL string, opts ...Option) *Verifier {
	v := &Verifier{
		jwksURL:  jwksURL,
		hc:       &http.Client{Timeout: 5 * time.Second},
		now:      time.Now,
		skew:     DefaultSkew,
		grace:    DefaultGrace,
		cacheTTL: DefaultCacheTTL,
		keys:     map[string]*rsa.PublicKey{},
	}
	for _, o := range opts {
		o(v)
	}
	// Fail closed on a non-https JWKS URL unless explicitly allowed: the fetched
	// key set is the sole trust anchor, so a plaintext endpoint invites a MITM
	// serving forged keys. Recorded as a sticky error (New keeps its no-error
	// signature) surfaced from Err() and the first Verify.
	if !v.insecure && jwksURL != "" {
		if u, err := url.Parse(jwksURL); err != nil || u.Scheme != "https" {
			v.initErr = fmt.Errorf("%w: %q", ErrInsecureJWKSURL, jwksURL)
		}
	}
	// Seed after options so fetchedAt is stamped with the final clock; a seeded
	// set counts as a fetch at construction, giving the full offline grace. A
	// corrupt seed is surfaced (not silently swallowed) so an offline-first
	// client fails fast instead of degrading to a misleading later error.
	if len(v.seed) > 0 {
		if keys, err := parseJWKS(v.seed); err != nil {
			if v.initErr == nil {
				v.initErr = fmt.Errorf("%w: %w", ErrBadSeed, err)
			}
		} else {
			v.keys = keys
			v.fetchedAt = v.now()
		}
	}
	return v
}

// Err returns any sticky construction error: a non-https JWKS URL supplied
// without WithInsecureJWKS, or a corrupt WithSeedJWKS. An offline-first client
// should check it at startup; Verify also returns it. nil means usable.
func (v *Verifier) Err() error { return v.initErr }

// ExportJWKS returns the most-recently fetched (or seeded) key set as an RFC
// 7517 document plus the time it was obtained, so an offline-first client
// (creator / lumen) can persist it after each successful refresh and re-seed
// with WithSeedJWKS on the next start — surviving restarts with no live
// endpoint. ok is false before any key set has been obtained.
func (v *Verifier) ExportJWKS() (body []byte, fetchedAt time.Time, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.keys) == 0 {
		return nil, time.Time{}, false
	}
	b, err := marshalJWKS(v.keys)
	if err != nil {
		return nil, time.Time{}, false
	}
	return b, v.fetchedAt, true
}

// Verify checks the RS256 signature, audience and freshness of token and
// returns the decoded claims. A token inside its 72h offline grace verifies
// with Freshness==Grace and a nil error (the caller decides whether to degrade);
// only a hard failure — bad signature, wrong aud, past grace, unknown kid,
// unreachable keys — returns a (wrapped, typed) error.
func (v *Verifier) Verify(ctx context.Context, token, expectedAud string) (*Claims, error) {
	if v.initErr != nil {
		return nil, v.initErr
	}
	if expectedAud == "" && !v.anyAud {
		return nil, ErrEmptyAudience
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}
	var hdr struct{ Alg, Kid, Typ string }
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return nil, ErrMalformed
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("%w: %q", ErrWrongAlg, hdr.Alg)
	}

	key, err := v.keyFor(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, ErrBadSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, ErrMalformed
	}
	claims := &Claims{
		Iss:       asString(raw["iss"]),
		Sub:       asString(raw["sub"]),
		Aud:       asString(raw["aud"]),
		IssuedAt:  asUnix(raw["iat"]),
		NotBefore: asUnix(raw["nbf"]),
		ExpiresAt: asUnix(raw["exp"]),
		Ent:       asStringMap(raw["ent"]),
		Raw:       raw,
	}
	// pricing keeps its ORIGINAL signed bytes rather than a float64 round-trip
	// through the generic map — money/precision values must survive verbatim.
	var pr struct {
		Pricing json.RawMessage `json:"pricing"`
	}
	if json.Unmarshal(payload, &pr) == nil {
		claims.Pricing = pr.Pricing
	}
	if !v.anyAud && claims.Aud != expectedAud {
		return nil, fmt.Errorf("%w: token aud=%q want %q", ErrWrongAudience, claims.Aud, expectedAud)
	}

	now := v.now()
	// Reject a token whose validity starts in the future — a signer clock fault
	// or tampering. Prefer nbf; fall back to iat.
	notBefore := claims.NotBefore
	if notBefore.IsZero() {
		notBefore = claims.IssuedAt
	}
	if !notBefore.IsZero() && now.Before(notBefore.Add(-v.skew)) {
		return nil, fmt.Errorf("%w: nbf=%s now=%s", ErrNotYetValid,
			notBefore.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	switch {
	case now.Before(claims.ExpiresAt.Add(v.skew)):
		claims.Freshness = Fresh
	case now.Before(claims.ExpiresAt.Add(v.grace + v.skew)):
		claims.Freshness = Grace
	default:
		return nil, fmt.Errorf("%w: exp=%s now=%s",
			ErrExpired, claims.ExpiresAt.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	return claims, nil
}

// keyFor resolves the RSA public key for kid. A fresh cache returns without any
// network I/O — including for an UNKNOWN kid, which is rejected immediately so a
// stream of garbage kids cannot force a JWKS fetch per request (the pre-fix
// DoS-amplification vector). Otherwise it attempts at most one refresh per
// CacheTTL, coalescing concurrent refreshes via singleflight; the HTTP call
// NEVER runs under the state mutex, so a slow endpoint cannot serialize
// unrelated verifications. A failed refresh falls back to the cached keys for
// up to the offline grace; past that it hard-fails.
func (v *Verifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	if k := v.keys[kid]; k != nil && v.now().Sub(v.fetchedAt) <= v.cacheTTL {
		v.mu.Unlock()
		return k, nil
	}
	// Rate-limit refresh ATTEMPTS (success or failure) to one per CacheTTL, so a
	// fresh cache rejects an unknown kid with zero network I/O and a failing
	// endpoint is not hammered.
	mayRefresh := v.now().Sub(v.lastAttempt) >= v.cacheTTL
	inflight := v.inflight != nil
	v.mu.Unlock()

	// Start a refresh when the rate-limit allows a new attempt, OR simply wait
	// for one a concurrent caller already has in flight — otherwise a stampede
	// of cold callers would fail with an empty cache while a fetch is moments
	// from completing.
	if mayRefresh || inflight {
		v.refresh(ctx)
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	// Never fetched, or past the offline grace → the cache is too stale to trust.
	if len(v.keys) == 0 || v.now().Sub(v.fetchedAt) > v.grace {
		return nil, fmt.Errorf("%w: cache age %v", ErrKeysUnavailable, v.now().Sub(v.fetchedAt))
	}
	if k := v.keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("%w: kid=%q", ErrUnknownKID, kid)
}

// refresh fetches the JWKS at most once concurrently (singleflight): the first
// caller fetches while later callers wait on a channel rather than the mutex.
// The HTTP round-trip in fetchJWKS runs with NO lock held; only the brief
// bookkeeping before and after takes v.mu.
func (v *Verifier) refresh(ctx context.Context) {
	v.mu.Lock()
	if v.inflight != nil {
		done := v.inflight
		v.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
		}
		return
	}
	done := make(chan struct{})
	v.inflight = done
	v.lastAttempt = v.now()
	v.mu.Unlock()

	// Restore the singleflight invariant even if fetchJWKS panics (e.g. an
	// injected transport / unexpected runtime panic): without this, inflight
	// would stay non-nil and done would never close, wedging every future
	// refresh. On panic the followers wake and fall back to cache/grace instead
	// of blocking forever; the panic still propagates to the original caller.
	defer func() {
		v.mu.Lock()
		v.inflight = nil
		close(done)
		v.mu.Unlock()
	}()

	keys, err := v.fetchJWKS(ctx)

	v.mu.Lock()
	if err == nil && len(keys) > 0 {
		v.keys = keys
		v.fetchedAt = v.now()
	}
	v.mu.Unlock()
}

// fetchJWKS performs the JWKS GET and parse with no access to Verifier state,
// so it is safe to call without the mutex held.
func (v *Verifier) fetchJWKS(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks GET %s: HTTP %d", v.jwksURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseJWKS(body)
}

// parseJWKS decodes an RFC 7517 set into a kid→key map, skipping non-RSA or
// malformed entries. The dual-kid rotation set simply yields two entries.
func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var doc struct {
		Keys []struct{ Kty, Kid, N, E string } `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	out := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.N == "" || k.E == "" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		pub := &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: int(new(big.Int).SetBytes(eb).Int64()),
		}
		// Drop weak/absurd keys: a sub-2048-bit modulus is factorable and a
		// non-sane exponent is not a real RSA key. An all-dropped set yields the
		// "no usable keys" error below (fail closed).
		if pub.N.BitLen() < minRSABits || pub.E < 3 {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("entverify: JWKS carried no usable RSA keys")
	}
	return out, nil
}

// marshalJWKS serializes a kid→key map back to an RFC 7517 document — the
// inverse of parseJWKS, used by ExportJWKS to persist the cache for offline
// restart.
func marshalJWKS(keys map[string]*rsa.PublicKey) ([]byte, error) {
	type jwk struct {
		Kty string `json:"kty"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	}
	out := make([]jwk, 0, len(keys))
	for kid, k := range keys {
		out = append(out, jwk{
			Kty: "RSA", Use: "sig", Alg: "RS256", Kid: kid,
			N: base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		})
	}
	return json.Marshal(map[string]any{"keys": out})
}

func decodeSegment(seg string, dst any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func asString(v any) string { s, _ := v.(string); return s }

// asUnix reads a NumericDate claim (JSON number, or a string fallback).
func asUnix(v any) time.Time {
	switch n := v.(type) {
	case float64:
		return time.Unix(int64(n), 0)
	case json.Number:
		i, _ := n.Int64()
		return time.Unix(i, 0)
	case string:
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return time.Unix(i, 0)
		}
	}
	return time.Time{}
}

func asStringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = asString(val)
	}
	return out
}
