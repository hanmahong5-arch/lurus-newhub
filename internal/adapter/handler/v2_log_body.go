package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Opt-in body archive (migration 052): the tenant's consent toggle and the
// read of an archived prompt/response. Both tenant routes sit behind UserAuth +
// TenantSlugGuard and use tenantSelectionScope, so the tenant is always the
// caller's own (never a path or body value) and a tenant admin is required.

type sedimentationConsentRequest struct {
	Consent *bool `json:"consent"`
}

func sedimentationView(tenantID string) (gin.H, error) {
	on, err := repo.GetTenantSedimentationConsent(tenantID)
	if err != nil {
		return nil, err
	}
	return gin.H{"consent": on}, nil
}

// GetSedimentationConsentV2 handles GET /:tenant_slug/data-policy/sedimentation.
func GetSedimentationConsentV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	data, err := sedimentationView(tenantID)
	if err != nil {
		common.SysError("sedimentation consent read failed: " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to read consent")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// PutSedimentationConsentV2 handles PUT /:tenant_slug/data-policy/sedimentation.
// Turning consent on does not by itself archive anything: the tenant's
// effective content retention must also be "full" (the GET carries no such
// hint on purpose - that is a different setting with its own endpoint).
func PutSedimentationConsentV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	var req sedimentationConsentRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Consent == nil {
		dpFail(c, http.StatusBadRequest, "INVALID_REQUEST", "consent (boolean) is required")
		return
	}
	prev, _ := repo.GetTenantSedimentationConsent(tenantID)
	if err := repo.SetTenantSedimentationConsent(tenantID, *req.Consent); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			dpFail(c, http.StatusNotFound, "TENANT_NOT_FOUND", "Tenant not found")
			return
		}
		common.SysError("sedimentation consent save failed: " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to save consent")
		return
	}
	// Withdrawal deletes what was kept, right now and in this request: the
	// switch's promise is "nothing of ours is held", not "nothing new is
	// added". The flag is already off, so a purge that fails halfway leaves
	// the rest to the cleanup sweep (DeleteUnconsentedLogBodies) and is
	// reported rather than hidden.
	var purged int64
	purgePending := false
	if !*req.Consent {
		var err error
		purged, err = repo.PurgeTenantLogBodies(c.Request.Context(), tenantID, 0, 0)
		if err != nil {
			purgePending = true
			common.SysError("sedimentation consent withdrawal: purge failed, sweep will finish: " + err.Error())
		}
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionSedimentationConsentSet, governance.ResourceDataPolicy, 0,
		fmt.Sprintf(`{"tenant_id":%q,"from":%t,"to":%t,"purged_bodies":%d,"purge_pending":%t}`,
			tenantID, prev, *req.Consent, purged, purgePending)))
	data, _ := sedimentationView(tenantID)
	if !*req.Consent {
		data["purged_bodies"] = purged
		data["purge_pending"] = purgePending
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// logBodyView is the wire shape of an archived body.
func logBodyView(b *repo.LogBody) gin.H {
	return gin.H{
		"request_id":        b.RequestId,
		"tenant_id":         b.TenantId,
		"user_id":           b.UserId,
		"token_id":          b.TokenId,
		"model":             b.Model,
		"created_at":        b.CreatedAt,
		"expires_at":        b.ExpiresAt,
		"request_body":      b.RequestBody,
		"response_text":     b.ResponseText,
		"response_captured": b.ResponseCaptured,
		"truncated":         b.Truncated,
	}
}

func writeLogBody(c *gin.Context, b *repo.LogBody, err error) {
	if err != nil {
		if errors.Is(err, repo.ErrLogBodyNotFound) {
			// One answer for missing, expired and foreign-tenant: the reply
			// must not reveal that another tenant owns this request id.
			dpFail(c, http.StatusNotFound, "LOG_BODY_NOT_FOUND", "Log body not found")
			return
		}
		common.SysError("log body read failed: " + err.Error())
		dpFail(c, http.StatusInternalServerError, "INTERNAL", "Failed to read log body")
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, dpActor(c),
		governance.ActionLogBodyRead, governance.ResourceLogBody, 0,
		fmt.Sprintf(`{"tenant_id":%q,"request_id":%q}`, b.TenantId, b.RequestId)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": logBodyView(b)})
}

// GetLogBodyV2 handles GET /:tenant_slug/logs/:request_id/body. Reading raw
// prompts is a tenant-admin act, and every successful read is audited.
func GetLogBodyV2(c *gin.Context) {
	tenantID, ok := tenantSelectionScope(c)
	if !ok {
		return
	}
	b, err := repo.GetLogBody(tenantID, c.Param("request_id"))
	writeLogBody(c, b, err)
}

// GetAdminLogBodyV2 handles GET /admin/logs/:request_id/body (RootJWTAuth):
// the platform-staff read across tenants. Same shape, same audit.
func GetAdminLogBodyV2(c *gin.Context) {
	b, err := repo.GetLogBodyAnyTenant(c.Param("request_id"))
	writeLogBody(c, b, err)
}
