package app

// user_notify_timeout_test.go — cycle 12 L7 oracles for the bark and gotify
// delivery branches.
//
// The lane's first round bounded the webhook branch only, which left
// NotifyUser's ctx bounding one of its four delivery targets. Bark and gotify
// are the same defect verbatim: http.NewRequest with no context, sent on
// GetHttpClient(), whose Timeout is zero whenever RELAY_TIMEOUT is unset — the
// deployed default. Both URLs are customer-configured, so "accepts and goes
// quiet" is a shape a customer can hand us by accident.
//
// The email branch — the DEFAULT one — is bounded in internal/pkg/common/email.go
// and driven by internal/pkg/common/email_timeout_test.go.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// notifyHungUpstream serves a request that never answers inside the test's
// horizon, releasing on cleanup so nothing is left parked.
func notifyHungUpstream(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	return srv.URL
}

func withShortNotifyBudget(t *testing.T) {
	t.Helper()
	prev := notifySendBudget
	notifySendBudget = 300 * time.Millisecond
	t.Cleanup(func() { notifySendBudget = prev })
}

func assertNotifyReturns(t *testing.T, label string, send func() error) {
	t.Helper()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- send() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("%s: a push endpoint that never answers must return an error", label)
		}
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("%s: returned after %v with a 300ms budget", label, elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: did not return within 2s against an endpoint that never answers — the "+
			"request carries no context and the shared client has no timeout", label)
	}
}

func TestSendBarkNotify_HungEndpointIsBounded(t *testing.T) {
	allowLocalFetch(t)
	withShortNotifyBudget(t)
	url := notifyHungUpstream(t)
	notify := dto.NewNotify(dto.NotifyTypeQuotaExceed, "t", "c", nil)

	assertNotifyReturns(t, "bark", func() error {
		return sendBarkNotify(url+"/{{title}}/{{content}}", notify)
	})
}

func TestSendGotifyNotify_HungEndpointIsBounded(t *testing.T) {
	allowLocalFetch(t)
	withShortNotifyBudget(t)
	url := notifyHungUpstream(t)
	notify := dto.NewNotify(dto.NotifyTypeQuotaExceed, "t", "c", nil)

	assertNotifyReturns(t, "gotify", func() error {
		return sendGotifyNotify(url, "tok", 5, notify)
	})
}

// TestNotifySendBudgetDefault pins the production number: the tests above run at
// 300ms, and a shortened budget leaking into the shipped default would turn a
// slow-but-healthy push endpoint into a dropped notification.
func TestNotifySendBudgetDefault(t *testing.T) {
	if notifySendBudget != 10*time.Second {
		t.Errorf("notifySendBudget = %v, want 10s (same as webhookSendBudget)", notifySendBudget)
	}
	if webhookSendBudget != 10*time.Second {
		t.Errorf("webhookSendBudget = %v, want 10s", webhookSendBudget)
	}
}
