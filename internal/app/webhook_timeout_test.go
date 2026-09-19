package app

// webhook_timeout_test.go — cycle 12 L7 oracle for outbound webhook delivery.
//
// SendWebhookNotify built its request with http.NewRequest (no context) and
// sent it on GetHttpClient(), whose Timeout is zero whenever RELAY_TIMEOUT is
// unset — which is the deployed configuration. A customer-configured webhook
// endpoint that accepts the connection and then goes quiet therefore held the
// notifying goroutine indefinitely, and the notify path is reached from quota
// accounting, so the endpoint under test is one a customer controls.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// webhookHungUpstream serves a request that never answers inside the test's
// horizon, releasing on cleanup so nothing is left parked.
func webhookHungUpstream(t *testing.T) string {
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

func TestSendWebhookNotify_HungUpstreamIsBounded(t *testing.T) {
	allowLocalFetch(t)
	prev := webhookSendBudget
	webhookSendBudget = 300 * time.Millisecond
	t.Cleanup(func() { webhookSendBudget = prev })

	url := webhookHungUpstream(t)
	notify := dto.NewNotify(dto.NotifyTypeQuotaExceed, "t", "c", nil)

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- SendWebhookNotify(url, "", notify) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a webhook to an endpoint that never answers must return an error")
		}
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("SendWebhookNotify returned after %v with a 300ms budget", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendWebhookNotify did not return within 2s against an endpoint that never " +
			"answers: the request carries no context and the shared client has no timeout")
	}
}

func TestSendWebhookNotifyWithContext_HonoursCallerDeadline(t *testing.T) {
	allowLocalFetch(t)
	url := webhookHungUpstream(t)
	notify := dto.NewNotify(dto.NotifyTypeQuotaExceed, "t", "c", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := SendWebhookNotifyWithContext(ctx, url, "", notify)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the caller's deadline expires mid-send")
	}
	if elapsed > time.Second {
		t.Errorf("SendWebhookNotifyWithContext took %v with a 200ms caller budget", elapsed)
	}
}

// TestSendWebhookNotify_StillDeliversAndSigns is the do-not-regress half: the
// bound must not change what a healthy endpoint receives.
func TestSendWebhookNotify_StillDeliversAndSigns(t *testing.T) {
	allowLocalFetch(t)

	var gotSig string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Webhook-Signature")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	notify := dto.NewNotify(dto.NotifyTypeQuotaExceed, "quota low", "remaining", nil)
	if err := SendWebhookNotify(srv.URL, "s3cr3t", notify); err != nil {
		t.Fatalf("SendWebhookNotify: %v", err)
	}
	if gotSig == "" {
		t.Error("signature header missing")
	}
	if len(gotBody) == 0 {
		t.Error("payload body missing")
	}
}
