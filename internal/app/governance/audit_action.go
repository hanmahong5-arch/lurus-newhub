package governance

import "sort"

// Audit action taxonomy.
//
// Phase E3 (Enterprise Foundation): every security-relevant mutation should
// produce one of the constants below. The full set is grouped by domain so
// new actions can be added next to peers. Action strings are stable identifiers
// recorded in audit_events.action — never rename in place; mark old strings
// deprecated and add a new constant if semantics shift.
//
// The registry below intentionally includes some actions that are not yet
// wired to a handler. Defining the constant first lets downstream PRs add
// the RecordAuditEvent call without bouncing through another taxonomy change.
const (
	// Authentication and session lifecycle.
	ActionAuthFailed         = "auth.failed"
	ActionAuthIPRejected     = "auth.ip_rejected"
	ActionAuthScopeRejected  = "auth.scope_rejected" // Phase E2: token lacks scope for the relay path
	ActionAuthBootstrapped   = "auth.bootstrapped"
	ActionAuthLoginSuccess   = "auth.login_success"
	ActionAuthLogout         = "auth.logout"
	ActionAuthSessionRevoked = "auth.session_revoked"
	ActionAuthOIDCLinked     = "auth.oidc_linked"
	ActionAuthOIDCUnlinked   = "auth.oidc_unlinked"
	ActionAuthTokenRotated   = "auth.token_rotated"

	// Token CRUD (relay key lifecycle).
	ActionTokenCreated       = "token.created"
	ActionTokenUpdated       = "token.updated"
	ActionTokenDeleted       = "token.deleted"
	ActionTokenBatchDeleted  = "token.batch_deleted"
	ActionTokenStatusChanged = "token.status_changed"

	// Channel CRUD (upstream provider config).
	ActionChannelCreated      = "channel.created"
	ActionChannelUpdated      = "channel.updated"
	ActionChannelDeleted      = "channel.deleted"
	ActionChannelBatchDeleted = "channel.batch_deleted"
	ActionChannelDisabled     = "channel.disabled"
	ActionChannelEnabled      = "channel.enabled"
	ActionChannelTagDisabled  = "channel.tag_disabled"
	ActionChannelTested       = "channel.tested"

	// User lifecycle and admin operations on users.
	ActionUserCreated       = "user.created"
	ActionUserUpdated       = "user.updated"
	ActionUserDeleted       = "user.deleted"
	ActionUserRoleChanged   = "user.role_changed"
	ActionUserBanned        = "user.banned"
	ActionUserUnbanned      = "user.unbanned"
	ActionUserSelfUpdated   = "user.self_updated"
	ActionUserQuotaAdjusted = "user.quota_adjusted"

	// Redemption codes (兑换码).
	ActionRedemptionCreated        = "redemption.created"
	ActionRedemptionUpdated        = "redemption.updated"
	ActionRedemptionDeleted        = "redemption.deleted"
	ActionRedemptionRedeemed       = "redemption.redeemed"
	ActionRedemptionInvalidDeleted = "redemption.invalid_deleted"

	// System options (global config keys).
	ActionOptionUpdated = "option.updated"

	// Model sync and registry.
	ActionModelSyncTriggered = "model.sync_triggered"

	// Tenant administration.
	ActionTenantCreated        = "tenant.created"
	ActionTenantUpdated        = "tenant.updated"
	ActionTenantDeleted        = "tenant.deleted"
	ActionTenantMappingDeleted = "tenant.mapping_deleted"
	ActionTenantBrandUpdated   = "tenant.brand_updated"

	// Internal API key tenant whitelist (internal_api_key_tenants — migration
	// 013/021 §1). Granting/revoking changes which tenants a narrow-scope
	// internal key may reach, so both directions are audited.
	ActionInternalKeyTenantGranted = "internal_key.tenant_granted"
	ActionInternalKeyTenantRevoked = "internal_key.tenant_revoked"

	// Security incidents.
	ActionSensitiveBlocked      = "security.sensitive_blocked"
	ActionWhitelabelKeyAccessed = "security.whitelabel_key_accessed"

	// Billing events. Mirror gRPC platform-side WalletDebit / WalletCredit /
	// quota consumption so a single audit trail is searchable end-to-end.
	ActionBillingDebit         = "billing.debit"
	ActionBillingCredit        = "billing.credit"
	ActionBillingQuotaConsumed = "billing.quota_consumed"
	// ActionBillingQuotaThreshold fires when a user's used_quota crosses one of
	// the configured percentage rungs (default 50/80/95/100). Phase E4: emitted
	// alongside the llm.quota.threshold NATS publish so the audit trail records
	// the crossing even when NATS dispatch fails.
	ActionBillingQuotaThreshold = "billing.quota_threshold"

	// System lifecycle.
	ActionSystemStartup  = "system.startup"
	ActionSystemShutdown = "system.shutdown"
)

// Actor type constants — who performed the action.
const (
	ActorUser   = "user"
	ActorAdmin  = "admin"
	ActorSystem = "system"
	ActorToken  = "token"
)

// Resource type constants — which entity was acted on.
const (
	ResourceToken       = "token"
	ResourceChannel     = "channel"
	ResourceUser        = "user"
	ResourceRedemption  = "redemption"
	ResourceOption      = "option"
	ResourceModel       = "model"
	ResourceTenant      = "tenant"
	ResourceSystem      = "system"
	ResourceInternalKey = "internal_key"
)

// validAuditActions is the canonical registry, used by IsValidAuditAction.
// New actions must be added here AND to one of the const groups above; the
// validator is the single source of truth that the export endpoint and any
// future schema-checking client can rely on.
var validAuditActions = map[string]struct{}{
	ActionAuthFailed:               {},
	ActionAuthIPRejected:           {},
	ActionAuthScopeRejected:        {},
	ActionAuthBootstrapped:         {},
	ActionAuthLoginSuccess:         {},
	ActionAuthLogout:               {},
	ActionAuthSessionRevoked:       {},
	ActionAuthOIDCLinked:           {},
	ActionAuthOIDCUnlinked:         {},
	ActionAuthTokenRotated:         {},
	ActionTokenCreated:             {},
	ActionTokenUpdated:             {},
	ActionTokenDeleted:             {},
	ActionTokenBatchDeleted:        {},
	ActionTokenStatusChanged:       {},
	ActionChannelCreated:           {},
	ActionChannelUpdated:           {},
	ActionChannelDeleted:           {},
	ActionChannelBatchDeleted:      {},
	ActionChannelDisabled:          {},
	ActionChannelEnabled:           {},
	ActionChannelTagDisabled:       {},
	ActionChannelTested:            {},
	ActionUserCreated:              {},
	ActionUserUpdated:              {},
	ActionUserDeleted:              {},
	ActionUserRoleChanged:          {},
	ActionUserBanned:               {},
	ActionUserUnbanned:             {},
	ActionUserSelfUpdated:          {},
	ActionUserQuotaAdjusted:        {},
	ActionRedemptionCreated:        {},
	ActionRedemptionUpdated:        {},
	ActionRedemptionDeleted:        {},
	ActionRedemptionRedeemed:       {},
	ActionRedemptionInvalidDeleted: {},
	ActionOptionUpdated:            {},
	ActionModelSyncTriggered:       {},
	ActionTenantCreated:            {},
	ActionTenantUpdated:            {},
	ActionTenantDeleted:            {},
	ActionTenantMappingDeleted:     {},
	ActionTenantBrandUpdated:       {},
	ActionInternalKeyTenantGranted: {},
	ActionInternalKeyTenantRevoked: {},
	ActionSensitiveBlocked:         {},
	ActionWhitelabelKeyAccessed:    {},
	ActionBillingDebit:             {},
	ActionBillingCredit:            {},
	ActionBillingQuotaConsumed:     {},
	ActionBillingQuotaThreshold:    {},
	ActionSystemStartup:            {},
	ActionSystemShutdown:           {},
}

// IsValidAuditAction reports whether action is in the canonical taxonomy.
// The audit export endpoint uses this to filter unknown values; handlers
// don't need to call it because they pass typed constants from the block above.
func IsValidAuditAction(action string) bool {
	_, ok := validAuditActions[action]
	return ok
}

// AllAuditActions returns the canonical action list, sorted alphabetically by
// action string. Useful for export tooling and the v2 admin discovery endpoint
// when surfacing the taxonomy to operators.
func AllAuditActions() []string {
	out := make([]string, 0, len(validAuditActions))
	for k := range validAuditActions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
