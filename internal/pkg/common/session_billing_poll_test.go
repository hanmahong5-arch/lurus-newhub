package common

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// makeSessionToken builds a platform-style HS256 session token signed with secret.
func makeSessionToken(t *testing.T, secret, iss, sub string, exp int64) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := enc(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload := enc(map[string]any{"iss": iss, "sub": sub, "exp": exp})
	body := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig
}

func TestValidateIdentitySessionToken(t *testing.T) {
	const secret = "test-session-secret-123"
	orig := IdentitySessionSecret
	IdentitySessionSecret = secret
	t.Cleanup(func() { IdentitySessionSecret = orig })

	future := time.Now().Add(time.Hour).Unix()

	// Valid token.
	tok := makeSessionToken(t, secret, "lurus-platform", "lurus:42", future)
	id, err := ValidateIdentitySessionToken(tok)
	if err != nil || id != 42 {
		t.Fatalf("valid token: got (%d,%v), want (42,nil)", id, err)
	}

	// Legacy issuer still accepted.
	tok2 := makeSessionToken(t, secret, "lurus-identity", "lurus:7", future)
	if id, err := ValidateIdentitySessionToken(tok2); err != nil || id != 7 {
		t.Errorf("legacy issuer: got (%d,%v)", id, err)
	}

	// Wrong signature (signed with a different secret).
	bad := makeSessionToken(t, "other-secret", "lurus-platform", "lurus:42", future)
	if _, err := ValidateIdentitySessionToken(bad); err == nil {
		t.Error("token with wrong signature must be rejected")
	}

	// Malformed (not 3 parts).
	if _, err := ValidateIdentitySessionToken("only.two"); err == nil {
		t.Error("malformed token must be rejected")
	}

	// Unexpected issuer.
	iss := makeSessionToken(t, secret, "evil-issuer", "lurus:42", future)
	if _, err := ValidateIdentitySessionToken(iss); err == nil {
		t.Error("unexpected issuer must be rejected")
	}

	// Expired.
	exp := makeSessionToken(t, secret, "lurus-platform", "lurus:42", time.Now().Add(-time.Hour).Unix())
	if _, err := ValidateIdentitySessionToken(exp); err == nil {
		t.Error("expired token must be rejected")
	}

	// Bad sub format.
	subBad := makeSessionToken(t, secret, "lurus-platform", "not-lurus-prefixed", future)
	if _, err := ValidateIdentitySessionToken(subBad); err == nil {
		t.Error("bad sub prefix must be rejected")
	}

	// Non-numeric account id.
	subNum := makeSessionToken(t, secret, "lurus-platform", "lurus:abc", future)
	if _, err := ValidateIdentitySessionToken(subNum); err == nil {
		t.Error("non-numeric account id must be rejected")
	}

	// Secret not configured.
	IdentitySessionSecret = ""
	if _, err := ValidateIdentitySessionToken(tok); err == nil {
		t.Error("unconfigured secret must be rejected")
	}
}

func TestFetchAndApplyBillingConfig(t *testing.T) {
	origFlag := BillingUnifiedEnabled()
	t.Cleanup(func() { SetBillingUnifiedEnabled(origFlag) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, billingConfigPath) {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"unified_billing_enabled": true})
	}))
	defer srv.Close()

	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })

	SetBillingUnifiedEnabled(false)
	fetchAndApplyBillingConfig(context.Background())
	if !BillingUnifiedEnabled() {
		t.Error("poll should have flipped the unified-billing flag to true")
	}

	// Non-200 leaves the flag unchanged (fail-keep).
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()
	IdentityServiceURL = srv500.URL
	SetBillingUnifiedEnabled(true)
	fetchAndApplyBillingConfig(context.Background())
	if !BillingUnifiedEnabled() {
		t.Error("non-200 poll must not change the flag")
	}
}

func TestStartBillingConfigPoller(t *testing.T) {
	// Empty URL: no-op, returns without spawning, and the done channel is
	// already closed.
	prev := IdentityServiceURL
	IdentityServiceURL = ""
	t.Cleanup(func() { IdentityServiceURL = prev })
	select {
	case <-StartBillingConfigPoller(context.Background()):
	case <-time.After(time.Second):
		t.Fatal("no-op poller must hand back an already-closed done channel")
	}

	// Configured URL: immediate poll fires against the fake server.
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hit <- struct{}{}:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"unified_billing_enabled": false})
	}))
	defer srv.Close()
	IdentityServiceURL = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := StartBillingConfigPoller(ctx)
	select {
	case <-hit:
	case <-time.After(2 * time.Second):
		t.Error("poller did not perform its immediate startup poll")
	}

	// The poller must be gone before this test returns. A goroutine left
	// behind keeps calling SysLog while later tests in this package swap the
	// global slog writer for a bytes.Buffer and read it back; the CI race job
	// reported exactly that pair on 2026-09-19 (TestSlogJSONHandler_ContextInjection).
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller goroutine did not exit after its context was cancelled")
	}
}

func TestParseErrorResponse(t *testing.T) {
	if got := parseErrorResponse(strings.NewReader(`{"error":"insufficient_balance"}`)); got != "insufficient_balance" {
		t.Errorf("got %q", got)
	}
	if got := parseErrorResponse(strings.NewReader(`{}`)); got != "unknown" {
		t.Errorf("empty error should be unknown, got %q", got)
	}
	if got := parseErrorResponse(strings.NewReader(`not json`)); got != "unknown" {
		t.Errorf("bad json should be unknown, got %q", got)
	}
}
