package common

// identity_timeout_test.go — cycle 12 L7 oracle for the platform identity client.
//
// Two separate defects, one symptom (plan §1.2 item 9):
//   - the gRPC client was built with WaitForReady(true), so a call to an
//     unreachable platform did not fail on "connection refused" — it parked
//     until the call deadline expired;
//   - the gRPC leg and the HTTP fallback leg each had their own independent 5s
//     budget, so one logical identity call could cost 10s of wall clock while
//     the caller believed it had handed out one deadline.
//
// The oracle here is total elapsed time across BOTH legs, not the gRPC leg
// alone: measuring only the first leg would have gone green while the fallback
// still doubled the wait.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	identityv1 "github.com/LurusTech/lurus-proto-go/identity/v1"
)

// closedPortGRPCClient installs a real gRPC client aimed at a port nothing is
// listening on, restoring the previous singleton on cleanup. grpc.NewClient is
// lazy, so construction succeeds and the failure surfaces on the first call —
// which is the production shape when platform-core is down.
func closedPortGRPCClient(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	// Consume the singleton's sync.Once first (the same trick
	// identity_grpc_bufconn_test.go uses) so the assignment below is not
	// overwritten by a later lazy init, then restore the prior value.
	prev := grpcClient
	_ = getGRPCClient()
	grpcClient = identityv1.NewIdentityServiceClient(conn)
	t.Cleanup(func() {
		grpcClient = prev
		_ = conn.Close()
	})
}

// hungIdentityHTTPServer points IdentityServiceURL at a server that never
// answers within the test's horizon. The handler also watches the request
// context so a client-side cancellation ends it immediately, which keeps
// srv.Close() from blocking on an in-flight request.
func hungIdentityHTTPServer(t *testing.T) {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	prevURL := IdentityServiceURL
	IdentityServiceURL = srv.URL
	// Registered in this order so cleanup runs LIFO: release the handler first,
	// then close the server.
	t.Cleanup(func() {
		IdentityServiceURL = prevURL
		srv.Close()
	})
	t.Cleanup(func() { close(release) })
}

// TestIdentityCall_TotalBudgetBoundsBothLegs is the timing oracle: one identity
// call against a dead gRPC port plus a hung HTTP fallback must respect the
// whole-call budget, not one budget per leg.
func TestIdentityCall_TotalBudgetBoundsBothLegs(t *testing.T) {
	t.Setenv("IDENTITY_TIMEOUT_MS", "800")
	t.Setenv("IDENTITY_GRPC_TIMEOUT_MS", "300")
	closedPortGRPCClient(t)
	hungIdentityHTTPServer(t)

	start := time.Now()
	_, err := UpsertAccountGRPC(context.Background(), "sub-l7", "l7@example.invalid", "L7", "")
	elapsed := time.Since(start)

	// The call degrades (nil mapping, nil error is the HTTP twin's contract on a
	// transport failure); what this test is about is how long it took.
	_ = err
	if elapsed > 2*time.Second {
		t.Fatalf("UpsertAccountGRPC took %v with an 800ms total budget: the gRPC leg and the "+
			"HTTP fallback are still budgeted independently", elapsed)
	}
}

// TestIdentityGRPCClient_DoesNotWaitForReady pins the other half. WaitForReady
// turns "connection refused" into "block until the deadline", which is the
// opposite of what a fallback path wants: the whole point of having an HTTP twin
// is to reach it quickly. The construction happens inside a sync.Once against a
// fixed address, so a behavioural test would have to fight the singleton —
// a source check is the honest instrument here.
func TestIdentityGRPCClient_DoesNotWaitForReady(t *testing.T) {
	raw, err := os.ReadFile("identity_grpc_client.go")
	if err != nil {
		t.Fatalf("read identity_grpc_client.go: %v", err)
	}
	// Match the call, not the word: the construction site carries a comment
	// explaining why the option is absent, and that comment names it.
	if strings.Contains(string(raw), "grpc.WaitForReady(") {
		t.Error("identity_grpc_client.go still asks for grpc.WaitForReady: a call to an unreachable " +
			"platform will park until the deadline instead of failing over to HTTP")
	}
}

// TestIdentityBudgetDefaults pins the numbers cycle 12 §2 decided on for a
// deployment that sets neither env var.
func TestIdentityBudgetDefaults(t *testing.T) {
	for _, k := range []string{"IDENTITY_TIMEOUT_MS", "IDENTITY_GRPC_TIMEOUT_MS"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Skipf("%s is set to %q in the ambient env; default case not applicable", k, v)
		}
	}
	if got := identityTotalBudget(); got != 5*time.Second {
		t.Errorf("identityTotalBudget() = %v, want 5s", got)
	}
	if got := identityGRPCLegTimeout(); got != 2*time.Second {
		t.Errorf("identityGRPCLegTimeout() = %v, want 2s", got)
	}
}

// TestIdentityGRPCLegNeverOutlivesTheTotalBudget: when the caller arrives with
// less budget than the gRPC leg would like, the leg must shrink rather than the
// total stretch.
func TestIdentityGRPCLegNeverOutlivesTheTotalBudget(t *testing.T) {
	t.Setenv("IDENTITY_TIMEOUT_MS", "150")
	t.Setenv("IDENTITY_GRPC_TIMEOUT_MS", "5000")

	bctx, cancel := withIdentityBudget(context.Background())
	defer cancel()
	gctx, gcancel := grpcTimeout(bctx)
	defer gcancel()

	deadline, ok := gctx.Deadline()
	if !ok {
		t.Fatal("grpcTimeout must attach a deadline")
	}
	if remaining := time.Until(deadline); remaining > 300*time.Millisecond {
		t.Errorf("gRPC leg deadline is %v away, but the whole call only had 150ms of budget", remaining)
	}
}

// countingClientProvider installs a client provider that records how many times
// the wrappers ask for a gRPC client, and hands back a real one aimed at a dead
// port so any call that does happen fails fast into the HTTP twin.
func countingClientProvider(t *testing.T) *int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	client := newIdentityGRPCClient(addr)
	if client == nil {
		t.Fatal("newIdentityGRPCClient must build a client for a syntactically valid address")
	}
	calls := 0
	prev := identityGRPCClient
	identityGRPCClient = func() identityv1.IdentityServiceClient {
		calls++
		return client
	}
	t.Cleanup(func() { identityGRPCClient = prev })
	return &calls
}

// tripBreaker drives the billing breaker to OPEN and restores it afterwards.
// Three consecutive failures is its threshold (billing_breaker.go).
func tripBreaker(t *testing.T) {
	t.Helper()
	BillingBreakerSuccess()
	BillingBreakerFailure()
	BillingBreakerFailure()
	BillingBreakerFailure()
	if !BillingBreakerIsOpen() {
		t.Fatal("three consecutive failures must open the breaker")
	}
	t.Cleanup(BillingBreakerSuccess)
}

// TestAccountLookupSkipsTheGRPCLegWhileTheBreakerIsOpen is the cycle 12 operator
// decision: the account/entitlement calls read the breaker (never write it) and,
// when it is open, go straight to the HTTP twin instead of re-paying the gRPC
// leg's budget to rediscover that platform-core is down.
func TestAccountLookupSkipsTheGRPCLegWhileTheBreakerIsOpen(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/entitlements/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"plan_code": "pro"})
		case strings.Contains(r.URL.Path, "/overview"):
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{"id": 5}})
		case strings.Contains(r.URL.Path, "/upsert"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 6})
		case strings.Contains(r.URL.Path, "/by-idp-sub/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "idp_subject": "s"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
	calls := countingClientProvider(t)
	tripBreaker(t)

	ctx := context.Background()
	if m, err := GetAccountByZitadelSubGRPC(ctx, "s"); err != nil || m == nil || m.ID != 7 {
		t.Errorf("GetAccountByZitadelSubGRPC with the breaker open: %+v err=%v", m, err)
	}
	if m, err := UpsertAccountGRPC(ctx, "s", "e", "n", ""); err != nil || m == nil || m.ID != 6 {
		t.Errorf("UpsertAccountGRPC with the breaker open: %+v err=%v", m, err)
	}
	if ent, err := GetEntitlementsGRPC(ctx, 1, "prod"); err != nil || ent.GetString("plan_code", "") != "pro" {
		t.Errorf("GetEntitlementsGRPC with the breaker open: %+v err=%v", ent, err)
	}
	if ov, err := GetAccountOverviewGRPC(ctx, 5, ""); err != nil || ov == nil || ov.Account.ID != 5 {
		t.Errorf("GetAccountOverviewGRPC with the breaker open: %+v err=%v", ov, err)
	}

	if *calls != 0 {
		t.Errorf("the gRPC client was resolved %d times with the breaker open, want 0: the "+
			"account/entitlement lookups are still paying the gRPC leg to rediscover a "+
			"platform-core this process already knows is down", *calls)
	}
	// Read-only: consulting the breaker must not have moved it, and nothing on
	// this path may record a failure against it either.
	if !BillingBreakerIsOpen() {
		t.Error("the account path changed breaker state; it must only read it")
	}
}

// TestAccountLookupUsesTheGRPCLegWhileTheBreakerIsClosed is the other half: the
// gate must be a gate, not an amputation.
func TestAccountLookupUsesTheGRPCLegWhileTheBreakerIsClosed(t *testing.T) {
	newIdentityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "idp_subject": "s"})
	})
	calls := countingClientProvider(t)
	BillingBreakerSuccess()
	t.Cleanup(BillingBreakerSuccess)

	if _, err := GetAccountByZitadelSubGRPC(context.Background(), "s"); err != nil {
		t.Errorf("GetAccountByZitadelSubGRPC: %v", err)
	}
	if *calls != 1 {
		t.Errorf("the gRPC client was resolved %d times with the breaker closed, want 1", *calls)
	}
}

// TestAccountLookupFailureNeverWritesBreakerState guards the half of the
// operator decision that is about the MONEY path: if the account path recorded
// failures, an identity-side blip would fast-fail wallet operations, and on a
// deployment with unified billing off nothing would ever close the breaker again
// because no money leg is there to probe it.
func TestAccountLookupFailureNeverWritesBreakerState(t *testing.T) {
	t.Setenv("IDENTITY_TIMEOUT_MS", "300")
	t.Setenv("IDENTITY_GRPC_TIMEOUT_MS", "100")
	newIdentityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	countingClientProvider(t)
	BillingBreakerSuccess()
	t.Cleanup(BillingBreakerSuccess)

	for i := 0; i < 5; i++ {
		_, _ = GetAccountByZitadelSubGRPC(context.Background(), "s")
		_, _ = GetEntitlementsGRPC(context.Background(), 1, "prod")
	}
	if BillingBreakerIsOpen() {
		t.Error("ten failed account/entitlement lookups opened the billing breaker: the lookup " +
			"path is writing breaker state, which would fast-fail the money path on an " +
			"identity-side blip")
	}
}

// TestMoneyLegIgnoresTheLookupGate: the wallet wrappers must keep trying gRPC
// first even while the breaker is open. Their own gate is BillingBreakerAllow,
// applied by the *WithBreaker wrappers one level up, and that one is allowed to
// move state.
func TestMoneyLegIgnoresTheLookupGate(t *testing.T) {
	t.Setenv("IDENTITY_TIMEOUT_MS", "500")
	t.Setenv("IDENTITY_GRPC_TIMEOUT_MS", "150")
	newIdentityServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "balance_after": 1.0})
	})
	calls := countingClientProvider(t)
	tripBreaker(t)

	if _, err := DebitWalletGRPC(context.Background(), 1, 1.0, "spend", "d", "prod", "idem"); err != nil {
		t.Errorf("DebitWalletGRPC: %v", err)
	}
	if *calls != 1 {
		t.Errorf("the money leg resolved the gRPC client %d times with the breaker open, want 1: "+
			"the read-only lookup gate has leaked onto the wallet path", *calls)
	}
}
