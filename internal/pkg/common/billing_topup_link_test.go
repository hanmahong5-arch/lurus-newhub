package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// billing_topup_link_test.go — lurus-platform #106 puts the absolute wallet
// top-up URL in its insufficient_balance body as actions[0].url on wallet
// debit / pre-authorize. newhub must carry that link through to its API
// callers (as InsufficientBalanceError) WITHOUT changing how the rejection
// classifies: errors.Is(err, ErrInsufficientBalance) must keep holding, and a
// body without a usable link must still yield the bare sentinel.

const platform106Body = `{"error":"insufficient_balance","message":"Wallet available balance is insufficient for this pre-authorization","actions":[{"type":"link","label":"Top up wallet","url":"https://identity.lurus.cn/topup"}]}`

func serveOnce(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })
}

func TestPreAuthorize_InsufficientBalance_CarriesPlatformTopupURL(t *testing.T) {
	serveOnce(t, http.StatusPaymentRequired, platform106Body)
	_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref", "desc", 60)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("err = %v, want ErrInsufficientBalance identity preserved", err)
	}
	if got := PlatformTopupURL(err); got != "https://identity.lurus.cn/topup" {
		t.Errorf("PlatformTopupURL = %q, want the platform link verbatim", got)
	}
	if err.Error() != ErrInsufficientBalance.Error() {
		t.Errorf("Error() = %q, must stay %q (text-arm classification)", err.Error(), ErrInsufficientBalance.Error())
	}
	if !platformAnswered(err) {
		t.Error("platformAnswered must still treat the enriched sentinel as the platform's verdict")
	}
}

func TestDebitWallet_InsufficientBalance_CarriesPlatformTopupURL(t *testing.T) {
	serveOnce(t, http.StatusPaymentRequired, platform106Body)
	_, err := DebitWallet(context.Background(), 42, 2.5, "spend", "d", "prod", "idem")
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("err = %v, want ErrInsufficientBalance", err)
	}
	if got := PlatformTopupURL(err); got != "https://identity.lurus.cn/topup" {
		t.Errorf("PlatformTopupURL = %q", got)
	}
}

// Without a usable link (pre-#106 platform, relative path, non-link action)
// the result is the bare sentinel — byte-for-byte the old behaviour.
func TestInsufficientBalance_NoUsableLink_IsBareSentinel(t *testing.T) {
	for _, body := range []string{
		`{"error":"insufficient_balance"}`,
		`{"error":"insufficient_balance","actions":[]}`,
		`{"error":"insufficient_balance","actions":[{"type":"link","label":"Top up","url":"/topup"}]}`,
		`{"error":"insufficient_balance","actions":[{"type":"link","label":"x","url":"javascript:alert(1)"}]}`,
		`{"error":"insufficient_balance","actions":[{"type":"retry","label":"Retry","url":"https://x/y"}]}`,
		`{"error":"insufficient_balance","actions":[{"type":"link","label":"Other","url":""}]}`,
	} {
		serveOnce(t, http.StatusPaymentRequired, body)
		_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref", "desc", 60)
		if err != ErrInsufficientBalance { //nolint:errorlint // identity is the point
			t.Errorf("body %s: err = %#v, want the bare sentinel", body, err)
		}
		if PlatformTopupURL(err) != "" {
			t.Errorf("body %s: unexpected link %q", body, PlatformTopupURL(err))
		}
	}
}

// The first usable link wins when the platform sends several actions.
func TestInsufficientBalance_FirstUsableLinkWins(t *testing.T) {
	serveOnce(t, http.StatusPaymentRequired,
		`{"error":"insufficient_balance","actions":[{"type":"link","label":"rel","url":"/topup"},{"type":"link","label":"abs","url":"https://id.example.com/topup?x=1"},{"type":"link","label":"2nd","url":"https://other/"}]}`)
	_, err := PreAuthorize(context.Background(), 42, 2.5, "prod", "ref", "desc", 60)
	if got := PlatformTopupURL(err); got != "https://id.example.com/topup?x=1" {
		t.Errorf("PlatformTopupURL = %q", got)
	}
}

func TestPlatformTopupURL_OtherErrors(t *testing.T) {
	if PlatformTopupURL(nil) != "" || PlatformTopupURL(errors.New("x")) != "" ||
		PlatformTopupURL(&PlatformRejectedError{Op: "pre-authorize", Status: 400, Reason: "wallet_frozen"}) != "" {
		t.Error("only InsufficientBalanceError carries a link")
	}
}
