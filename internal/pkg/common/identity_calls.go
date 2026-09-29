package common

import (
	"context"
	"time"
)

// Budgeted identity/wallet calls to platform-core.
//
// These wrappers used to try a gRPC leg first and fall back to the HTTP twin.
// That gRPC leg never completed a single call: the lurus-proto-go request and
// response types are hand-written structs with no protoimpl, so every call
// failed in the client-side codec ("want proto.Message") and silently fell back
// to HTTP at Debug level. The gRPC leg was removed; what production actually ran
// — the HTTP twin inside one whole-call budget — is what remains.
//
// The *GRPC suffix is historical and kept only so this change does not touch
// every call site and test seam; each function is now exactly "the HTTP twin
// under identityTotalBudget". Renaming is a separate, mechanical follow-up.
//
// Idempotency is unchanged: the money calls carry their idempotency key (or
// referenceID / preAuthID) in the HTTP request itself, which is where the
// platform dedupes them.

// defaultIdentityTotalBudgetMS is the default wall-clock ceiling for one
// identity call (cycle 12 §2).
const defaultIdentityTotalBudgetMS = 5000

// identityTotalBudget is the wall-clock ceiling for one identity call.
// Env: IDENTITY_TIMEOUT_MS.
func identityTotalBudget() time.Duration {
	return time.Duration(GetEnvOrDefault("IDENTITY_TIMEOUT_MS", defaultIdentityTotalBudgetMS)) * time.Millisecond
}

// withIdentityBudget caps ctx at the whole-call budget. A caller that already
// carries a shorter deadline keeps it — WithTimeout never extends.
func withIdentityBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, identityTotalBudget())
}

// GetAccountByZitadelSubGRPC resolves an account by IdP subject (HTTP).
func GetAccountByZitadelSubGRPC(ctx context.Context, sub string) (*IdentityMapping, error) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return GetAccountByZitadelSub(bctx, sub)
}

// UpsertAccountGRPC creates or updates an account (HTTP).
func UpsertAccountGRPC(ctx context.Context, zitadelSub, email, displayName, avatarURL string) (*IdentityMapping, error) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return UpsertAccount(bctx, zitadelSub, email, displayName, avatarURL)
}

// GetEntitlementsGRPC retrieves entitlements (HTTP).
func GetEntitlementsGRPC(ctx context.Context, accountID int64, productID string) (Entitlements, error) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return GetEntitlements(bctx, accountID, productID)
}

// GetAccountOverviewGRPC retrieves the aggregated account overview (HTTP).
func GetAccountOverviewGRPC(ctx context.Context, accountID int64, productID string) (*AccountOverview, error) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return GetAccountOverview(bctx, accountID, productID)
}

// ReportLLMUsageGRPC sends a usage report (HTTP, fire-and-forget).
func ReportLLMUsageGRPC(ctx context.Context, accountID int64, amountCNY float64) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	ReportLLMUsage(bctx, accountID, amountCNY)
}

// DebitWalletGRPC deducts credits from an account's wallet (HTTP). DebitWallet
// records billing_debit_amount_cny{op="debit"} on a confirmed success.
func DebitWalletGRPC(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) (*DebitWalletResult, error) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return DebitWallet(bctx, accountID, amount, txType, description, productID, idempotencyKey)
}

// PreAuthorizeGRPC freezes wallet balance (HTTP).
func PreAuthorizeGRPC(ctx context.Context, accountID int64, amount float64, productID, referenceID, description string, ttlSeconds int) (*PreAuthResult, error) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return PreAuthorize(bctx, accountID, amount, productID, referenceID, description, ttlSeconds)
}

// SettlePreAuthGRPC settles a pre-auth (HTTP).
func SettlePreAuthGRPC(ctx context.Context, preAuthID int64, actualAmount float64) (*SettlePreAuthResult, error) {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return SettlePreAuth(bctx, preAuthID, actualAmount)
}

// ReleasePreAuthGRPC releases a pre-auth (HTTP).
func ReleasePreAuthGRPC(ctx context.Context, preAuthID int64) error {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return ReleasePreAuth(bctx, preAuthID)
}

// CreditWalletGRPC adds credits to an account's wallet (HTTP).
func CreditWalletGRPC(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) error {
	bctx, cancel := withIdentityBudget(ctx)
	defer cancel()
	return CreditWallet(bctx, accountID, amount, txType, description, productID, idempotencyKey)
}
