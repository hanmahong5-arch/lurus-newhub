package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// billing_config_poll_test.go — cycle-13 L9 defect (1).
//
// The poller decodes the platform's billing-config body and applies it. When
// unified_billing_enabled was a plain bool, a 200 whose body did not STATE
// that key decoded to false and was applied as false: the flag went OFF on
// every replica within one poll, and because StartBillingConfigPoller polls
// once before its first tick, seconds after boot. Unified billing off stops
// the billing outbox from draining at all, so "the platform answered 200 with
// a body we did not recognise" was a silent fleet-wide money-path switch.
//
// These tests pin the three shapes that produce the same zero value —
// omitted, renamed, nested — plus the two that must still take effect
// (explicit true, explicit false).

// pollAgainst points IdentityServiceURL at a server that answers every request
// with the given JSON body, runs exactly one poll, and restores the URL.
func pollAgainst(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	defer func() { IdentityServiceURL = prev }()
	fetchAndApplyBillingConfig(context.Background())
}

// saveBillingFlags restores both process-global billing flags after the test.
func saveBillingFlags(t *testing.T) {
	t.Helper()
	unified, advisory := BillingUnifiedEnabled(), LocalLedgerAdvisory()
	prevSummary, _ := billingConfigEffective.Load().(string)
	t.Cleanup(func() {
		SetBillingUnifiedEnabled(unified)
		SetLocalLedgerAdvisory(advisory)
		billingConfigEffective.Store(prevSummary)
	})
}

// TestBillingConfigPoll_AbsentUnifiedKeyKeepsCurrentValue is the oracle for
// defect (1): a 200 that does not state unified_billing_enabled must leave the
// flag exactly as it was, in every shape that produces the Go zero value.
func TestBillingConfigPoll_AbsentUnifiedKeyKeepsCurrentValue(t *testing.T) {
	saveBillingFlags(t)

	bodies := map[string]string{
		"key omitted entirely": `{"local_ledger_advisory":false}`,
		"key renamed":          `{"unified_billing":true,"local_ledger_advisory":false}`,
		"key nested":           `{"billing":{"unified_billing_enabled":true}}`,
		"empty object":         `{}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			SetBillingUnifiedEnabled(true)
			pollAgainst(t, body)
			if !BillingUnifiedEnabled() {
				t.Fatalf("a 200 that never states unified_billing_enabled (%s) turned unified billing OFF; "+
					"the billing outbox stops draining fleet-wide on the next poll", body)
			}
		})
	}
}

// TestBillingConfigPoll_StatedValuesStillApply is the other half: the pointer
// must not turn the poller into a one-way ratchet. An explicit true and an
// explicit false both still land, for both flags.
func TestBillingConfigPoll_StatedValuesStillApply(t *testing.T) {
	saveBillingFlags(t)

	SetBillingUnifiedEnabled(false)
	SetLocalLedgerAdvisory(false)
	pollAgainst(t, `{"unified_billing_enabled":true,"local_ledger_advisory":true}`)
	if !BillingUnifiedEnabled() || !LocalLedgerAdvisory() {
		t.Fatalf("stated true did not apply: unified=%v advisory=%v",
			BillingUnifiedEnabled(), LocalLedgerAdvisory())
	}

	pollAgainst(t, `{"unified_billing_enabled":false,"local_ledger_advisory":false}`)
	if BillingUnifiedEnabled() || LocalLedgerAdvisory() {
		t.Fatalf("stated false did not apply (operator dial-down is broken): unified=%v advisory=%v",
			BillingUnifiedEnabled(), LocalLedgerAdvisory())
	}
}

// TestBillingConfigPoll_EffectiveValueIsObservable pins the second half of
// defect (1): "the flag is kept on absence" must be readable from the running
// process, not only from a comment. BillingConfigEffective() states the value
// AND where it came from, and a change to that summary is logged once (not
// every 30s).
func TestBillingConfigPoll_EffectiveValueIsObservable(t *testing.T) {
	saveBillingFlags(t)
	covInitTextMode(t)
	out, _ := covSwapGinWriters(t)

	billingConfigEffective.Store("")
	SetBillingUnifiedEnabled(true)
	pollAgainst(t, `{"local_ledger_advisory":false}`)

	eff := BillingConfigEffective()
	if !strings.Contains(eff, "unified=true") {
		t.Errorf("BillingConfigEffective() = %q, want it to state unified=true", eff)
	}
	if !strings.Contains(eff, "kept") {
		t.Errorf("BillingConfigEffective() = %q, want it to name the absent key's source as kept", eff)
	}
	if !strings.Contains(out.String(), eff) {
		t.Errorf("the effective summary %q was never logged; log was %q", eff, out.String())
	}

	// A second identical poll must not re-log — the poller runs every 30s.
	out.Reset()
	pollAgainst(t, `{"local_ledger_advisory":false}`)
	if out.Len() != 0 {
		t.Errorf("an unchanged poll re-logged the summary (30s spam): %q", out.String())
	}
}

// TestBillingConfigPoll_FailuresStillKeepTheValue re-pins the pre-existing
// fail-keep behaviour that the pointer change must not disturb: a non-200 and
// an unparseable body both leave both flags alone.
func TestBillingConfigPoll_FailuresStillKeepTheValue(t *testing.T) {
	saveBillingFlags(t)

	SetBillingUnifiedEnabled(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]bool{"unified_billing_enabled": false})
	}))
	defer srv.Close()
	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	t.Cleanup(func() { IdentityServiceURL = prev })
	fetchAndApplyBillingConfig(context.Background())
	if !BillingUnifiedEnabled() {
		t.Error("non-200 must not change the flag")
	}

	pollAgainst(t, `not json at all`)
	if !BillingUnifiedEnabled() {
		t.Error("unparseable body must not change the flag")
	}
}
