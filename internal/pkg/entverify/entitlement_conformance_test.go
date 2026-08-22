// Vendored from lurus-platform pkg/entverify/entitlement_conformance_test.go
// at platform master bc3d32a, ADAPTED for this module — see "ADAPTATION
// NOTES" below; re-copy + re-adapt from platform on drift, do not hand-edit.

//go:build entitlement_token_gate

package entverify

// Entitlement-token conformance GATE.
//
// Guarded by the entitlement_token_gate build tag (NOT part of the default
// `go test ./...`). Run:
//
//	go test -tags=entitlement_token_gate ./internal/pkg/entverify/ -run EntitlementTokenConformance
//
// Non-gameable invariants for the offline entitlement-token trust boundary —
// a regression that removes any guard turns the gate RED:
//
//  1. SIGNER KEY FLOOR — a sub-2048-bit key is rejected (a factorable modulus
//     published in the JWKS = universal token forgery).
//  2. VERIFIER REJECTION MATRIX — every adversarial token variant is rejected
//     with its SPECIFIC typed error, AND a valid token still verifies (so the
//     gate cannot pass by rejecting everything indiscriminately).
//  3. FAIL-CLOSED CONFIG — a non-https JWKS URL, an empty audience, and a
//     corrupt seed are refused, not silently accepted.
//  4. DoS BACKSTOP — an unknown-kid storm does not amplify into a JWKS fetch
//     per request (the pre-fix unauthenticated availability hole).
//
// ADAPTATION NOTES (this copy only, see the vendor header above):
//   - SIGNER KEY FLOOR calls localSignerKeyFloor (below), a local mirror of
//     entsign.parseRSAPrivateKey's bit-length check, instead of importing
//     platform's internal/pkg/entsign — Go's internal-package boundary
//     forbids a cross-module import of it, and this package's own tests are
//     already documented as self-contained / zero-platform-coupling (see
//     entverify_test.go's header). Only the floor check is mirrored; the real
//     signer (key generation, Sign, JWKS serving) stays platform-only — this
//     repo verifies entitlement tokens, it does not mint them.
//   - Each invariant runs as its own t.Run subtest (platform's original is
//     one monolithic test) so `-v` reports each independently; the pass/fail
//     bookkeeping (report + findings -> coverage.md) is unchanged.
//   - Mint-side authz (sub = authenticated caller, unauthenticated -> 401,
//     ent server-derived) is proven in platform's handler package
//     (TestEntitlementTokenHandler_GetToken_* in lurus-platform), not here —
//     this repo has no minting endpoint to test.
//
// Result written to _bmad-output/entitlement-token/coverage.md.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// localSignerKeyFloor mirrors platform's entsign.parseRSAPrivateKey bit-length
// floor check (MinSigningKeyBits=2048) locally — see "ADAPTATION NOTES" above
// for why. It reproduces ONLY the floor check under test; the real signer
// (key generation, Sign, JWKS serving) stays platform-only.
func localSignerKeyFloor(pemStr string) error {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return errors.New("no PEM block found")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	const minSigningKeyBits = 2048 // entsign.MinSigningKeyBits
	if bits := key.N.BitLen(); bits < minSigningKeyBits {
		return fmt.Errorf("signing key too small: %d bits (min %d)", bits, minSigningKeyBits)
	}
	return nil
}

func TestEntitlementTokenConformance(t *testing.T) {
	var findings []string
	report := map[string]string{}
	pass := func(k string) { report[k] = "✅" }
	fail := func(k, why string) {
		report[k] = "❌ " + why
		findings = append(findings, k+": "+why)
	}
	// runInvariant wraps one of the four documented invariant groups as its
	// own t.Run subtest — see "ADAPTATION NOTES" above — while keeping the
	// SAME pass/fail bookkeeping (report + findings) the platform original
	// uses for coverage.md: a subtest FAILs iff its block added a finding.
	runInvariant := func(name string, body func(t *testing.T)) {
		t.Run(name, func(t *testing.T) {
			before := len(findings)
			body(t)
			if len(findings) > before {
				t.Errorf("%s RED:\n  - %s", name, strings.Join(findings[before:], "\n  - "))
			}
		})
	}

	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	clock := WithClock(func() time.Time { return base })

	// (1) Signer key floor.
	runInvariant("1_signer_key_floor", func(t *testing.T) {
		pemPKCS1 := func(bits int) string {
			k, err := rsa.GenerateKey(rand.Reader, bits)
			if err != nil {
				t.Fatalf("keygen %d: %v", bits, err)
			}
			return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
		}
		if err := localSignerKeyFloor(pemPKCS1(1024)); err == nil {
			fail("signer:reject-1024bit", "accepted a factorable 1024-bit key")
		} else {
			pass("signer:reject-1024bit")
		}
		// Boundary: a key JUST below the floor must also be rejected, so the gate
		// PINS the floor at 2048 rather than merely bracketing it in (1024, 2048] —
		// a regression lowering MinSigningKeyBits to e.g. 1536 would otherwise stay
		// green.
		if err := localSignerKeyFloor(pemPKCS1(2047)); err == nil {
			fail("signer:reject-2047bit-boundary", "accepted a 2047-bit key (floor must be exactly 2048)")
		} else {
			pass("signer:reject-2047bit-boundary")
		}
		if err := localSignerKeyFloor(pemPKCS1(2048)); err != nil {
			fail("signer:accept-2048bit", fmt.Sprintf("rejected a valid key: %v", err))
		} else {
			pass("signer:accept-2048bit")
		}
	})

	// (2) Verifier rejection matrix — real RS256 signer, real JWKS server.
	cur := genKey(t)
	ghost := genKey(t) // signs tokens whose kid is absent from the JWKS
	srv := newJWKSServer(t, jwksDoc(kn{"cur", &cur.PublicKey}), nil)
	claims := func(exp, nbf time.Time, aud string) map[string]any {
		return map[string]any{
			"sub": "1", "aud": aud, "iat": base.Add(-time.Minute).Unix(),
			"nbf": nbf.Unix(), "exp": exp.Unix(), "ent": map[string]string{"plan_code": "pro"},
		}
	}
	tamper := func(tok string) string {
		p := strings.Split(tok, ".")
		pb, _ := base64.RawURLEncoding.DecodeString(p[1])
		pb[3] ^= 0xFF // flip a payload byte -> signature no longer matches
		p[1] = base64.RawURLEncoding.EncodeToString(pb)
		return strings.Join(p, ".")
	}
	valid := func() map[string]any { return claims(base.Add(time.Hour), base.Add(-time.Minute), "creator") }

	runInvariant("2_verifier_rejection_matrix", func(t *testing.T) {
		matrix := []struct {
			name    string
			tok     string
			aud     string
			wantErr error // nil = must verify
		}{
			{"valid-control", signRS256(t, cur, "cur", valid()), "creator", nil},
			{"alg-none", signWithAlg(t, cur, "cur", "none", valid()), "creator", ErrWrongAlg},
			{"alg-hs256-confusion", signWithAlg(t, cur, "cur", "HS256", valid()), "creator", ErrWrongAlg},
			{"expired-past-grace", signRS256(t, cur, "cur", claims(base.Add(-80*time.Hour), base.Add(-81*time.Hour), "creator")), "creator", ErrExpired},
			{"wrong-audience", signRS256(t, cur, "cur", valid()), "switch", ErrWrongAudience},
			{"unknown-kid", signRS256(t, ghost, "ghost", valid()), "creator", ErrUnknownKID},
			{"tampered-signature", tamper(signRS256(t, cur, "cur", valid())), "creator", ErrBadSignature},
			{"malformed", "only.two", "creator", ErrMalformed},
			{"not-yet-valid", signRS256(t, cur, "cur", claims(base.Add(25*time.Hour), base.Add(time.Hour), "creator")), "creator", ErrNotYetValid},
		}
		for _, tc := range matrix {
			v := New(srv.URL, clock, WithInsecureJWKS())
			_, err := v.Verify(context.Background(), tc.tok, tc.aud)
			ok := (tc.wantErr == nil && err == nil) || (tc.wantErr != nil && errors.Is(err, tc.wantErr))
			if ok {
				pass("verify:" + tc.name)
			} else {
				fail("verify:"+tc.name, fmt.Sprintf("err=%v want=%v", err, tc.wantErr))
			}
		}
	})

	// (3) Fail-closed config.
	runInvariant("3_fail_closed_config", func(t *testing.T) {
		vHTTP := New(srv.URL, clock) // no WithInsecureJWKS -> http must be refused
		if _, err := vHTTP.Verify(context.Background(), signRS256(t, cur, "cur", valid()), "creator"); errors.Is(err, ErrInsecureJWKSURL) {
			pass("config:https-required")
		} else {
			fail("config:https-required", fmt.Sprintf("err=%v want ErrInsecureJWKSURL", err))
		}
		vAud := New(srv.URL, clock, WithInsecureJWKS())
		if _, err := vAud.Verify(context.Background(), signRS256(t, cur, "cur", valid()), ""); errors.Is(err, ErrEmptyAudience) {
			pass("config:empty-audience-refused")
		} else {
			fail("config:empty-audience-refused", fmt.Sprintf("err=%v want ErrEmptyAudience", err))
		}
		vSeed := New("https://identity.lurus.cn/jwks", clock, WithSeedJWKS([]byte("{not valid")))
		if errors.Is(vSeed.Err(), ErrBadSeed) {
			pass("config:corrupt-seed-surfaced")
		} else {
			fail("config:corrupt-seed-surfaced", fmt.Sprintf("Err()=%v want ErrBadSeed", vSeed.Err()))
		}
	})

	// (4) DoS backstop — an unknown-kid storm must not fetch per request.
	runInvariant("4_dos_backstop", func(t *testing.T) {
		var hits int32
		dos := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&hits, 1)
			_, _ = w.Write(jwksDoc(kn{"cur", &cur.PublicKey}))
		}))
		defer dos.Close()
		vDoS := New(dos.URL, clock, WithInsecureJWKS())
		if _, err := vDoS.Verify(context.Background(), signRS256(t, cur, "cur", valid()), "creator"); err != nil {
			t.Fatalf("dos prime verify: %v", err)
		}
		for i := 0; i < 50; i++ {
			_, _ = vDoS.Verify(context.Background(), signRS256(t, ghost, "ghost", valid()), "creator")
		}
		if got := atomic.LoadInt32(&hits); got == 1 {
			report["dos:no-amplification"] = "✅ 1 fetch / 50 unknown-kid requests"
		} else {
			fail("dos:no-amplification", fmt.Sprintf("%d fetches for 50 unknown kids (want 1)", got))
		}
	})

	writeEntitlementReport(t, report, findings)
	if len(findings) > 0 {
		t.Fatalf("entitlement-token gate RED:\n  - %s\nSee _bmad-output/entitlement-token/coverage.md.",
			strings.Join(findings, "\n  - "))
	}
}

func writeEntitlementReport(t *testing.T, report map[string]string, findings []string) {
	keys := make([]string, 0, len(report))
	for k := range report {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Entitlement-token Conformance — offline trust-boundary proof\n\n")
	b.WriteString("Generated by `TestEntitlementTokenConformance`\n")
	b.WriteString("(`go test -tags=entitlement_token_gate ./internal/pkg/entverify/`).\n\n")
	b.WriteString("Invariants: (1) signer rejects < 2048-bit keys; (2) the verifier\n")
	b.WriteString("rejects every adversarial token variant with its specific typed error\n")
	b.WriteString("while still accepting a valid one; (3) non-https URL / empty audience /\n")
	b.WriteString("corrupt seed fail closed; (4) an unknown-kid storm does not amplify\n")
	b.WriteString("into a JWKS fetch per request. Mint-authz is proven in platform's\n")
	b.WriteString("handler package (this repo verifies entitlement tokens, it does not\n")
	b.WriteString("mint them).\n\n")
	b.WriteString(fmt.Sprintf("Findings: %d\n\n", len(findings)))
	b.WriteString("| invariant | status |\n|---|---|\n")
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("| `%s` | %s |\n", k, report[k]))
	}
	if len(findings) > 0 {
		b.WriteString("\n## Open findings\n\n")
		for _, f := range findings {
			b.WriteString("- " + f + "\n")
		}
	}

	dir := filepath.Join("..", "..", "..", "_bmad-output", "entitlement-token")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("report: mkdir: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "coverage.md"), []byte(b.String()), 0o644); err != nil {
		t.Logf("report: write coverage.md: %v", err)
	}
}
