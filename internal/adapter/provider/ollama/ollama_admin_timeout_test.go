package ollama

// ollama_admin_timeout_test.go — cycle 12 L7.
//
// FetchOllamaModels and DeleteOllamaModel each built a bare &http.Client{}: no
// Timeout, and a request with no context. Both are reached from console admin
// handlers (internal/adapter/handler/channel.go:301, :1497, :2430), so a
// self-hosted Ollama box that accepts the connection and then goes quiet held
// the operator's request open with nothing to end it.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ollamaHungUpstream serves a request that never answers inside the test's
// horizon, releasing on cleanup.
func ollamaHungUpstream(t *testing.T) string {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	return srv.URL
}

func withShortOllamaAdminBudget(t *testing.T) {
	t.Helper()
	prev := ollamaAdminBudget
	ollamaAdminBudget = 300 * time.Millisecond
	t.Cleanup(func() { ollamaAdminBudget = prev })
}

func TestFetchOllamaModels_HungHostIsBounded(t *testing.T) {
	withShortOllamaAdminBudget(t)
	base := ollamaHungUpstream(t)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := FetchOllamaModels(base, "")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("listing models from a host that never answers must return an error")
		}
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("FetchOllamaModels returned after %v with a 300ms budget", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchOllamaModels did not return within 2s against a host that never answers: " +
			"the call has neither a client timeout nor a request context")
	}
}

func TestDeleteOllamaModel_HungHostIsBounded(t *testing.T) {
	withShortOllamaAdminBudget(t)
	base := ollamaHungUpstream(t)

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- DeleteOllamaModel(base, "", "some-model") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("deleting a model on a host that never answers must return an error")
		}
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("DeleteOllamaModel returned after %v with a 300ms budget", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DeleteOllamaModel did not return within 2s against a host that never answers")
	}
}
