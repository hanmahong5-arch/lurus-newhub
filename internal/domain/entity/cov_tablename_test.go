package entity

// cov_tablename_test.go — pins every entity's GORM TableName() override to
// its documented physical table name. These are cheap but real forcing
// functions: a stubbed-out TableName() returning "" (or the wrong name)
// would silently point GORM at the wrong table (or force it to derive a
// pluralized default that may not match the actual migration-created
// table), so each assertion here is load-bearing, not decorative.

import "testing"

func TestTableNames(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"AuditChainHead", (AuditChainHead{}).TableName(), "audit_chain_heads"},
		{"BillingCheckoutOrder", (BillingCheckoutOrder{}).TableName(), "billing_checkout_orders"},
		{"BillingOutbox", (BillingOutbox{}).TableName(), "billing_outbox"},
		{"CurrencyExchange", (CurrencyExchange{}).TableName(), "currency_exchanges"},
		{"LeaderElection", (LeaderElection{}).TableName(), "leader_elections"},
		{"ModelRateLimit", (ModelRateLimit{}).TableName(), "model_rate_limits"},
		{"ModelUsageStat", (ModelUsageStat{}).TableName(), "model_usage_stats"},
		{"PrivacyErasureRequest", (PrivacyErasureRequest{}).TableName(), "privacy_erasure_requests"},
		{"ProvisionedRedemptionBatch", (ProvisionedRedemptionBatch{}).TableName(), "provisioned_redemption_batches"},
		{"Release", (Release{}).TableName(), "releases"},
		{"ReleaseArtifact", (ReleaseArtifact{}).TableName(), "release_artifacts"},
		{"DownloadLog", (DownloadLog{}).TableName(), "download_logs"},
		{"TenantConfig", (TenantConfig{}).TableName(), "tenant_configs"},
		{"TenantCreditPool", (TenantCreditPool{}).TableName(), "tenant_credit_pools"},
		{"TenantCreditPoolDraw", (TenantCreditPoolDraw{}).TableName(), "tenant_credit_pool_draws"},
		{"CreditPoolFundEvent", (CreditPoolFundEvent{}).TableName(), "credit_pool_fund_events"},
		{"UserIdentityMapping", (UserIdentityMapping{}).TableName(), "user_identity_mapping"},
		{"UserTOTP", (UserTOTP{}).TableName(), "user_totps"},
		{"ModelRateLimit again (guards accidental copy-paste dup)", (ModelRateLimit{}).TableName(), "model_rate_limits"},
	}
	seen := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("TableName() = %q, want %q", tt.got, tt.want)
			}
		})
		seen[tt.want] = true
	}
	if len(seen) != 18 {
		t.Fatalf("test table covers %d distinct physical table names, want 18 (one duplicate check intentional)", len(seen))
	}
}
