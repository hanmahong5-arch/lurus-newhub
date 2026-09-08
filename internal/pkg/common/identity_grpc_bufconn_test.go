package common

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	identityv1 "github.com/LurusTech/lurus-proto-go/identity/v1"
)

// billingDebitSampleCount reads the cumulative sample_count for one
// (product, op) series of billing_debit_amount_cny. Shared by
// identity_grpc_bufconn_test.go and grpc_fallback_test.go (same package).
func billingDebitSampleCount(t *testing.T, product, op string) int {
	t.Helper()
	metric, ok := metrics.BillingDebitAmountCNY.WithLabelValues(product, op).(prometheus.Metric)
	if !ok {
		t.Fatalf("BillingDebitAmountCNY observer does not implement prometheus.Metric")
	}
	var m dto.Metric
	if err := metric.Write(&m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	hist := m.GetHistogram()
	if hist == nil {
		t.Fatalf("metric is not a histogram")
	}
	return int(hist.GetSampleCount())
}

// withInjectedGRPCClient replaces the package gRPC-client singleton with a REAL
// grpc.ClientConn dialed over an in-process bufconn, then restores the prior
// singleton on cleanup. This drives the *GRPC wrappers down their non-nil-client
// branch (the client.X call actually executes) instead of the nil-client
// short-circuit that grpc_fallback_test.go already covers.
//
// NOTE: the lurus-proto-go request/response types in this build are plain
// structs with no proto.Message methods, so gRPC's default codec fails to
// marshal them and every call returns an Internal "error while marshaling"
// status BEFORE reaching any server. That is exactly the condition we exploit:
// with a live client but an unusable codec, each wrapper must degrade to its
// HTTP twin. (It also means the gRPC success-mapping return statements are
// structurally unreachable here — see ceilingReason.)
func withInjectedGRPCClient(t *testing.T) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	// A live server keeps the client connection READY, so each call fails fast at
	// the client-side marshal step instead of blocking on connect until timeout.
	srv := grpc.NewServer()
	identityv1.RegisterIdentityServiceServer(srv, identityv1.UnimplementedIdentityServiceServer{})
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}

	origClient := grpcClient
	// Run the real getGRPCClient once so its connect body is exercised (grpc.NewClient
	// is lazy, so this succeeds against the default address without a live platform),
	// then overwrite the singleton with our bufconn-backed client.
	_ = getGRPCClient()
	grpcClient = identityv1.NewIdentityServiceClient(conn)

	t.Cleanup(func() {
		grpcClient = origClient
		_ = conn.Close()
		srv.Stop()
		_ = lis.Close()
	})
}

// TestGRPC_LiveClientFallsBackToHTTP proves the resilience contract for the case
// a gRPC client IS configured but the call cannot complete (here: codec/marshal
// failure): every wrapper transparently retries its HTTP twin — carrying the
// same idempotency inputs — rather than surfacing a raw gRPC error to the relay
// path. This exercises the client.X invocation + error branch that the
// nil-client fallback test cannot reach.
func TestGRPC_LiveClientFallsBackToHTTP(t *testing.T) {
	withInjectedGRPCClient(t)
	if getGRPCClient() == nil {
		t.Fatal("injected gRPC client must be non-nil for this test")
	}

	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/wallet/pre-authorize"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 11, "status": "held", "amount": 2.0})
		case strings.Contains(r.URL.Path, "/settle"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 11, "status": "settled"})
		case strings.Contains(r.URL.Path, "/release"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/debit"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "balance_after": 7.0})
		case strings.Contains(r.URL.Path, "/credit"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/entitlements/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"plan_code": "pro"})
		case strings.Contains(r.URL.Path, "/overview"):
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{"id": 21}})
		case strings.Contains(r.URL.Path, "/upsert"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 22})
		case strings.Contains(r.URL.Path, "/by-idp-sub/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 23, "idp_subject": "s"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	ctx := context.Background()

	if m, err := GetAccountByZitadelSubGRPC(ctx, "s"); err != nil || m == nil || m.ID != 23 {
		t.Errorf("GetAccountByZitadelSubGRPC live-fallback: %+v err=%v", m, err)
	}
	if m, err := UpsertAccountGRPC(ctx, "s", "e", "n", "a"); err != nil || m == nil || m.ID != 22 {
		t.Errorf("UpsertAccountGRPC live-fallback: %+v err=%v", m, err)
	}
	if ent, err := GetEntitlementsGRPC(ctx, 1, "prod"); err != nil || ent.GetString("plan_code", "") != "pro" {
		t.Errorf("GetEntitlementsGRPC live-fallback: %+v err=%v", ent, err)
	}
	if ov, err := GetAccountOverviewGRPC(ctx, 21, ""); err != nil || ov == nil || ov.Account.ID != 21 {
		t.Errorf("GetAccountOverviewGRPC live-fallback: %+v err=%v", ov, err)
	}
	ReportLLMUsageGRPC(ctx, 1, 1.0) // fire-and-forget; must not panic

	if res, err := PreAuthorizeGRPC(ctx, 1, 2.0, "prod", "ref", "desc", 60); err != nil || res == nil || res.PreAuthID != 11 {
		t.Errorf("PreAuthorizeGRPC live-fallback: %+v err=%v", res, err)
	}
	if res, err := SettlePreAuthGRPC(ctx, 11, 1.5); err != nil || res == nil || res.Status != "settled" {
		t.Errorf("SettlePreAuthGRPC live-fallback: %+v err=%v", res, err)
	}
	if err := ReleasePreAuthGRPC(ctx, 11); err != nil {
		t.Errorf("ReleasePreAuthGRPC live-fallback: %v", err)
	}
	debitBefore := billingDebitSampleCount(t, "prod", "debit")
	if res, err := DebitWalletGRPC(ctx, 1, 3.0, "spend", "d", "prod", "idem"); err != nil || res == nil || !res.Success {
		t.Errorf("DebitWalletGRPC live-fallback: %+v err=%v", res, err)
	}
	// The codec failure sends this call down its HTTP fallback (see
	// withInjectedGRPCClient's doc comment), which must observe the same
	// billing_debit_amount_cny{product,op="debit"} series as the gRPC
	// success branch — one money-moving call, one metric, regardless of
	// which transport actually confirmed it.
	if got := billingDebitSampleCount(t, "prod", "debit") - debitBefore; got != 1 {
		t.Errorf("billing_debit_amount_cny{product=prod,op=debit} delta = %d, want 1", got)
	}
	if err := CreditWalletGRPC(ctx, 1, 3.0, "refund", "d", "prod", "idem"); err != nil {
		t.Errorf("CreditWalletGRPC live-fallback: %v", err)
	}
}
