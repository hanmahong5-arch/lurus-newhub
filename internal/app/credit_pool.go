// Package app — credit pool metric wiring.
//
// This file is the single call-site for pool-exhaustion and billing-debit metrics
// so the two counters added in δ2 (Wave-UAT) have an unambiguous home.
// The actual pool enforcement logic lives in internal/adapter/middleware/pool_balance_check.go;
// the actual gRPC debit call lives in internal/pkg/common/identity_grpc_client.go.
// This file exposes thin wrappers that are called from those locations so that
// the metric recording is testable in isolation without importing adapter packages.
package app

import "github.com/LurusTech/lurus-hub/internal/pkg/metrics"

// RecordPoolExhausted increments PoolExhaustedRejections for a given tenant.
// Called by pool_balance_check middleware whenever it returns HTTP 402 due to
// an exhausted credit pool. poolKind is "relay" for all current call-sites.
func RecordPoolExhausted(tenantID, poolKind string) {
	metrics.RecordPoolExhaustedRejection(tenantID, poolKind)
}

// RecordDebitSuccess observes the CNY amount of a successful WalletDebit.
// Called by DebitWalletGRPC (and its HTTP fallback) immediately after a
// confirmed debit response from lurus-platform.
func RecordDebitSuccess(tenantID string, amountCNY float64) {
	metrics.RecordBillingDebit(tenantID, amountCNY)
}

// RecordPoolNotConfigured increments CreditPoolNotConfiguredTotal for a given
// tenant/action. Called by pool_balance_check middleware when
// CREDIT_POOL_REQUIRED is "log" (bypass-but-counted) or "enforce" (402) and
// the tenant has no credit pool row at all.
func RecordPoolNotConfigured(tenantID, action string) {
	metrics.RecordPoolNotConfigured(tenantID, action)
}
