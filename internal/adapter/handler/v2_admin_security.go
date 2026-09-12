package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// v2_admin_security.go — L6 (2026-09-12): TOTP adoption stats for the
// security reviewer, and the audited admin escape hatch for a user who lost
// their device (and any backup codes) with no self-service recovery left.
// Both routes mount under adminRoute (RootJWTAuth + AuditWriteGuard,
// router/api-v2-router.go).

// notifyUserFn is a call seam so tests can spy on the notification without a
// working SMTP/webhook sink — mirrors relay.go's getChannelFn seam.
// Production code does not reassign it.
var notifyUserFn = app.NotifyUser

// GetAdminTotpStatsV2 reports TOTP adoption for the security reviewer:
// GET /api/v2/admin/security/totp-stats?tenant_id=
//
// backup_codes_exhausted/no_codes_issued split the enrolled population —
// see repo.TOTPAdoptionStats — with no backfill for users enrolled before
// backup codes existed (§8 O2/L6): they simply report no_codes_issued until
// they call the regenerate endpoint.
func GetAdminTotpStatsV2(c *gin.Context) {
	tenantID := c.Query("tenant_id")
	stats, err := repo.GetTOTPAdoptionStats(tenantID)
	if err != nil {
		common.SysError("GetAdminTotpStatsV2: aggregate failed: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to aggregate TOTP stats"})
		return
	}
	adoptionPct := 0.0
	if stats.TotalUsers > 0 {
		adoptionPct = float64(stats.Enrolled) / float64(stats.TotalUsers) * 100
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"enrolled":               stats.Enrolled,
			"pending":                stats.Pending,
			"total_users":            stats.TotalUsers,
			"adoption_pct":           adoptionPct,
			"backup_codes_exhausted": stats.Exhausted,
			"no_codes_issued":        stats.NoCodesIssued,
		},
	})
}

type totpForceDisableRequest struct {
	Reason string `json:"reason"`
}

// ForceDisableTotpV2 is the audited admin escape hatch:
// POST /api/v2/admin/security/users/:id/totp/force-disable
//
// Mounted behind RootJWTAuth + SecureVerificationRequired on the route
// (router/api-v2-router.go) — the acting root must have stepped up in
// THEIR OWN session, not merely present a Bearer JWT: RootJWTAuth's
// Bearer-JWT branch (admin_jwt_auth.go) never sets the "id" context key
// SecureVerificationRequired reads, so a pure Bearer-JWT caller 401s on
// "未登录" before ever reaching this handler (mitigates a stolen root JWT
// alone stripping a user's 2FA — see the routing comment in
// router/api-v2-router.go and TestAdminTotpForceDisable_BearerJWTRoot401 in
// the middleware package).
func ForceDisableTotpV2(c *gin.Context) {
	targetID, err := strconv.Atoi(c.Param("id"))
	if err != nil || targetID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid user id"})
		return
	}
	var req totpForceDisableRequest
	_ = c.ShouldBindJSON(&req)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" || len(req.Reason) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "reason is required (max 200 chars)"})
		return
	}

	rec, err := repo.GetUserTOTP(targetID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if rec == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "user has no TOTP enrollment"})
		return
	}

	if err := repo.DeleteUserTOTP(targetID); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := repo.DeleteUserTOTPBackupCodes(targetID); err != nil {
		common.ApiError(c, err)
		return
	}

	actorID := c.GetInt("id")
	detail := `{"reason":` + strconv.Quote(req.Reason) + `}`
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTotpAdminDisabled, governance.ResourceUser, targetID, detail))

	target := &repo.User{Id: targetID}
	if ferr := target.FillUserById(); ferr == nil {
		notify := dto.NewNotify("totp_admin_disabled", "两步验证已被管理员关闭",
			"您的账户两步验证已被管理员关闭。如非本人操作，请立即联系管理员。", nil)
		if notifyErr := notifyUserFn(c.Request.Context(), targetID, target.Email, target.GetSetting(), notify); notifyErr != nil {
			common.SysLog("ForceDisableTotpV2: notify target failed: " + notifyErr.Error())
		}
	} else {
		common.SysLog("ForceDisableTotpV2: could not load target user to notify: " + ferr.Error())
	}

	repo.RecordLog(targetID, repo.LogTypeSystem, "两步验证已被管理员关闭")

	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}
