package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	entity "github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

type InternalApiKey = entity.InternalApiKey

// Re-export scope constants for existing callers
const (
	ScopeUserRead          = entity.ScopeUserRead
	ScopeUserWrite         = entity.ScopeUserWrite
	ScopeUserDelete        = entity.ScopeUserDelete
	ScopeSubscriptionRead  = entity.ScopeSubscriptionRead
	ScopeSubscriptionWrite = entity.ScopeSubscriptionWrite
	ScopeQuotaRead         = entity.ScopeQuotaRead
	ScopeQuotaWrite        = entity.ScopeQuotaWrite
	ScopeBalanceRead       = entity.ScopeBalanceRead
	ScopeBalanceWrite      = entity.ScopeBalanceWrite
	ScopeTokenRead         = entity.ScopeTokenRead
	ScopeTokenWrite        = entity.ScopeTokenWrite
	ScopeCurrencyRead      = entity.ScopeCurrencyRead
	ScopeCurrencyExchange  = entity.ScopeCurrencyExchange
	ScopeAuthLogin         = entity.ScopeAuthLogin
	ScopeLogRead           = entity.ScopeLogRead
	ScopeModelRead         = entity.ScopeModelRead
	ScopeProvisioning      = entity.ScopeProvisioning
	ScopeAdmin             = entity.ScopeAdmin
	ScopeAll               = entity.ScopeAll
)

// hashKey creates SHA256 hash of the API key
func hashKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// CreateInternalApiKey generates a new API key
func CreateInternalApiKey(name string, scopes []string, createdBy int, expiresAt int64, description string) (string, *InternalApiKey, error) {
	// Generate random key: lurus_ik_xxxxxxxxxxxxxxxxxxxx
	key := "lurus_ik_" + common.GetRandomString(32)
	keyHash := hashKey(key)
	keyPrefix := key[:16] // First 16 chars for display

	scopesJson, err := json.Marshal(scopes)
	if err != nil {
		return "", nil, err
	}

	apiKey := &InternalApiKey{
		Name:        name,
		KeyHash:     keyHash,
		KeyPrefix:   keyPrefix,
		Scopes:      string(scopesJson),
		CreatedBy:   createdBy,
		ExpiresAt:   expiresAt,
		Enabled:     true,
		Description: description,
	}

	err = DB.Create(apiKey).Error
	if err != nil {
		return "", nil, err
	}

	return key, apiKey, nil
}

// ValidateInternalApiKey validates key and returns the key object
func ValidateInternalApiKey(key string) (*InternalApiKey, error) {
	keyHash := hashKey(key)

	var apiKey InternalApiKey
	err := DB.Where("key_hash = ? AND enabled = ?", keyHash, true).First(&apiKey).Error
	if err != nil {
		return nil, err
	}

	// Check expiration
	if apiKey.ExpiresAt > 0 && apiKey.ExpiresAt < common.GetTimestamp() {
		return nil, errors.New("API key expired")
	}

	// Update last used (non-blocking)
	// Capture db reference to avoid nil dereference if DB is reassigned during tests
	db := DB
	go func() {
		if db != nil {
			db.Model(&apiKey).Update("last_used_at", common.GetTimestamp())
		}
	}()

	return &apiKey, nil
}

// GetAllInternalApiKeys returns all API keys
func GetAllInternalApiKeys() ([]*InternalApiKey, error) {
	var keys []*InternalApiKey
	err := DB.Order("id desc").Find(&keys).Error
	return keys, err
}

// GetInternalApiKeyById returns an API key by ID
func GetInternalApiKeyById(id int) (*InternalApiKey, error) {
	var key InternalApiKey
	err := DB.First(&key, id).Error
	return &key, err
}

// DeleteInternalApiKey deletes an API key
func DeleteInternalApiKey(id int) error {
	return DB.Delete(&InternalApiKey{}, id).Error
}

// ToggleInternalApiKey enables/disables an API key
func ToggleInternalApiKey(id int) error {
	var key InternalApiKey
	err := DB.First(&key, id).Error
	if err != nil {
		return err
	}
	return DB.Model(&key).Update("enabled", !key.Enabled).Error
}

// UpdateInternalApiKey updates an API key
func UpdateInternalApiKey(id int, name string, scopes []string, expiresAt int64, description string) error {
	scopesJson, err := json.Marshal(scopes)
	if err != nil {
		return err
	}

	return DB.Model(&InternalApiKey{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":        name,
		"scopes":      string(scopesJson),
		"expires_at":  expiresAt,
		"description": description,
	}).Error
}

// InternalKeyAllowedForTenant returns true if the given internal API key may
// issue / manage tokens for the given tenant.
//
// Authorisation has two tiers:
//
//  1. Platform-wide admin keys (ScopeAll = "*") bypass the whitelist. They
//     are already trusted to do anything cross-tenant; requiring a row per
//     tenant would only force operators to maintain a redundant table that
//     mirrors the tenants table.
//  2. Narrow-scope keys (typically ScopeProvisioning-only, e.g. a Reseller's
//     own integration key) must have an explicit (api_key_id, tenant_id)
//     row in internal_api_key_tenants. Empty whitelist for a narrow key →
//     deny (fail-closed).
//
// Phase 2 self-audit (2026-05-19) closed a cross-tenant Provisioning Create
// hole: any holder of a ScopeProvisioning key could create tokens for any
// tenant slug because creator_user_id was used only for attribution, never
// as a permission boundary. Migration 014 (Phase 3) will add a first-class
// tenant_id column on InternalApiKey, after which this whitelist table is
// expected to remain only for the rare multi-tenant Reseller key case.
//
// nil apiKey, missing id, or empty tenantID → deny. DB error → deny (the
// safer half of the trade-off, since the alternative is silent cross-tenant
// access during a Postgres blip).
func InternalKeyAllowedForTenant(apiKey *InternalApiKey, tenantID string) bool {
	if apiKey == nil || apiKey.Id <= 0 || tenantID == "" {
		return false
	}
	if apiKey.HasScope(ScopeAll) {
		return true
	}
	var count int64
	err := DB.Table("internal_api_key_tenants").
		Where("api_key_id = ? AND tenant_id = ?", apiKey.Id, tenantID).
		Count(&count).Error
	return err == nil && count > 0
}

// InternalKeyTenantGrant is one row of the internal_api_key_tenants whitelist
// table. No GORM struct backs that table (migration 013/021 §1 — code reads
// it via DB.Table, see InternalKeyAllowedForTenant above), so admin listing
// scans directly into this shape instead of a mapped entity.
type InternalKeyTenantGrant struct {
	TenantID  string    `json:"tenant_id"`
	CreatedAt time.Time `json:"created_at"`
}

// ListInternalKeyTenants returns the tenant whitelist for one internal API
// key, newest grant first. Empty slice (not an error) when the key has no
// whitelist rows — e.g. a ScopeAll key, which bypasses the whitelist entirely
// per InternalKeyAllowedForTenant and is never expected to have rows here.
func ListInternalKeyTenants(apiKeyID int) ([]InternalKeyTenantGrant, error) {
	var grants []InternalKeyTenantGrant
	err := DB.Table("internal_api_key_tenants").
		Where("api_key_id = ?", apiKeyID).
		Order("created_at DESC").
		Find(&grants).Error
	if err != nil {
		return nil, fmt.Errorf("list internal key tenants: %w", err)
	}
	return grants, nil
}

// GrantInternalKeyTenant whitelists tenantID for apiKeyID so
// InternalKeyAllowedForTenant starts admitting that key for that tenant.
// Idempotent: granting an already-whitelisted (api_key_id, tenant_id) pair —
// the table's PRIMARY KEY — is a no-op, not an error.
func GrantInternalKeyTenant(apiKeyID int, tenantID string) error {
	err := DB.Exec(
		`INSERT INTO internal_api_key_tenants (api_key_id, tenant_id) VALUES (?, ?)`,
		apiKeyID, tenantID,
	).Error
	if err != nil && !isUniqueViolation(err) {
		return fmt.Errorf("grant internal key tenant: %w", err)
	}
	return nil
}

// RevokeInternalKeyTenant removes tenantID from apiKeyID's whitelist.
// Idempotent: revoking an absent grant is a no-op, not an error — the caller
// only needs to distinguish "the key itself doesn't exist" (404, checked
// before calling this) from "nothing to revoke" (200).
func RevokeInternalKeyTenant(apiKeyID int, tenantID string) error {
	if err := DB.Exec(
		`DELETE FROM internal_api_key_tenants WHERE api_key_id = ? AND tenant_id = ?`,
		apiKeyID, tenantID,
	).Error; err != nil {
		return fmt.Errorf("revoke internal key tenant: %w", err)
	}
	return nil
}

// GetAvailableScopes returns all available scopes for UI
func GetAvailableScopes() []map[string]string {
	return []map[string]string{
		{"key": ScopeUserRead, "name": "Read User Info", "description": "Get user information by ID, email, or phone"},
		{"key": ScopeUserWrite, "name": "Write User Info", "description": "Update user information"},
		{"key": ScopeUserDelete, "name": "Delete User", "description": "Delete user accounts"},
		{"key": ScopeSubscriptionRead, "name": "Read Subscription", "description": "Get user subscription status"},
		{"key": ScopeSubscriptionWrite, "name": "Write Subscription", "description": "Grant or modify subscriptions"},
		{"key": ScopeQuotaRead, "name": "Read Quota", "description": "Get user quota information"},
		{"key": ScopeQuotaWrite, "name": "Write Quota", "description": "Adjust user quota"},
		{"key": ScopeBalanceRead, "name": "Read Balance", "description": "Get user balance"},
		{"key": ScopeBalanceWrite, "name": "Write Balance", "description": "Top up user balance"},
		{"key": ScopeTokenRead, "name": "Read Token", "description": "Get user tokens"},
		{"key": ScopeTokenWrite, "name": "Write Token", "description": "Create user tokens"},
		{"key": ScopeCurrencyRead, "name": "Read Currency", "description": "View exchange rates and model pricing in Lute"},
		{"key": ScopeCurrencyExchange, "name": "Currency Exchange", "description": "Exchange LuCoin to Lute for users"},
		{"key": ScopeLogRead, "name": "Read Logs", "description": "Query usage logs by user or token"},
		{"key": ScopeModelRead, "name": "Read Models", "description": "View model catalog and pricing"},
		{"key": ScopeAuthLogin, "name": "Auth Login", "description": "Authenticate users via login"},
		{"key": ScopeProvisioning, "name": "Provisioning", "description": "Reseller sub-tenant key issuance / revocation"},
		{"key": ScopeAdmin, "name": "Platform Admin", "description": "Platform admin operations (backfill jobs, etc.) under /internal/admin"},
		{"key": ScopeAll, "name": "All Permissions", "description": "Full access to all internal APIs"},
	}
}
