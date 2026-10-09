package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// v2_token_issue.go — enterprise key issuing (docs/plans/enterprise-hub §2.4):
// employee_ref / trusted_identity_headers validation shared by token create and
// update, and the roster endpoint POST /:tenant_slug/tokens/batch.

const (
	// maxBatchCreateTokens bounds one roster upload (single transaction).
	maxBatchCreateTokens = 500

	tokenErrCodeEmployeeRefInvalid   = "TOKEN_EMPLOYEE_REF_INVALID"
	tokenErrCodeEmployeeRefConflict  = "TOKEN_EMPLOYEE_REF_CONFLICT"
	tokenErrCodeTrustedAdminOnly     = "TOKEN_TRUSTED_HEADERS_ADMIN_ONLY"
	tokenErrCodeEmployeeRefAdminOnly = "TOKEN_EMPLOYEE_REF_ADMIN_ONLY"
	tokenErrCodeBatchInvalid         = "TOKEN_BATCH_INVALID"
	tokenErrCodeBatchTooLarge        = "TOKEN_BATCH_TOO_LARGE"
	tokenErrCodeBatchAdminOnly       = "TOKEN_BATCH_ADMIN_ONLY"
)

// batchTokenKeyGen is the key source for the roster endpoint; tests swap it to
// force a mid-batch failure.
var batchTokenKeyGen = app.GenerateTokenKey

// validEmployeeRefText mirrors the relay-side rule for X-Lurus-Employee:
// 1..64 characters of [A-Za-z0-9._@:-].
func validEmployeeRefText(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		case b == '.' || b == '_' || b == '@' || b == ':' || b == '-':
		default:
			return false
		}
	}
	return true
}

func respondEmployeeRefConflict(c *gin.Context) {
	c.JSON(http.StatusConflict, gin.H{
		"success":    false,
		"message":    "employee_ref is already used by another key in this tenant",
		"error_code": tokenErrCodeEmployeeRefConflict,
	})
}

// requireEmployeeRefAdmin gates every write of employee_ref to tenant admins.
// The ref is the attribution identity usage and bills are grouped by (design
// D6: attribution trusts only admin-controlled sources); if any member could set
// it, a member could squat a roster id before the admin issues it and have that
// employee's spend billed to themselves.
func requireEmployeeRefAdmin(c *gin.Context, tc *middleware.TenantContext) bool {
	if requireTenantAdmin(c, tc) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{
		"success":    false,
		"message":    "Only a tenant admin can set or change a key's employee_ref",
		"error_code": tokenErrCodeEmployeeRefAdminOnly,
	})
	return false
}

// checkEnterpriseTokenFields enforces the enterprise fields of a token write and
// replies on failure. wantTrusted is "this write turns trusted_identity_headers
// ON" and is tenant-admin only; ref (when non-empty) must be well-formed and
// unused by another live key of the tenant (exceptID = the key being edited).
func checkEnterpriseTokenFields(c *gin.Context, tc *middleware.TenantContext, ref string, exceptID int, wantTrusted bool) bool {
	if wantTrusted && !requireTenantAdmin(c, tc) {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "Only a tenant admin can mark a key as a trusted gateway key",
			"error_code": tokenErrCodeTrustedAdminOnly,
		})
		return false
	}
	if ref != "" && !requireEmployeeRefAdmin(c, tc) {
		return false
	}
	if ref == "" {
		return true
	}
	if !validEmployeeRefText(ref) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "employee_ref must be 1-64 characters of [A-Za-z0-9._@:-]",
			"error_code": tokenErrCodeEmployeeRefInvalid,
		})
		return false
	}
	taken, err := repo.TokenEmployeeRefTaken(tc.TenantID, ref, exceptID)
	if err != nil {
		common.SysError("employee_ref uniqueness check: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to validate employee_ref"})
		return false
	}
	if taken {
		respondEmployeeRefConflict(c)
		return false
	}
	return true
}

type batchTokenItem struct {
	Name           string `json:"name"`
	EmployeeRef    string `json:"employee_ref"`
	ProjectId      int    `json:"project_id"`
	RemainQuota    int    `json:"remain_quota"`
	UnlimitedQuota bool   `json:"unlimited_quota"`
	ModelLimits    string `json:"model_limits"`
}

func batchInvalid(c *gin.Context, index int, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"success":    false,
		"message":    fmt.Sprintf("tokens[%d]: %s", index, msg),
		"error_code": tokenErrCodeBatchInvalid,
	})
}

// BatchCreateTokensV2 issues one key per employee of a roster.
// Route: POST /api/v2/:tenant_slug/tokens/batch
// Body:  { tokens: [{name, employee_ref, project_id, remain_quota, unlimited_quota, model_limits}] }
//
// Tenant-admin only. At most 500 items, all-or-nothing in ONE transaction.
// Idempotent per employee_ref: an employee that already has a live key in the
// tenant is reported under "skipped" and not re-issued, so re-uploading a roster
// is safe. The plaintext keys of the newly created tokens appear in this
// response only. The keys are owned by the calling admin (the tenant's payer
// identity), never by the employee, and cannot be trusted-gateway keys.
func BatchCreateTokensV2(c *gin.Context) {
	tc, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	if !requireTenantAdmin(c, tc) {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "Admin role required",
			"error_code": tokenErrCodeBatchAdminOnly,
		})
		return
	}
	var req struct {
		Tokens []batchTokenItem `json:"tokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request parameters", "error": err.Error()})
		return
	}
	if len(req.Tokens) == 0 {
		batchInvalid(c, 0, "at least one token is required")
		return
	}
	if len(req.Tokens) > maxBatchCreateTokens {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    fmt.Sprintf("Too many tokens: %d (max %d per batch)", len(req.Tokens), maxBatchCreateTokens),
			"error_code": tokenErrCodeBatchTooLarge,
		})
		return
	}

	// Validate everything before touching the database: one bad row rejects the
	// whole upload, so the caller never has to reason about a half-applied roster.
	seen := make(map[string]int, len(req.Tokens))
	projects := map[int]bool{}
	refs := make([]string, 0, len(req.Tokens))
	for i := range req.Tokens {
		it := &req.Tokens[i]
		if !validEmployeeRefText(it.EmployeeRef) {
			batchInvalid(c, i, "employee_ref must be 1-64 characters of [A-Za-z0-9._@:-]")
			return
		}
		if first, dup := seen[it.EmployeeRef]; dup {
			batchInvalid(c, i, fmt.Sprintf("duplicate employee_ref (same as tokens[%d])", first))
			return
		}
		seen[it.EmployeeRef] = i
		refs = append(refs, it.EmployeeRef)
		if it.Name == "" {
			it.Name = it.EmployeeRef
		}
		if err := app.ValidateTokenName(it.Name); err != nil {
			batchInvalid(c, i, err.Error())
			return
		}
		if err := app.ValidateTokenQuota(it.RemainQuota, it.UnlimitedQuota); err != nil {
			batchInvalid(c, i, err.Error())
			return
		}
		if err := app.ValidateTokenModelLimits(it.ModelLimits, it.ModelLimits != ""); err != nil {
			batchInvalid(c, i, err.Error())
			return
		}
		if it.ProjectId < 0 {
			batchInvalid(c, i, "project_id must not be negative")
			return
		}
		if it.ProjectId > 0 {
			projects[it.ProjectId] = true
		}
	}
	for pid := range projects {
		if _, err := repo.GetProjectByID(tc.TenantID, pid); err != nil {
			batchInvalid(c, indexOfProject(req.Tokens, pid), "project not found in this tenant")
			return
		}
	}

	existing, err := repo.ExistingEmployeeRefs(tc.TenantID, refs)
	if err != nil {
		common.SysError("batch tokens: existing lookup: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create tokens"})
		return
	}

	identityAccountID := repo.IdentityAccountIDForUser(tc.UserID)
	now := common.GetTimestamp()
	var toCreate []*repo.Token
	skipped := make([]gin.H, 0)
	for _, it := range req.Tokens {
		if ex, ok := existing[it.EmployeeRef]; ok {
			skipped = append(skipped, gin.H{"employee_ref": it.EmployeeRef, "id": ex.Id, "name": ex.Name})
			continue
		}
		key, err := batchTokenKeyGen()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
			return
		}
		toCreate = append(toCreate, &repo.Token{
			UserId:             tc.UserID,
			TenantId:           tc.TenantID,
			Name:               it.Name,
			Key:                key,
			CreatedTime:        now,
			AccessedTime:       now,
			ExpiredTime:        -1,
			RemainQuota:        it.RemainQuota,
			UnlimitedQuota:     it.UnlimitedQuota,
			ModelLimitsEnabled: it.ModelLimits != "",
			ModelLimits:        it.ModelLimits,
			Group:              "default",
			ProjectId:          it.ProjectId,
			IdentityAccountID:  identityAccountID,
			EmployeeRef:        it.EmployeeRef,
		})
	}

	if len(toCreate) > 0 {
		if err := repo.CreateTokensBatch(tc.TenantID, toCreate); err != nil {
			if repo.IsUniqueViolation(err) {
				// Only a concurrent roster upload can have taken one of OUR refs
				// since the idempotency lookup above; any other unique hit is a
				// key collision and must not be reported as an employee_ref clash.
				newRefs := make([]string, 0, len(toCreate))
				for _, t := range toCreate {
					newRefs = append(newRefs, t.EmployeeRef)
				}
				if taken, lerr := repo.ExistingEmployeeRefs(tc.TenantID, newRefs); lerr == nil && len(taken) > 0 {
					respondEmployeeRefConflict(c)
					return
				}
			}
			common.SysError("batch tokens: insert: " + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create tokens"})
			return
		}
	}

	created := make([]gin.H, 0, len(toCreate))
	createdRefs := make([]string, 0, len(toCreate))
	for _, t := range toCreate {
		created = append(created, gin.H{
			"id": t.Id, "name": t.Name, "employee_ref": t.EmployeeRef,
			"project_id": t.ProjectId, "key": "sk-" + t.Key,
		})
		createdRefs = append(createdRefs, t.EmployeeRef)
	}
	sort.Strings(createdRefs)
	detail, _ := json.Marshal(map[string]interface{}{
		"requested": len(req.Tokens), "created": len(toCreate), "skipped": len(skipped), "employee_refs": createdRefs,
	})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tc.UserID,
		governance.ActionTokenBatchCreated, governance.ResourceToken, 0, string(detail)))

	status := http.StatusCreated
	if len(toCreate) == 0 {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{
		"success": true,
		"data": gin.H{
			"created":   created,
			"skipped":   skipped,
			"requested": len(req.Tokens),
		},
	})
}

func indexOfProject(items []batchTokenItem, pid int) int {
	for i, it := range items {
		if it.ProjectId == pid {
			return i
		}
	}
	return 0
}

// Moved verbatim from v2_token.go to keep that file under the source-size ratchet.
// maxBatchDeleteTokens bounds a single batch-delete request. Above this the
// request is rejected rather than sweeping an unbounded set in one transaction.
const maxBatchDeleteTokens = 100

// DeleteTokensV2 batch-deletes the caller's own tokens (v2 API with tenant context).
// Route: POST /api/v2/:tenant_slug/tokens/batch-delete  Body: { "ids": [int] }
//
// Ownership is enforced inside repo.BatchDeleteTokens (the delete is scoped to
// tenantCtx.UserID), so ids belonging to other users are silently ignored and
// never deleted. An empty list is a no-op (the UI may submit an empty selection).
func DeleteTokensV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Tenant context not found",
		})
		return
	}

	var req struct {
		Ids []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request parameters",
			"error":   err.Error(),
		})
		return
	}

	// Empty selection deletes nothing — succeed with deleted:0 rather than error.
	if len(req.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "deleted": 0})
		return
	}

	if len(req.Ids) > maxBatchDeleteTokens {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": fmt.Sprintf("Too many ids: %d (max %d per batch)", len(req.Ids), maxBatchDeleteTokens),
		})
		return
	}

	deleted, err := repo.BatchDeleteTokens(req.Ids, tenantCtx.UserID)
	if err != nil {
		common.SysError("Failed to batch-delete tokens: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete tokens",
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, tenantCtx.UserID,
		governance.ActionTokenDeleted, governance.ResourceToken, 0,
		fmt.Sprintf(`{"action":"batch_delete","count":%d}`, deleted)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"deleted": deleted,
	})
}
