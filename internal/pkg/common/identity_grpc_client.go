package common

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	identityv1 "github.com/LurusTech/lurus-proto-go/identity/v1"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

// identityGRPCAddr is the gRPC address for lurus-platform core (host:port).
var identityGRPCAddr = getIdentityGRPCAddr()

func getIdentityGRPCAddr() string {
	if addr := os.Getenv("IDENTITY_GRPC_ADDR"); addr != "" {
		return addr
	}
	return "platform-core.lurus-platform.svc.cluster.local:18105"
}

var (
	grpcClientOnce sync.Once
	grpcClient     identityv1.IdentityServiceClient
)

// getGRPCClient returns a singleton gRPC client. Returns nil if connection fails
// (callers fall back to HTTP).
func getGRPCClient() identityv1.IdentityServiceClient {
	grpcClientOnce.Do(func() {
		if identityGRPCAddr == "" {
			return
		}
		conn, err := grpc.NewClient(identityGRPCAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
		)
		if err != nil {
			slog.Error("identity grpc client: failed to connect", "addr", identityGRPCAddr, "err", err)
			return
		}
		grpcClient = identityv1.NewIdentityServiceClient(conn)
		slog.Info("identity grpc client connected", "addr", identityGRPCAddr)
	})
	return grpcClient
}

// grpcCtx adds the internal API key as gRPC metadata.
func grpcCtx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+IdentityServiceInternalKey)
}

// grpcTimeout wraps a context with a 5s timeout for gRPC calls.
func grpcTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(grpcCtx(ctx), 5*time.Second)
}

// grpcCtxIdem adds the internal API key AND an idempotency-key to gRPC metadata.
// The platform money-mutation RPCs (WalletDebit/Credit/PreAuthorize/Settle/Release)
// dedupe on the "idempotency-key" metadata header (platform server.go INT-3), so a
// retried or gRPC→HTTP-fallback call that carries the same key must not double-charge.
// An empty key is omitted (no metadata pair appended).
func grpcCtxIdem(ctx context.Context, idempotencyKey string) context.Context {
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+IdentityServiceInternalKey)
	if idempotencyKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "idempotency-key", idempotencyKey)
	}
	return ctx
}

// grpcTimeoutIdem is grpcTimeout plus an idempotency-key metadata header.
func grpcTimeoutIdem(ctx context.Context, idempotencyKey string) (context.Context, context.CancelFunc) {
	return context.WithTimeout(grpcCtxIdem(ctx, idempotencyKey), 5*time.Second)
}

// GetAccountByZitadelSubGRPC retrieves account info via gRPC.
// Falls back to HTTP if gRPC client is not available.
func GetAccountByZitadelSubGRPC(ctx context.Context, sub string) (*IdentityMapping, error) {
	client := getGRPCClient()
	if client == nil {
		return GetAccountByZitadelSub(ctx, sub) // fallback to HTTP
	}

	gctx, cancel := grpcTimeout(ctx)
	defer cancel()

	resp, err := client.GetAccountByZitadelSub(gctx, &identityv1.GetAccountByZitadelSubRequest{
		ZitadelSub: sub,
	})
	if err != nil {
		slog.Debug("identity grpc GetAccountByZitadelSub failed, falling back to HTTP", "err", err)
		return GetAccountByZitadelSub(ctx, sub)
	}

	return protoToIdentityMapping(resp), nil
}

// UpsertAccountGRPC creates or updates an account via gRPC.
// Falls back to HTTP if gRPC client is not available.
func UpsertAccountGRPC(ctx context.Context, zitadelSub, email, displayName, avatarURL string) (*IdentityMapping, error) {
	client := getGRPCClient()
	if client == nil {
		return UpsertAccount(ctx, zitadelSub, email, displayName, avatarURL)
	}

	gctx, cancel := grpcTimeout(ctx)
	defer cancel()

	resp, err := client.UpsertAccount(gctx, &identityv1.UpsertAccountRequest{
		ZitadelSub:  zitadelSub,
		Email:       email,
		DisplayName: displayName,
		AvatarUrl:   avatarURL,
	})
	if err != nil {
		slog.Debug("identity grpc UpsertAccount failed, falling back to HTTP", "err", err)
		return UpsertAccount(ctx, zitadelSub, email, displayName, avatarURL)
	}

	return protoToIdentityMapping(resp), nil
}

// GetEntitlementsGRPC retrieves entitlements via gRPC.
// Falls back to HTTP if gRPC client is not available.
func GetEntitlementsGRPC(ctx context.Context, accountID int64, productID string) (Entitlements, error) {
	client := getGRPCClient()
	if client == nil {
		return GetEntitlements(ctx, accountID, productID)
	}

	gctx, cancel := grpcTimeout(ctx)
	defer cancel()

	resp, err := client.GetEntitlements(gctx, &identityv1.GetEntitlementsRequest{
		AccountId: accountID,
		ProductId: productID,
	})
	if err != nil {
		slog.Debug("identity grpc GetEntitlements failed, falling back to HTTP", "err", err)
		return GetEntitlements(ctx, accountID, productID)
	}

	return Entitlements(resp.Entitlements), nil
}

// GetAccountOverviewGRPC retrieves the aggregated overview via gRPC.
// Falls back to HTTP if gRPC client is not available.
func GetAccountOverviewGRPC(ctx context.Context, accountID int64, productID string) (*AccountOverview, error) {
	client := getGRPCClient()
	if client == nil {
		return GetAccountOverview(ctx, accountID, productID)
	}

	gctx, cancel := grpcTimeout(ctx)
	defer cancel()

	resp, err := client.GetAccountOverview(gctx, &identityv1.GetAccountOverviewRequest{
		AccountId: accountID,
		ProductId: productID,
	})
	if err != nil {
		slog.Debug("identity grpc GetAccountOverview failed, falling back to HTTP", "err", err)
		return GetAccountOverview(ctx, accountID, productID)
	}

	return protoToAccountOverview(resp), nil
}

// ReportLLMUsageGRPC sends usage report via gRPC.
// Falls back to HTTP if gRPC client is not available.
func ReportLLMUsageGRPC(ctx context.Context, accountID int64, amountCNY float64) {
	client := getGRPCClient()
	if client == nil {
		ReportLLMUsage(ctx, accountID, amountCNY)
		return
	}

	gctx, cancel := grpcTimeout(ctx)
	defer cancel()

	_, err := client.ReportUsage(gctx, &identityv1.ReportUsageRequest{
		AccountId: accountID,
		AmountCny: amountCNY,
	})
	if err != nil {
		slog.Debug("identity grpc ReportUsage failed, falling back to HTTP", "err", err)
		ReportLLMUsage(ctx, accountID, amountCNY)
	}
}

// DebitWalletGRPC deducts credits from an account's wallet via gRPC.
// Falls back to HTTP if gRPC client is not available.
func DebitWalletGRPC(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) (*DebitWalletResult, error) {
	client := getGRPCClient()
	if client == nil {
		return DebitWallet(ctx, accountID, amount, txType, description, productID, idempotencyKey)
	}

	gctx, cancel := grpcTimeoutIdem(ctx, idempotencyKey)
	defer cancel()

	resp, err := client.WalletDebit(gctx, &identityv1.WalletOperationRequest{
		AccountId:   accountID,
		Amount:      amount,
		Type:        txType,
		ProductId:   productID,
		Description: description,
	})
	if err != nil {
		// Same idempotencyKey flows to the HTTP twin so a gRPC call that committed
		// on the server but failed to ack is deduped here, not charged twice.
		slog.Debug("identity grpc WalletDebit failed, falling back to HTTP", "err", err)
		return DebitWallet(ctx, accountID, amount, txType, description, productID, idempotencyKey)
	}

	// Record billing debit metric after confirmed success.
	// tenantID is not available at this call-site; label uses productID as the
	// closest scoping key. Relay-path callers that have tenantID should use
	// metrics.RecordBillingDebit(tenantID, amount) directly for finer granularity.
	if resp.Success {
		metrics.RecordBillingDebit(productID, amount)
	}

	return &DebitWalletResult{
		Success:      resp.Success,
		BalanceAfter: resp.BalanceAfter,
	}, nil
}

// PreAuthorizeGRPC freezes wallet balance via gRPC, falls back to HTTP.
func PreAuthorizeGRPC(ctx context.Context, accountID int64, amount float64, productID, referenceID, description string, ttlSeconds int) (*PreAuthResult, error) {
	client := getGRPCClient()
	if client == nil {
		return PreAuthorize(ctx, accountID, amount, productID, referenceID, description, ttlSeconds)
	}

	// PreAuth dedupes on referenceID (the relay request ref) so a retried freeze
	// reuses the prior hold instead of stacking a second one.
	gctx, cancel := grpcTimeoutIdem(ctx, referenceID)
	defer cancel()

	resp, err := client.WalletPreAuthorize(gctx, &identityv1.WalletPreAuthorizeRequest{
		AccountId:   accountID,
		Amount:      amount,
		ProductId:   productID,
		ReferenceId: referenceID,
		Description: description,
		TtlSeconds: int32(ttlSeconds), // #nosec G115 — TTL max is 30 days; fits in int32.
	})
	if err != nil {
		slog.Debug("identity grpc WalletPreAuthorize failed, falling back to HTTP", "err", err)
		return PreAuthorize(ctx, accountID, amount, productID, referenceID, description, ttlSeconds)
	}

	return &PreAuthResult{
		PreAuthID: resp.PreauthId,
		Amount:    resp.Amount,
		Status:    resp.Status,
	}, nil
}

// SettlePreAuthGRPC settles a pre-auth via gRPC, falls back to HTTP.
func SettlePreAuthGRPC(ctx context.Context, preAuthID int64, actualAmount float64) (*SettlePreAuthResult, error) {
	client := getGRPCClient()
	if client == nil {
		return SettlePreAuth(ctx, preAuthID, actualAmount)
	}

	gctx, cancel := grpcTimeoutIdem(ctx, fmt.Sprintf("settle:%d", preAuthID))
	defer cancel()

	resp, err := client.WalletSettlePreAuth(gctx, &identityv1.WalletSettlePreAuthRequest{
		PreauthId:    preAuthID,
		ActualAmount: actualAmount,
	})
	if err != nil {
		slog.Debug("identity grpc WalletSettlePreAuth failed, falling back to HTTP", "err", err)
		return SettlePreAuth(ctx, preAuthID, actualAmount)
	}

	return &SettlePreAuthResult{
		PreAuthID:    resp.PreauthId,
		Status:       resp.Status,
		HeldAmount:   resp.HeldAmount,
		ActualAmount: resp.ActualAmount,
	}, nil
}

// ReleasePreAuthGRPC releases a pre-auth via gRPC, falls back to HTTP.
func ReleasePreAuthGRPC(ctx context.Context, preAuthID int64) error {
	client := getGRPCClient()
	if client == nil {
		return ReleasePreAuth(ctx, preAuthID)
	}

	gctx, cancel := grpcTimeoutIdem(ctx, fmt.Sprintf("release:%d", preAuthID))
	defer cancel()

	_, err := client.WalletReleasePreAuth(gctx, &identityv1.WalletReleasePreAuthRequest{
		PreauthId: preAuthID,
	})
	if err != nil {
		slog.Debug("identity grpc WalletReleasePreAuth failed, falling back to HTTP", "err", err)
		return ReleasePreAuth(ctx, preAuthID)
	}

	return nil
}

// CreditWalletGRPC adds credits to an account's wallet via gRPC.
// Falls back to HTTP if gRPC client is not available.
func CreditWalletGRPC(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) error {
	client := getGRPCClient()
	if client == nil {
		return CreditWallet(ctx, accountID, amount, txType, description, productID, idempotencyKey)
	}

	gctx, cancel := grpcTimeoutIdem(ctx, idempotencyKey)
	defer cancel()

	_, err := client.WalletCredit(gctx, &identityv1.WalletOperationRequest{
		AccountId:   accountID,
		Amount:      amount,
		Type:        txType,
		ProductId:   productID,
		Description: description,
	})
	if err != nil {
		slog.Debug("identity grpc WalletCredit failed, falling back to HTTP", "err", err)
		return CreditWallet(ctx, accountID, amount, txType, description, productID, idempotencyKey)
	}

	return nil
}

// protoToIdentityMapping converts a proto Account to IdentityMapping.
func protoToIdentityMapping(a *identityv1.Account) *IdentityMapping {
	m := &IdentityMapping{
		ID:          a.Id,
		LurusID:     a.LurusId,
		ZitadelSub:  a.ZitadelSub,
		Email:       a.Email,
		DisplayName: a.DisplayName,
		AvatarURL:   a.AvatarUrl,
		Status: int16(a.Status), // #nosec G115 — account status is an enum (0-3); fits in int16.
	}
	if a.CreatedAt != nil {
		m.CreatedAt = a.CreatedAt.AsTime()
	}
	return m
}

// protoToAccountOverview converts a proto AccountOverview to the local struct.
func protoToAccountOverview(ov *identityv1.AccountOverview) *AccountOverview {
	topupURL := ov.TopupUrl
	if topupURL == "" {
		topupURL = IdentityPublicURL + "/wallet/topup"
	}
	result := &AccountOverview{
		TopupURL: topupURL,
	}

	if ov.Account != nil {
		result.Account.ID = ov.Account.Id
		result.Account.LurusID = ov.Account.LurusId
		result.Account.DisplayName = ov.Account.DisplayName
		result.Account.AvatarURL = ov.Account.AvatarUrl
	}

	if ov.Vip != nil {
		result.VIP.Level = int16(ov.Vip.Level) // #nosec G115 — VIP level is an enum (<100); fits in int16.
		result.VIP.LevelName = ov.Vip.LevelName
		result.VIP.LevelEN = ov.Vip.LevelEn
		result.VIP.Points = ov.Vip.Points
		if ov.Vip.LevelExpiresAt != nil {
			t := ov.Vip.LevelExpiresAt.AsTime().Format(time.RFC3339)
			result.VIP.LevelExpiresAt = &struct {
				Time string `json:"time"`
			}{Time: t}
		}
	}

	if ov.Wallet != nil {
		result.Wallet.Balance = ov.Wallet.Balance
		result.Wallet.Frozen = ov.Wallet.Frozen
	}

	if ov.Subscription != nil {
		expiresAt := ""
		if ov.Subscription.ExpiresAt != nil {
			t := ov.Subscription.ExpiresAt.AsTime().Format(time.RFC3339)
			expiresAt = t
		}
		result.Subscription = &struct {
			ProductID string  `json:"product_id"`
			PlanCode  string  `json:"plan_code"`
			Status    string  `json:"status"`
			ExpiresAt *string `json:"expires_at"`
			AutoRenew bool    `json:"auto_renew"`
		}{
			ProductID: ov.Subscription.ProductId,
			PlanCode:  ov.Subscription.PlanCode,
			Status:    ov.Subscription.Status,
			AutoRenew: ov.Subscription.AutoRenew,
		}
		if expiresAt != "" {
			result.Subscription.ExpiresAt = &expiresAt
		}
	}

	return result
}
