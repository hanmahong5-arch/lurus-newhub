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
	// ActionAuthTotpBackupRegenerated is recorded by
	// POST /api/user/totp/backup-codes/regenerate — the previous unused
	// codes are invalidated in the same call, so this marks a security
	// event worth a trail even though it moves no privilege (self-service,
	// gated behind SecureVerificationRequired like TOTP disable).
	ActionAuthTotpBackupRegenerated = "auth.totp_backup_regenerated"
	// ActionTotpAdminDisabled is recorded for POST
	// /api/v2/admin/security/users/:id/totp/force-disable — a root operator
	// stripping another user's 2FA. Distinct from ActionUserSelfUpdated
	// (the user's own TOTP disable) because the actor and target differ and
	// this is the escape hatch for a lost device, not routine self-service.
	ActionTotpAdminDisabled = "auth.totp_admin_disabled"
	// ActionAuthStepUpNoCredential is recorded by UniversalVerify
	// (secure_verification.go, L3, cycle 9) on every pass through the
	// no-TOTP-enrollment branch — method "session" accepted with nothing
	// checked beyond an already-authenticated session, no credential of any
	// kind presented. Handed to the audit writer regardless of
	// SECURE_VERIFICATION_REQUIRE_ENROLLMENT (the flag only controls
	// whether the grant happens at all, not whether it's audited once it
	// does), so this grant stays auditable even while the flag that would
	// refuse it instead stays off. The write itself goes through
	// governance.RecordAuditEvent, which is a best-effort background
	// insert: dropped silently if no writer is registered, logged (not
	// retried) if the insert fails. See that flag's doc in .env.example for
	// why the default is off.
	ActionAuthStepUpNoCredential = "auth.stepup_without_credential"

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
	// ActionChannelSensitiveWriteRefused is recorded by
	// internal/adapter/handler/channel_sensitive_write.go's
	// enforceChannelSensitiveWrite (L2, cycle 9, auth-security-17/18
	// follow-up) when a non-root admin's channel write touches a sensitive
	// field (key, base_url, param_override, header_override, or the
	// per-channel proxy setting) without an active channel:sensitive_write
	// grant. Fires on refusal only — a write that is allowed to proceed
	// already produces ActionChannelCreated/ActionChannelUpdated; this
	// event exists so the DENIED attempt is visible too, across all four
	// entry points (v1 add/update, v2 create/update).
	ActionChannelSensitiveWriteRefused = "channel.sensitive_write_refused"

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

	// Cost-attribution projects (migration 029). Tenant-admin maintained
	// labels: renaming or deleting one silently re-shapes every spend report
	// the tenant reads, so the change belongs in the audit trail even though
	// a project grants no privilege.
	ActionProjectCreated = "project.created"
	ActionProjectUpdated = "project.updated"
	ActionProjectDeleted = "project.deleted"
	// ActionProjectRestored is the undo of ActionProjectDeleted. It is a
	// separate action rather than a "created" event so the audit trail shows
	// the mistake AND its correction instead of an unexplained resurrection.
	ActionProjectRestored = "project.restored"

	// System options (global config keys).
	ActionOptionUpdated = "option.updated"
	// ActionPricingUpdated is queued asynchronously via gopool.Go
	// (governance.RecordAuditEvent) after a committed
	// POST /api/v2/:tenant_slug/pricing (v2_pricing_write.go). Pricing moves
	// live billing ratios that apply regardless of which tenant's slug the
	// request used, so it gets its own action — distinct from the generic
	// ActionOptionUpdated — carrying from_version/to_version and the
	// per-field diff in Details. Not covered: the legacy PUT /api/option/
	// (ModelRatio|CompletionRatio|ModelPrice|CacheRatio) and POST
	// /api/option/rest_model_ratio routes rewrite the same maps without
	// producing this action or bumping PricingVersion.
	ActionPricingUpdated = "pricing.updated"

	// Model sync and registry.
	ActionModelSyncTriggered = "model.sync_triggered"
	// ActionModelCreated / ActionModelDeleted cover POST/DELETE
	// /api/v2/:tenant_slug/models(/:id) — root-gated catalog writes outside
	// /api/v2/admin (the catalog has no tenant_id; requirePlatformRoot is
	// enforced inside the handler, same shape as pricing's tenant-slug
	// route). L2 audit-completeness named these as an in-scope gap: the
	// model catalog and its pricing are process-global, so an unaudited
	// write here is as consequential as one under /admin.
	ActionModelCreated = "model.created"
	ActionModelDeleted = "model.deleted"

	// Tenant administration.
	ActionTenantCreated        = "tenant.created"
	ActionTenantUpdated        = "tenant.updated"
	ActionTenantDeleted        = "tenant.deleted"
	ActionTenantMappingDeleted = "tenant.mapping_deleted"
	ActionTenantBrandUpdated   = "tenant.brand_updated"
	// Tenant invite codes (migration 032, N2) — root-issued one-time codes
	// that route a first-time zita-bridge login into a specific tenant.
	ActionTenantInviteIssued   = "tenant.invite_issued"
	ActionTenantInviteConsumed = "tenant.invite_consumed"
	ActionTenantInviteRevoked  = "tenant.invite_revoked"

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
	// ActionBillingPoolReset is recorded for every tenant credit pool the
	// scheduled reset pass actually refilled (CREDIT_POOL_RESET_MODE=enforce
	// only — observe mode never writes this). Details carry {pool_id, delta,
	// next_reset_at}.
	ActionBillingPoolReset = "billing.pool_reset"
	// ActionBillingPoolThreshold is recorded whenever the pool-threshold
	// publisher (internal/pkg/nats/pool_threshold.go) actually fires — i.e.
	// its own schema+Redis dedup let this crossing through — regardless of
	// whether NATS itself is enabled (delivery is "nats" or "recorded_only").
	// Details carry {pool_id, balance, max_balance, threshold_pct, delivery}.
	ActionBillingPoolThreshold = "billing.pool_threshold"

	// System lifecycle.
	ActionSystemStartup  = "system.startup"
	ActionSystemShutdown = "system.shutdown"

	// ActionAdminWriteUnaudited is recorded by middleware.AuditWriteGuard —
	// not by a handler — when an admin/internal-admin mutating request (POST/
	// PUT/PATCH/DELETE under /api/v2/admin or /internal/admin) completes
	// without the handler ever calling governance.RecordAuditEvent. It fires
	// regardless of the response status the handler wrote, including
	// rejected (non-2xx) writes (TestAuditWriteGuard_FallbackRowWhenHandlerSilent
	// covers 201, _FiresOnRejectedWrite covers 403): a write that failed is
	// still a write attempt and must not vanish from the audit trail just
	// because it didn't succeed. Details carry {"route","method","status"},
	// the matched route's path parameters under "params" when present, and,
	// when the caller is an OIDC-JWT root admin (RootJWTAuth's Bearer branch
	// never populates the "id" context key — a pre-existing gap, not fixed
	// by this action), "admin_sub" too.
	ActionAdminWriteUnaudited = "admin.write_unaudited"

	// Reseller tenant credit pools (ADR 2026-05-18 §4.1). Creating, topping
	// up, or deleting a pool moves or gates a real wallet-backed balance, so
	// each gets its own action distinct from the generic tenant/option
	// events — these are the routes L2's audit-completeness gap named
	// explicitly as "money routes, not swept".
	ActionCreditPoolCreated  = "credit_pool.created"
	ActionCreditPoolToppedUp = "credit_pool.topped_up"
	ActionCreditPoolDeleted  = "credit_pool.deleted"
	// ActionCreditPoolFunded is recorded by InternalFundCreditPool (POST
	// /internal/v1/provisioning/tenants/:slug/credit-pool/fund,
	// internal_credit_pool_fund.go) — the platform BillingOutbox supply
	// call, distinct from ActionCreditPoolToppedUp because the actor is
	// ActorSystem (the calling internal API key), not an admin operator, and
	// the caller is unauthenticated-by-session (X-API-Key with the platform internal key's balance:write
	// scope) rather than RootJWTAuth. Recorded only when the fund actually
	// moved the balance (fundErr == nil && !replayed) — an idempotent replay
	// changes nothing, so it produces no second row.
	ActionCreditPoolFunded = "credit_pool.funded"

	// ActionSwitchPresetCreated is recorded for every admin-authored Switch
	// config preset (POST /api/v2/admin/switch/presets) — a platform-wide
	// template every Switch install can pull, so who authored/changed it is
	// worth a trail even though it moves no money.
	ActionSwitchPresetCreated = "switch.preset_created"

	// ActionAdminMaintenanceTriggered is recorded for on-demand internal-admin
	// maintenance passes (POST /internal/admin/{backfill-token-accounts,
	// rotate-due-tokens,reset-due-pools}) — mirrors ActionModelSyncTriggered's
	// "someone kicked off a batch job" shape. Per-item detail (which token
	// got rotated, which pool got reset) is recorded separately by the job
	// itself when it also runs on the unattended schedule
	// (token_rotation.go, credit_pool_reset.go, both via
	// NewDetachedAuditEvent); this event is the "who/when triggered a manual
	// pass" record that AuditWriteGuard's untyped fallback would otherwise
	// have to fabricate.
	ActionAdminMaintenanceTriggered = "admin.maintenance_triggered"

	// ActionRoutingAffinityPurged is recorded by DELETE
	// /api/v2/admin/routing/affinity(/:key) (L5, routing-resilience-limits-11)
	// for both purge shapes — one binding by its HMAC key, or every binding
	// via ?all=true. Session-affinity bindings steer which upstream channel a
	// conversation's later turns land on, so an operator forcibly clearing one
	// is a routing-affecting action worth a trail, distinct from the generic
	// admin.write_unaudited fallback.
	ActionRoutingAffinityPurged = "routing.affinity_purged"

	// ActionPermissionGranted / ActionPermissionRevoked are recorded by the
	// two write handlers in v2_admin_authz.go (L4, auth-security-17/18,
	// console-ux-36) — a root operator delegating (or withdrawing) a
	// narrow root-gated permission to a non-root admin. The grant that
	// unlocks the audit feed is itself in the feed: a compliance reader
	// who gains audit:read access can see the exact row that gave it to
	// them.
	ActionPermissionGranted = "authz.permission_granted"
	ActionPermissionRevoked = "authz.permission_revoked"

	// ActionResponseRetrieved / ActionResponseDeleted / ActionResponseDenied
	// are recorded by relay_responses_registry.go (L7, cycle-8,
	// tasks-plugins-12) — GET/DELETE /v1/responses/:response_id, the
	// stateful half of the OpenAI Responses API. Retrieved/Deleted fire on
	// a successful pass-through to the vendor (the response body itself is
	// never in Details — only response_id/channel_id/upstream_status);
	// Denied fires instead of either when the row belongs to a different
	// user or tenant, so an operator can see who probed for whose response
	// id without the caller ever getting anything but a 404.
	ActionResponseRetrieved = "response.retrieved"
	ActionResponseDeleted   = "response.deleted"
	ActionResponseDenied    = "response.denied"
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
	ResourceProject     = "project"
	ResourceSystem      = "system"
	ResourceInternalKey = "internal_key"
	ResourcePricing     = "pricing"
	// ResourceRoute is used only by AuditWriteGuard's admin.write_unaudited
	// fallback event — the "resource" is the route itself, not a domain
	// entity, since the guard has no idea what the handler was mutating.
	ResourceRoute        = "route"
	ResourceCreditPool   = "credit_pool"
	ResourceSwitchPreset = "switch_preset"
	// ResourceSessionAffinity is used by ActionRoutingAffinityPurged — the
	// affinity binding is keyed by an HMAC string, not a numeric row id, so
	// ResourceID on that event is always 0; the key itself lives in Details.
	ResourceSessionAffinity = "session_affinity"
	// ResourceAuthz is used by ActionPermissionGranted/ActionPermissionRevoked
	// (L4) — the resource acted on is an admin_permission_grants row.
	ResourceAuthz = "authz"
	// ResourceResponse is used by ActionResponseRetrieved/Deleted/Denied
	// (L7) — the resource acted on is a response_registry row. ResourceID on
	// these events is always 0 (like ResourceSessionAffinity above): the row
	// is keyed by the vendor's string response_id, not a numeric id, and
	// that string is already in Details.
	ResourceResponse = "response"
)

// validAuditActions is the canonical registry, used by IsValidAuditAction.
// New actions must be added here AND to one of the const groups above; the
// validator is the single source of truth that the export endpoint and any
// future schema-checking client can rely on.
var validAuditActions = map[string]struct{}{
	ActionAuthFailed:                   {},
	ActionAuthIPRejected:               {},
	ActionAuthScopeRejected:            {},
	ActionAuthBootstrapped:             {},
	ActionAuthLoginSuccess:             {},
	ActionAuthLogout:                   {},
	ActionAuthSessionRevoked:           {},
	ActionAuthOIDCLinked:               {},
	ActionAuthOIDCUnlinked:             {},
	ActionAuthTokenRotated:             {},
	ActionAuthTotpBackupRegenerated:    {},
	ActionTotpAdminDisabled:            {},
	ActionAuthStepUpNoCredential:       {},
	ActionTokenCreated:                 {},
	ActionTokenUpdated:                 {},
	ActionTokenDeleted:                 {},
	ActionTokenBatchDeleted:            {},
	ActionTokenStatusChanged:           {},
	ActionChannelCreated:               {},
	ActionChannelUpdated:               {},
	ActionChannelDeleted:               {},
	ActionChannelBatchDeleted:          {},
	ActionChannelDisabled:              {},
	ActionChannelEnabled:               {},
	ActionChannelTagDisabled:           {},
	ActionChannelTested:                {},
	ActionChannelSensitiveWriteRefused: {},
	ActionUserCreated:                  {},
	ActionUserUpdated:                  {},
	ActionUserDeleted:                  {},
	ActionUserRoleChanged:              {},
	ActionUserBanned:                   {},
	ActionUserUnbanned:                 {},
	ActionUserSelfUpdated:              {},
	ActionUserQuotaAdjusted:            {},
	ActionRedemptionCreated:            {},
	ActionRedemptionUpdated:            {},
	ActionRedemptionDeleted:            {},
	ActionRedemptionRedeemed:           {},
	ActionRedemptionInvalidDeleted:     {},
	ActionProjectCreated:               {},
	ActionProjectUpdated:               {},
	ActionProjectDeleted:               {},
	ActionProjectRestored:              {},
	ActionOptionUpdated:                {},
	ActionPricingUpdated:               {},
	ActionModelSyncTriggered:           {},
	ActionModelCreated:                 {},
	ActionModelDeleted:                 {},
	ActionTenantCreated:                {},
	ActionTenantUpdated:                {},
	ActionTenantDeleted:                {},
	ActionTenantMappingDeleted:         {},
	ActionTenantBrandUpdated:           {},
	ActionTenantInviteIssued:           {},
	ActionTenantInviteConsumed:         {},
	ActionTenantInviteRevoked:          {},
	ActionInternalKeyTenantGranted:     {},
	ActionInternalKeyTenantRevoked:     {},
	ActionSensitiveBlocked:             {},
	ActionWhitelabelKeyAccessed:        {},
	ActionBillingDebit:                 {},
	ActionBillingCredit:                {},
	ActionBillingQuotaConsumed:         {},
	ActionBillingQuotaThreshold:        {},
	ActionBillingPoolReset:             {},
	ActionBillingPoolThreshold:         {},
	ActionSystemStartup:                {},
	ActionSystemShutdown:               {},
	ActionAdminWriteUnaudited:          {},
	ActionCreditPoolCreated:            {},
	ActionCreditPoolToppedUp:           {},
	ActionCreditPoolDeleted:            {},
	ActionCreditPoolFunded:             {},
	ActionSwitchPresetCreated:          {},
	ActionAdminMaintenanceTriggered:    {},
	ActionRoutingAffinityPurged:        {},
	ActionPermissionGranted:            {},
	ActionPermissionRevoked:            {},
	ActionResponseRetrieved:            {},
	ActionResponseDeleted:              {},
	ActionResponseDenied:               {},
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
