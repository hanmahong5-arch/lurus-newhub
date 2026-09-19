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
