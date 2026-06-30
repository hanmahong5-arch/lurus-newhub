package middleware

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// JWKS Race Condition Tests
// These tests verify that the JWKSManager handles concurrent access correctly
// ============================================================================

func TestJWKS_ConcurrentRefresh(t *testing.T) {
	// Generate test key pair
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "concurrent-kid")}}

	// Track server hits
	var serverHits int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&serverHits, 1)
		// Add small delay to simulate network latency
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0, // Disable rate limiting for this test
	}

	// Launch 10 concurrent goroutines all trying to refresh
	var wg sync.WaitGroup
	concurrency := 10

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.tryRefreshKeys()
		}()
	}

	wg.Wait()

	// Due to the refreshing flag, we should have <= 2 server hits
	// (first one starts, others see refreshing=true and return)
	hits := atomic.LoadInt64(&serverHits)
	if hits > 2 {
		t.Errorf("expected <= 2 server hits (concurrent refresh protection), got %d", hits)
	}
}

func TestJWKS_RateLimiting(t *testing.T) {
	// Verify minRefreshInterval prevents abuse
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "rate-limit-kid")}}

	var serverHits int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&serverHits, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 30 * time.Second, // Standard rate limit
	}

	// Initial refresh should succeed
	refreshed1, err := mgr.tryRefreshKeys()
	if err != nil {
		t.Fatalf("first refresh failed: %v", err)
	}
	if !refreshed1 {
		t.Error("expected first refresh to be performed")
	}

	// Immediate second refresh should be rate limited
	refreshed2, _ := mgr.tryRefreshKeys()
	if refreshed2 {
		t.Error("expected second refresh to be rate limited")
	}

	// Verify only 1 server hit
	if atomic.LoadInt64(&serverHits) != 1 {
		t.Errorf("expected 1 server hit, got %d", atomic.LoadInt64(&serverHits))
	}
}

func TestJWKS_RefreshOnMissingKey(t *testing.T) {
	// Initial JWKS with one key
	_, pub1 := generateTestRSAKeyPair(t)
	_, pub2 := generateTestRSAKeyPair(t)

	currentJWKS := &JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub1, "kid-1")}}
	var serverHits int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&serverHits, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(currentJWKS)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0, // Disable rate limiting for this test
	}

	// Initial fetch
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("initial refresh failed: %v", err)
	}

	// kid-1 should be available
	key1, err := mgr.getKey("kid-1")
	if err != nil {
		t.Fatalf("getKey(kid-1) failed: %v", err)
	}
	if key1 == nil {
		t.Fatal("expected non-nil key for kid-1")
	}

	// kid-2 should not be available
	_, err = mgr.getKey("kid-2")
	if err == nil {
		t.Fatal("expected error for missing kid-2")
	}

	// Now add kid-2 to the server response
	currentJWKS.Keys = []JWK{
		rsaPublicKeyToJWK(pub1, "kid-1"),
		rsaPublicKeyToJWK(pub2, "kid-2"),
	}

	// getKeyWithRefresh should trigger refresh and find kid-2
	key2, err := mgr.getKeyWithRefresh("kid-2")
	if err != nil {
		t.Fatalf("getKeyWithRefresh(kid-2) failed: %v", err)
	}
	if key2 == nil {
		t.Fatal("expected non-nil key for kid-2 after refresh")
	}

	// Verify refresh was triggered (should be 2 server hits total)
	hits := atomic.LoadInt64(&serverHits)
	if hits != 2 {
		t.Errorf("expected 2 server hits, got %d", hits)
	}
}

func TestJWKS_ConcurrentKeyAccess(t *testing.T) {
	// Test multiple readers during refresh (race detector test)
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "race-kid")}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond) // Simulate network latency
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0,
	}

	// Initial load
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("initial refresh failed: %v", err)
	}

	// Launch concurrent readers and one writer
	var wg sync.WaitGroup

	// Readers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, _ = mgr.getKey("race-kid")
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	// Writer (refresh)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 3; j++ {
			mgr.tryRefreshKeys()
			time.Sleep(30 * time.Millisecond)
		}
	}()

	wg.Wait()

	// If we get here without race detector complaints, the test passes
	// The race detector will fail the test if any races are detected
}

func TestJWKS_RefreshingFlagAtomic(t *testing.T) {
	// Verify the refreshing flag properly prevents concurrent refreshes
	_, pub := generateTestRSAKeyPair(t)
	jwks := JWKSet{Keys: []JWK{rsaPublicKeyToJWK(pub, "atomic-kid")}}

	var activeRefreshes int32
	var maxConcurrent int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&activeRefreshes, 1)
		defer atomic.AddInt32(&activeRefreshes, -1)

		// Track max concurrent requests
		for {
			oldMax := atomic.LoadInt32(&maxConcurrent)
			if current <= oldMax || atomic.CompareAndSwapInt32(&maxConcurrent, oldMax, current) {
				break
			}
		}

		time.Sleep(100 * time.Millisecond) // Long operation
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0,
	}

	// Launch many concurrent refreshes
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.tryRefreshKeys()
		}()
	}

	wg.Wait()

	// Due to the refreshing flag, max concurrent should be 1
	if atomic.LoadInt32(&maxConcurrent) > 1 {
		t.Errorf("expected max 1 concurrent refresh, got %d", atomic.LoadInt32(&maxConcurrent))
	}
}

func TestJWKS_KeyMapConsistency(t *testing.T) {
	// Verify key map is updated atomically
	_, pub1 := generateTestRSAKeyPair(t)
	_, pub2 := generateTestRSAKeyPair(t)

	// Start with both keys
	currentJWKS := &JWKSet{Keys: []JWK{
		rsaPublicKeyToJWK(pub1, "key-a"),
		rsaPublicKeyToJWK(pub2, "key-b"),
	}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(currentJWKS)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0,
	}

	// Initial load
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("initial refresh failed: %v", err)
	}

	// Verify both keys are present
	if _, err := mgr.getKey("key-a"); err != nil {
		t.Errorf("key-a should be present: %v", err)
	}
	if _, err := mgr.getKey("key-b"); err != nil {
		t.Errorf("key-b should be present: %v", err)
	}

	// Remove key-a from server response (simulating key rotation)
	currentJWKS.Keys = []JWK{rsaPublicKeyToJWK(pub2, "key-b")}

	// Refresh
	if err := mgr.refreshKeys(); err != nil {
		t.Fatalf("refresh after rotation failed: %v", err)
	}

	// key-a should be gone, key-b should remain
	if _, err := mgr.getKey("key-a"); err == nil {
		t.Error("key-a should be removed after rotation")
	}
	if _, err := mgr.getKey("key-b"); err != nil {
		t.Errorf("key-b should still be present: %v", err)
	}
}

func TestJWKS_EmptyResponseHandling(t *testing.T) {
	// Verify error handling for empty JWKS response
	emptyJWKS := JWKSet{Keys: []JWK{}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(emptyJWKS)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0,
	}

	err := mgr.refreshKeys()
	if err == nil {
		t.Fatal("expected error for empty JWKS response")
	}
}

func TestJWKS_ServerTimeoutHandling(t *testing.T) {
	// Verify timeout handling (server takes too long)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than typical timeout
		time.Sleep(20 * time.Second)
	}))
	defer srv.Close()

	mgr := &JWKSManager{
		jwksURI:            srv.URL,
		publicKeys:         make(map[string]*rsa.PublicKey),
		minRefreshInterval: 0,
	}

	// This should timeout (using the global oidcHTTPClient with 15s timeout)
	start := time.Now()
	err := mgr.refreshKeys()
	elapsed := time.Since(start)

	// Should error within reasonable time (15-20 seconds)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	// Verify it didn't wait the full 20 seconds
	if elapsed > 18*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}
