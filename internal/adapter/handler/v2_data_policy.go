package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/contentpolicy"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Tenant-admin relay data control (migration 050): log retention for the
// tenant and its tokens, and the tenant's content rules. Every route sits
// behind UserAuth + TenantSlugGuard; the tenant is always the caller's own
// (tenantSelectionScope), never a path or body value.

func dpFail(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"success": false, "error_code": code, "message": msg})
}

func dpActor(c *gin.Context) int {
	id, _ := repo.GetUserID(c)
	return id
}

type retentionRequest struct {
	ContentRetention *string `json:"content_retention"`
}

func (r retentionRequest) mode() (contentpolicy.RetentionMode, bool) {
	if r.ContentRetention == nil || !contentpolicy.ValidRetention(*r.ContentRetention) {
		return "", false
	}
	return contentpolicy.RetentionMode(*r.ContentRetention), true
}

func retentionView(tenantID string) (gin.H, error) {
	tenant, err := repo.GetTenantContentRetention(tenantID)
	if err != nil {
		return nil, err
	}
	platform := contentpolicy.PlatformDefault()
	return gin.H{
		"platform_default": string(platform),
		"tenant":           string(tenant),
		"effective":        string(contentpolicy.Resolve(platform, tenant, contentpolicy.RetentionInherit)),
	}, nil
}

// GetContentRetentionV2 handles GET /:tenant_slug/data-policy/retention.
func GetContentRetentionV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	data, err := retentionView(tenantID)
	if err != nil {
		common.SysError("content retention read failed: " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to read retention setting")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// PutContentRetentionV2 handles PUT /:tenant_slug/data-policy/retention.
// The tenant may tighten past the platform default but never loosen below it.
func PutContentRetentionV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	var req retentionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "content_retention is required")
		return
	}
	mode, valid := req.mode()
	if !valid {
		dpFail(c, http.StatusBadRequest, "INVALID_RETENTION", "content_retention must be one of: \"\", full, metadata_only, none")
		return
	}
	if contentpolicy.Looser(mode, contentpolicy.PlatformDefault()) {
		dpFail(c, http.StatusBadRequest, "RETENTION_CANNOT_LOOSEN",
			"content_retention cannot be looser than the platform default ("+string(contentpolicy.PlatformDefault())+")")
		return
	}
	prev, _ := repo.GetTenantContentRetention(tenantID)
	if err := repo.SetTenantContentRetention(tenantID, mode); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dpFail(c, http.StatusNotFound, "TENANT_NOT_FOUND", "Tenant not found")
			return
		}
		common.SysError("content retention save failed: " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to save retention setting")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionContentRetentionSet, governance.ResourceDataPolicy, 0,
		fmt.Sprintf(`{"scope":"tenant","tenant_id":%q,"from":%q,"to":%q}`, tenantID, prev, mode)))
	data, _ := retentionView(tenantID)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// PutTokenContentRetentionV2 handles
// PUT /:tenant_slug/data-policy/tokens/:id/retention. A token may only be set
// at least as strict as its tenant's effective mode; a looser value is refused
// (and would be ignored at write time anyway, the strictest layer wins).
func PutTokenContentRetentionV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenID <= 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid token id")
		return
	}
	var req retentionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "content_retention is required")
		return
	}
	mode, valid := req.mode()
	if !valid {
		dpFail(c, http.StatusBadRequest, "INVALID_RETENTION", "content_retention must be one of: \"\", full, metadata_only, none")
		return
	}
	tenantMode, _ := repo.GetTenantContentRetention(tenantID)
	floor := contentpolicy.Resolve(contentpolicy.PlatformDefault(), tenantMode, contentpolicy.RetentionInherit)
	if contentpolicy.Looser(mode, floor) {
		dpFail(c, http.StatusBadRequest, "RETENTION_CANNOT_LOOSEN",
			"a token cannot be looser than its tenant's effective retention ("+string(floor)+")")
		return
	}
	if err := repo.SetTokenContentRetention(tokenID, tenantID, mode); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dpFail(c, http.StatusNotFound, "TOKEN_NOT_FOUND", "Token not found")
			return
		}
		common.SysError("token content retention save failed: " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to save retention setting")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionContentRetentionSet, governance.ResourceToken, tokenID,
		fmt.Sprintf(`{"scope":"token","tenant_id":%q,"to":%q}`, tenantID, mode)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"token_id": tokenID, "content_retention": string(mode),
		"effective": string(contentpolicy.Resolve(contentpolicy.PlatformDefault(), tenantMode, mode)),
	}})
}

// ---- content rules ----------------------------------------------------

type contentRuleRequest struct {
	Ordinal     int64  `json:"ordinal"`
	Name        string `json:"name"`
	RoleScope   string `json:"role_scope"`
	Kind        string `json:"kind"`
	PatternType string `json:"pattern_type"`
	Builtin     string `json:"builtin"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Mode        string `json:"mode"`
	Enabled     *bool  `json:"enabled"`
}

func (r contentRuleRequest) apply(dst *repo.ContentRule) {
	dst.Ordinal, dst.Name, dst.RoleScope, dst.Kind = r.Ordinal, r.Name, r.RoleScope, r.Kind
	dst.PatternType, dst.Builtin, dst.Pattern, dst.Replacement, dst.Mode =
		r.PatternType, r.Builtin, r.Pattern, r.Replacement, r.Mode
	dst.Enabled = r.Enabled == nil || *r.Enabled
	if dst.RoleScope == "" {
		dst.RoleScope = contentpolicy.RoleAny
	}
	if dst.Mode == "" {
		dst.Mode = contentpolicy.ModeObserve
	}
}

// ruleStatus maps store errors onto HTTP replies.
func ruleStatus(c *gin.Context, err error, what string) {
	switch {
	case errors.Is(err, contentpolicy.ErrInvalidRule):
		dpFail(c, http.StatusBadRequest, "INVALID_RULE", err.Error())
	case errors.Is(err, repo.ErrContentRuleLimit):
		dpFail(c, http.StatusConflict, "RULE_LIMIT", err.Error())
	case errors.Is(err, repo.ErrContentRuleNotFound):
		dpFail(c, http.StatusNotFound, "RULE_NOT_FOUND", "Content rule not found")
	default:
		common.SysError(what + ": " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to "+what)
	}
}

func listRulesFor(c *gin.Context, scope, tenantID string) {
	rules, err := repo.ListContentRules(scope, tenantID)
	if err != nil {
		ruleStatus(c, err, "list content rules")
		return
	}
	if rules == nil {
		rules = []repo.ContentRule{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"rules": rules, "builtins": contentpolicy.BuiltinNames(),
		"max_rules": contentpolicy.MaxRulesPerScope, "max_pattern_len": contentpolicy.MaxPatternLen,
	}})
}

func createRuleFor(c *gin.Context, scope, tenantID string) {
	var req contentRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid rule body")
		return
	}
	r := &repo.ContentRule{Scope: scope, TenantId: tenantID, CreatedBy: int64(dpActor(c))}
	req.apply(r)
	if err := repo.CreateContentRule(r); err != nil {
		ruleStatus(c, err, "create content rule")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionContentRuleCreated, governance.ResourceContentRule, int(r.Id), ruleAuditDetails(r)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": r})
}

func updateRuleFor(c *gin.Context, scope, tenantID string) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid rule id")
		return
	}
	var req contentRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid rule body")
		return
	}
	r, err := repo.GetContentRule(id, scope, tenantID)
	if err != nil {
		ruleStatus(c, err, "load content rule")
		return
	}
	req.apply(r)
	if err := repo.SaveContentRule(r); err != nil {
		ruleStatus(c, err, "update content rule")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionContentRuleUpdated, governance.ResourceContentRule, int(r.Id), ruleAuditDetails(r)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": r})
}

func deleteRuleFor(c *gin.Context, scope, tenantID string) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid rule id")
		return
	}
	if err := repo.DeleteContentRule(id, scope, tenantID); err != nil {
		ruleStatus(c, err, "delete content rule")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionContentRuleDeleted, governance.ResourceContentRule, int(id),
		fmt.Sprintf(`{"scope":%q,"tenant_id":%q}`, scope, tenantID)))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func ruleAuditDetails(r *repo.ContentRule) string {
	return fmt.Sprintf(`{"scope":%q,"tenant_id":%q,"name":%q,"kind":%q,"mode":%q,"pattern_type":%q,"builtin":%q,"role_scope":%q,"enabled":%t}`,
		r.Scope, r.TenantId, r.Name, r.Kind, r.Mode, r.PatternType, r.Builtin, r.RoleScope, r.Enabled)
}

// ListContentRulesV2 handles GET /:tenant_slug/data-policy/rules.
func ListContentRulesV2(c *gin.Context) {
	if tenantID, ok := tenantSelectionScope(c); ok {
		listRulesFor(c, contentpolicy.ScopeTenant, tenantID)
	}
}

// CreateContentRuleV2 handles POST /:tenant_slug/data-policy/rules.
func CreateContentRuleV2(c *gin.Context) {
	if tenantID, ok := tenantSelectionScope(c); ok {
		createRuleFor(c, contentpolicy.ScopeTenant, tenantID)
	}
}

// UpdateContentRuleV2 handles PUT /:tenant_slug/data-policy/rules/:id.
func UpdateContentRuleV2(c *gin.Context) {
	if tenantID, ok := tenantSelectionScope(c); ok {
		updateRuleFor(c, contentpolicy.ScopeTenant, tenantID)
	}
}

// DeleteContentRuleV2 handles DELETE /:tenant_slug/data-policy/rules/:id.
func DeleteContentRuleV2(c *gin.Context) {
	if tenantID, ok := tenantSelectionScope(c); ok {
		deleteRuleFor(c, contentpolicy.ScopeTenant, tenantID)
	}
}

// Platform rules: /api/v2/admin/content-rules, behind RootJWTAuth. The scope
// and the empty tenant are fixed here, so a tenant admin has no route that
// can write a platform rule.

// ListPlatformContentRulesV2 handles GET /admin/content-rules.
func ListPlatformContentRulesV2(c *gin.Context) {
	listRulesFor(c, contentpolicy.ScopePlatform, "")
}

// CreatePlatformContentRuleV2 handles POST /admin/content-rules.
func CreatePlatformContentRuleV2(c *gin.Context) {
	createRuleFor(c, contentpolicy.ScopePlatform, "")
}

// UpdatePlatformContentRuleV2 handles PUT /admin/content-rules/:id.
func UpdatePlatformContentRuleV2(c *gin.Context) {
	updateRuleFor(c, contentpolicy.ScopePlatform, "")
}

// DeletePlatformContentRuleV2 handles DELETE /admin/content-rules/:id.
func DeletePlatformContentRuleV2(c *gin.Context) {
	deleteRuleFor(c, contentpolicy.ScopePlatform, "")
}
