package middleware

import (
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestZitadelClient_ServerDown(t *testing.T) {
	// Create a server and immediately close it to simulate connection refused
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Close()

	m := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}

	err := m.refreshKeys()
	if err == nil {
		t.Fatal("expected error when server is down, got nil")
	}
}

func TestZitadelClient_EmptyResponse(t *testing.T) {
	// Server returns empty JSON object (no keys)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"keys":[]}`))
	}))
	defer srv.Close()

	m := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}

	err := m.refreshKeys()
	if err == nil {
		t.Fatal("expected error for empty JWKS keys, got nil")
	}
}

func TestZitadelClient_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}

	err := m.refreshKeys()
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
}

func TestZitadelClient_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not valid json`))
	}))
	defer srv.Close()

	m := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}

	err := m.refreshKeys()
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestZitadelClient_Timeout(t *testing.T) {
	// Server delays response; after bug fix, oidcHTTPClient has a timeout
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		w.Write([]byte(`{"keys":[]}`))
	}))
	defer func() {
		close(done) // unblock the handler so srv.Close() doesn't hang
		srv.Close()
	}()

	// Save and restore the package-level client
	origClient := oidcHTTPClient
	defer func() { oidcHTTPClient = origClient }()

	// Use a client with a very short timeout for testing
	oidcHTTPClient = &http.Client{Timeout: 100 * time.Millisecond}

	m := &JWKSManager{
		jwksURI:    srv.URL,
		publicKeys: make(map[string]*rsa.PublicKey),
	}

	err := m.refreshKeys()
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
