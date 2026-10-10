package repo

import (
	"errors"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"gorm.io/gorm"
)

// This file holds the persistence side of the relay data-control feature
// (migration 050): content rules, per-tenant / per-token log retention and the
// single log-write chokepoint that enforces retention.

// ContentRule is the stored form of contentpolicy.Rule (table content_rules).
//
// Dual-creation like 049: migration 050 and AutoMigrate agree on column types
// and the index name, so either order is a no-op for the other.
type ContentRule struct {
	Id          int64  `json:"id" gorm:"primaryKey"`
	Scope       string `json:"scope" gorm:"type:varchar(16);not null;default:'tenant';index:idx_content_rules_scope_tenant,priority:1"`
	TenantId    string `json:"tenant_id" gorm:"type:varchar(36);not null;default:'';index:idx_content_rules_scope_tenant,priority:2"`
	Ordinal     int64  `json:"ordinal" gorm:"not null;default:0;index:idx_content_rules_scope_tenant,priority:3"`
	Name        string `json:"name" gorm:"type:varchar(64);not null;default:''"`
	RoleScope   string `json:"role_scope" gorm:"type:varchar(16);not null;default:'any'"`
	Kind        string `json:"kind" gorm:"type:varchar(16);not null;default:'mask'"`
	PatternType string `json:"pattern_type" gorm:"type:varchar(16);not null;default:'builtin'"`
	Builtin     string `json:"builtin" gorm:"type:varchar(32);not null;default:''"`
	Pattern     string `json:"pattern" gorm:"type:varchar(512);not null;default:''"`
	Replacement string `json:"replacement" gorm:"type:varchar(64);not null;default:''"`
	Mode        string `json:"mode" gorm:"type:varchar(16);not null;default:'observe'"`
	Enabled     bool   `json:"enabled" gorm:"not null;default:true"`
	CreatedBy   int64  `json:"created_by" gorm:"not null;default:0"`
	CreatedAt   int64  `json:"created_at" gorm:"not null;default:0"`
	UpdatedAt   int64  `json:"updated_at" gorm:"not null;default:0"`
}

func (ContentRule) TableName() string { return "content_rules" }

// ToRule converts to the engine shape.
func (r *ContentRule) ToRule() contentpolicy.Rule {
	return contentpolicy.Rule{
		Id: r.Id, Scope: r.Scope, TenantId: r.TenantId, Ordinal: r.Ordinal, Name: r.Name,
		RoleScope: r.RoleScope, Kind: r.Kind, PatternType: r.PatternType, Builtin: r.Builtin,
		Pattern: r.Pattern, Replacement: r.Replacement, Mode: r.Mode, Enabled: r.Enabled,
	}
}

// Sentinel errors of the content-rule store.
var (
	ErrContentRuleNotFound = errors.New("content rule not found")
	ErrContentRuleLimit    = errors.New("content rule limit reached for this scope")
)

// ListContentRules lists the rules of one scope (tenantID "" for platform).
func ListContentRules(scope, tenantID string) ([]ContentRule, error) {
	var out []ContentRule
	err := DB.Where("scope = ? AND tenant_id = ?", scope, tenantID).
		Order("ordinal asc, id asc").Find(&out).Error
	return out, err
}

// CreateContentRule validates and stores a rule, enforcing the per-scope
// count limit.
func CreateContentRule(r *ContentRule) error {
	if err := contentpolicy.ValidateRule(rulePtr(r)); err != nil {
		return err
	}
	var n int64
	if err := DB.Model(&ContentRule{}).Where("scope = ? AND tenant_id = ?", r.Scope, r.TenantId).Count(&n).Error; err != nil {
		return err
	}
	if n >= contentpolicy.MaxRulesPerScope {
		return ErrContentRuleLimit
	}
	now := common.GetTimestamp()
	r.Id, r.CreatedAt, r.UpdatedAt = 0, now, now
	if err := DB.Create(r).Error; err != nil {
		return err
	}
	InvalidateContentRulesCache()
	return nil
}

func rulePtr(r *ContentRule) *contentpolicy.Rule {
	v := r.ToRule()
	return &v
}

// GetContentRule loads a rule confined to its scope/tenant, so a tenant can
// never read or touch another tenant's (or a platform) rule by id.
func GetContentRule(id int64, scope, tenantID string) (*ContentRule, error) {
	var r ContentRule
	err := DB.Where("id = ? AND scope = ? AND tenant_id = ?", id, scope, tenantID).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrContentRuleNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SaveContentRule persists an edited rule (loaded through GetContentRule) after
// re-validating it. Scope and tenant are immutable.
func SaveContentRule(r *ContentRule) error {
	if err := contentpolicy.ValidateRule(rulePtr(r)); err != nil {
		return err
	}
	r.UpdatedAt = common.GetTimestamp()
	res := DB.Model(&ContentRule{}).Where("id = ? AND scope = ? AND tenant_id = ?", r.Id, r.Scope, r.TenantId).
		Select("ordinal", "name", "role_scope", "kind", "pattern_type", "builtin", "pattern",
			"replacement", "mode", "enabled", "updated_at").Updates(r)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrContentRuleNotFound
	}
	InvalidateContentRulesCache()
	return nil
}

// DeleteContentRule removes a rule confined to its scope/tenant.
func DeleteContentRule(id int64, scope, tenantID string) error {
	res := DB.Where("id = ? AND scope = ? AND tenant_id = ?", id, scope, tenantID).Delete(&ContentRule{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrContentRuleNotFound
	}
	InvalidateContentRulesCache()
	return nil
}

// ---- compiled ruleset cache ------------------------------------------

const policyCacheTTL = 15 * time.Second

// errContentRulesNoDB: ContentRulesetForTenant was asked before the database existed.
var errContentRulesNoDB = errors.New("content rules: database not initialised")

type rulesetEntry struct {
	rs  *contentpolicy.Ruleset
	exp time.Time
}

var (
	rulesetCache sync.Map // tenantID -> rulesetEntry
	// rulesetNow is the clock of the caches (tests substitute it).
	rulesetNow = time.Now
)

// InvalidateContentRulesCache drops every compiled ruleset on this replica.
// Other replicas converge within policyCacheTTL.
func InvalidateContentRulesCache() { rulesetCache = sync.Map{} }

// ContentRulesetForTenant returns the compiled platform+tenant rules for a
// tenant (nil when there are none). A load failure keeps serving the stale
// ruleset when one exists, otherwise returns the error so the caller can
// decide (the relay entry fails open and logs, see handler.applyContentRules).
func ContentRulesetForTenant(tenantID string) (*contentpolicy.Ruleset, error) {
	now := rulesetNow()
	if v, ok := rulesetCache.Load(tenantID); ok {
		if e := v.(rulesetEntry); now.Before(e.exp) {
			return e.rs, nil
		}
	}
	// The relay entry calls this on every request; it must never panic. No
	// database (boot not finished, or a hermetic test) is "rules unavailable",
	// which the caller logs and fails open on, exactly like a query error.
	if DB == nil {
		if v, ok := rulesetCache.Load(tenantID); ok {
			return v.(rulesetEntry).rs, nil
		}
		return nil, errContentRulesNoDB
	}
	var rows []ContentRule
	err := DB.Where("enabled = ? AND ((scope = ? AND tenant_id = ?) OR (scope = ? AND tenant_id = ?))",
		true, contentpolicy.ScopePlatform, "", contentpolicy.ScopeTenant, tenantID).
		Order("ordinal asc, id asc").Find(&rows).Error
	if err != nil {
		if v, ok := rulesetCache.Load(tenantID); ok {
			return v.(rulesetEntry).rs, nil
		}
		return nil, err
	}
	var rules []contentpolicy.Rule
	for i := range rows {
		rules = append(rules, rows[i].ToRule())
	}
	var rs *contentpolicy.Ruleset
	if len(rules) > 0 {
		var errs []error
		rs, errs = contentpolicy.Compile(rules)
		for _, e := range errs {
			common.SysError("content rule skipped: " + e.Error())
		}
		if rs.Len() == 0 {
			rs = nil
		}
	}
	rulesetCache.Store(tenantID, rulesetEntry{rs: rs, exp: now.Add(policyCacheTTL)})
	return rs, nil
}

// ---- retention settings ----------------------------------------------

type retentionEntry struct {
	mode contentpolicy.RetentionMode
	exp  time.Time
}

var retentionCache sync.Map // "t:<id>" | "k:<id>" -> retentionEntry

// InvalidateContentRetentionCache drops the cached tenant/token settings on
// this replica; other replicas converge within policyCacheTTL.
func InvalidateContentRetentionCache() { retentionCache = sync.Map{} }

func lookupRetention(key string, load func() (string, error)) contentpolicy.RetentionMode {
	now := rulesetNow()
	cached, hasCached := retentionCache.Load(key)
	if hasCached {
		if e := cached.(retentionEntry); now.Before(e.exp) {
			return e.mode
		}
	}
	v, err := load()
	if err != nil {
		// A failed lookup keeps the last known decision (a tightened setting
		// must not evaporate because the DB blinked); with no history there is
		// nothing to enforce and the row is written as before.
		if hasCached {
			return cached.(retentionEntry).mode
		}
		return contentpolicy.RetentionInherit
	}
	mode := contentpolicy.RetentionMode(v)
	if !contentpolicy.ValidRetention(v) {
		mode = contentpolicy.RetentionInherit
	}
	retentionCache.Store(key, retentionEntry{mode: mode, exp: now.Add(policyCacheTTL)})
	return mode
}

// GetTenantContentRetention reads the tenant's own setting (” = inherit).
func GetTenantContentRetention(tenantID string) (contentpolicy.RetentionMode, error) {
	var v string
	err := DB.Model(&Tenant{}).Where("id = ?", tenantID).Select("content_retention").Scan(&v).Error
	return contentpolicy.RetentionMode(v), err
}

// GetTokenContentRetention reads the token's own setting (” = inherit).
func GetTokenContentRetention(tokenID int) (contentpolicy.RetentionMode, error) {
	var v string
	err := WithoutTenantIsolation(DB).Model(&Token{}).Where("id = ?", tokenID).Select("content_retention").Scan(&v).Error
	return contentpolicy.RetentionMode(v), err
}

// SetTenantContentRetention stores the tenant setting.
func SetTenantContentRetention(tenantID string, mode contentpolicy.RetentionMode) error {
	if !contentpolicy.ValidRetention(string(mode)) {
		return contentpolicy.ErrInvalidRule
	}
	res := DB.Model(&Tenant{}).Where("id = ?", tenantID).Update("content_retention", string(mode))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	InvalidateContentRetentionCache()
	return nil
}

// SetTokenContentRetention stores the token setting, confined to the tenant
// that owns the token.
func SetTokenContentRetention(tokenID int, tenantID string, mode contentpolicy.RetentionMode) error {
	if !contentpolicy.ValidRetention(string(mode)) {
		return contentpolicy.ErrInvalidRule
	}
	res := WithoutTenantIsolation(DB).Model(&Token{}).Where("id = ? AND tenant_id = ?", tokenID, tenantID).
		Update("content_retention", string(mode))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	InvalidateContentRetentionCache()
	return nil
}

// EffectiveContentRetention folds platform default, tenant and token into the
// mode a log row must obey. This is the ONE resolver every log write uses.
func EffectiveContentRetention(tenantID string, tokenID int) contentpolicy.RetentionMode {
	tenant := contentpolicy.RetentionInherit
	if tenantID != "" {
		tenant = lookupRetention("t:"+tenantID, func() (string, error) {
			m, err := GetTenantContentRetention(tenantID)
			return string(m), err
		})
	}
	token := contentpolicy.RetentionInherit
	if tokenID > 0 {
		token = lookupRetention("k:"+itoa(tokenID), func() (string, error) {
			m, err := GetTokenContentRetention(tokenID)
			return string(m), err
		})
	}
	return contentpolicy.Resolve(contentpolicy.PlatformDefault(), tenant, token)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// applyContentRetention is the single chokepoint through which every consume
// and error log row passes just before it is inserted. Because the decision is
// resolved here, from stored settings, and not carried on the request
// context, no call site can forget it and no later layer can overwrite a
// token-level decision (the failure mode behind bifrost PR 7478, where the
// per-key choice was bypassed by the logging plugin).
func applyContentRetention(l *Log) *Log {
	if l == nil {
		return nil
	}
	mode := EffectiveContentRetention(l.TenantId, l.TokenId)
	if mode == contentpolicy.RetentionFull {
		return l
	}
	rec := contentpolicy.LogRecord{
		Content: l.Content, Ip: l.Ip, RequestFingerprint: l.RequestFingerprint,
	}
	if l.Other != "" {
		rec.Other, _ = decodeOtherMap(l.Other)
	}
	contentpolicy.ApplyRetention(mode, &rec)
	l.Content, l.Ip, l.RequestFingerprint = rec.Content, rec.Ip, rec.RequestFingerprint
	if l.Other != "" {
		l.Other = common.MapToJsonStr(rec.Other)
	}
	return l
}

func decodeOtherMap(s string) (map[string]interface{}, error) {
	m := map[string]interface{}{}
	if err := common.UnmarshalJsonStr(s, &m); err != nil {
		return nil, err
	}
	return m, nil
}
