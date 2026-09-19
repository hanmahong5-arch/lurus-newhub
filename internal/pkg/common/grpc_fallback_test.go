package common

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"

	identityv1 "github.com/LurusTech/lurus-proto-go/identity/v1"
)

// withoutGRPCClient forces the "no gRPC client configured" condition through the
// package's client seam. Doing it by blanking identityGRPCAddr instead only
// works for whichever test reaches grpcClientOnce first, which under
// -shuffle=on is a coin flip — this test failed that way in 1 of 6 shuffled
// runs during cycle 12 acceptance.
func withoutGRPCClient(t *testing.T) {
	t.Helper()
	prev := identityGRPCClient
	identityGRPCClient = func() identityv1.IdentityServiceClient { return nil }
	t.Cleanup(func() { identityGRPCClient = prev })
}

// TestGRPC_MetadataHelpers exercises the pure gRPC metadata/context builders.
func TestGRPC_MetadataHelpers(t *testing.T) {
	orig := IdentityServiceInternalKey
	IdentityServiceInternalKey = "secret-key"
	t.Cleanup(func() { IdentityServiceInternalKey = orig })

	ctx := grpcCtx(context.Background())
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok || len(md.Get("authorization")) == 0 || md.Get("authorization")[0] != "Bearer secret-key" {
		t.Errorf("grpcCtx missing authorization metadata: %v", md)
	}

	// Idempotency key present.
	ctx2 := grpcCtxIdem(context.Background(), "idem-1")
	md2, _ := metadata.FromOutgoingContext(ctx2)
	if len(md2.Get("idempotency-key")) == 0 || md2.Get("idempotency-key")[0] != "idem-1" {
		t.Errorf("grpcCtxIdem missing idempotency-key: %v", md2)
	}
	// Empty idempotency key is omitted.
	ctx3 := grpcCtxIdem(context.Background(), "")
	md3, _ := metadata.FromOutgoingContext(ctx3)
	if len(md3.Get("idempotency-key")) != 0 {
		t.Errorf("empty idempotency-key should be omitted: %v", md3)
	}

	tctx, cancel := grpcTimeout(context.Background())
	defer cancel()
	if _, hasDeadline := tctx.Deadline(); !hasDeadline {
		t.Error("grpcTimeout should attach a deadline")
	}
	tctx2, cancel2 := grpcTimeoutIdem(context.Background(), "k")
	defer cancel2()
	if _, hasDeadline := tctx2.Deadline(); !hasDeadline {
		t.Error("grpcTimeoutIdem should attach a deadline")
	}
}

// TestGRPC_FallsBackToHTTP forces the gRPC client to be unavailable (empty
// address ⇒ nil client) and verifies every *GRPC wrapper transparently
// degrades to its HTTP counterpart — the resilience contract that keeps relay
// billing working when the gRPC port is unreachable.
func TestGRPC_FallsBackToHTTP(t *testing.T) {
	withoutGRPCClient(t)
	if identityGRPCClient() != nil {
		t.Fatal("the test seam must yield a nil client")
	}
	// The breaker gate in front of the account/entitlement lookups must not be
	// what makes them fall back here: force it closed so this test measures the
	// nil-client contract and nothing else. (Whatever ran before us may have left
	// it open; nothing in this package resets it on the way out.)
	BillingBreakerSuccess()
	t.Cleanup(BillingBreakerSuccess)

	newIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Answer any HTTP fallback endpoint generically.
		switch {
		case strings.Contains(r.URL.Path, "/wallet/pre-authorize"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 1, "status": "held"})
		case strings.Contains(r.URL.Path, "/settle"):
			_ = json.NewEncoder(w).Encode(map[string]any{"preauth_id": 1, "status": "settled"})
		case strings.Contains(r.URL.Path, "/release"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/debit"):
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "balance_after": 5.0})
		case strings.Contains(r.URL.Path, "/credit"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/entitlements/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"plan_code": "pro"})
		case strings.Contains(r.URL.Path, "/overview"):
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{"id": 9}})
		case strings.Contains(r.URL.Path, "/upsert"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 3})
		case strings.Contains(r.URL.Path, "/by-idp-sub/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "idp_subject": "s"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	ctx := context.Background()

	if m, err := GetAccountByZitadelSubGRPC(ctx, "s"); err != nil || m == nil || m.ID != 7 {
		t.Errorf("GetAccountByZitadelSubGRPC fallback: %+v err=%v", m, err)
	}
	if m, err := UpsertAccountGRPC(ctx, "s", "e", "n", "a"); err != nil || m == nil || m.ID != 3 {
		t.Errorf("UpsertAccountGRPC fallback: %+v err=%v", m, err)
	}
	if ent, err := GetEntitlementsGRPC(ctx, 1, "prod"); err != nil || ent.GetString("plan_code", "") != "pro" {
		t.Errorf("GetEntitlementsGRPC fallback: %+v err=%v", ent, err)
	}
	if ov, err := GetAccountOverviewGRPC(ctx, 9, ""); err != nil || ov == nil || ov.Account.ID != 9 {
		t.Errorf("GetAccountOverviewGRPC fallback: %+v err=%v", ov, err)
	}
	ReportLLMUsageGRPC(ctx, 1, 1.0) // fire-and-forget, must not panic

	if res, err := PreAuthorizeGRPC(ctx, 1, 1.0, "prod", "ref", "desc", 60); err != nil || res == nil {
		t.Errorf("PreAuthorizeGRPC fallback: %+v err=%v", res, err)
	}
	if res, err := SettlePreAuthGRPC(ctx, 1, 0.5); err != nil || res == nil {
		t.Errorf("SettlePreAuthGRPC fallback: %+v err=%v", res, err)
	}
	if err := ReleasePreAuthGRPC(ctx, 1); err != nil {
		t.Errorf("ReleasePreAuthGRPC fallback: %v", err)
	}
	debitBefore := billingDebitSampleCount(t, "prod", "debit")
	if res, err := DebitWalletGRPC(ctx, 1, 1.0, "spend", "d", "prod", "idem"); err != nil || res == nil || !res.Success {
		t.Errorf("DebitWalletGRPC fallback: %+v err=%v", res, err)
	}
	// Empty gRPC address -> nil client -> DebitWalletGRPC calls the HTTP twin
	// (DebitWallet) directly, not via its own internal fallback branch. That
	// HTTP path previously observed nothing on a confirmed debit.
	if got := billingDebitSampleCount(t, "prod", "debit") - debitBefore; got != 1 {
		t.Errorf("billing_debit_amount_cny{product=prod,op=debit} delta = %d, want 1", got)
	}
	if err := CreditWalletGRPC(ctx, 1, 1.0, "refund", "d", "prod", "idem"); err != nil {
		t.Errorf("CreditWalletGRPC fallback: %v", err)
	}
}

// TestNewIdentityGRPCClient_EmptyAddressYieldsNil covers the branch the seam
// above no longer reaches: a deployment with IDENTITY_GRPC_ADDR blanked runs
// HTTP-only rather than building a client aimed at nothing. Driven through the
// constructor rather than getGRPCClient so it does not depend on the sync.Once.
func TestNewIdentityGRPCClient_EmptyAddressYieldsNil(t *testing.T) {
	if c := newIdentityGRPCClient(""); c != nil {
		t.Errorf("newIdentityGRPCClient(\"\") = %v, want nil", c)
	}
	if c := newIdentityGRPCClient("127.0.0.1:1"); c == nil {
		t.Error("a syntactically valid address must yield a client: grpc.NewClient is lazy, so " +
			"an unreachable port is a call-time failure, not a construction failure")
	}
}
