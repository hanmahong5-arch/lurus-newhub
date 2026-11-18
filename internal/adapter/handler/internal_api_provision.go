package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
)

var resolveAccountByZitadelSub = common.GetAccountByZitadelSubGRPC

type internalProvisionRequest struct {
	IDPSubject         string `json:"idp_subject"`
	ZitadelSub         string `json:"zitadel_sub"`
	Email              string `json:"email" binding:"required"`
	DisplayName        string `json:"display_name"`
	TenantID           string `json:"tenant_id"`
	Group              string `json:"group"`
	InitialQuota       int    `json:"initial_quota"`
	CreateInitialToken bool   `json:"create_initial_token"`
	InitialTokenName   string `json:"initial_token_name"`
}

// InternalProvisionUser atomically creates a user, identity mapping, and optional initial API token.
func InternalProvisionUser(c *gin.Context) {
	var req internalProvisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error()})
		return
	}

	if req.IDPSubject != "" {
		req.ZitadelSub = req.IDPSubject
	}
	if req.ZitadelSub == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Missing OIDC subject: provide idp_subject (preferred) or zitadel_sub",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}
	if !strings.Contains(req.Email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid email format",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}

	tenantId := req.TenantID
	if tenantId == "" {
		tenantId = "default"
	}

	linkedAccountID := resolveLinkedPlatformAccount(c, req.ZitadelSub)

	existingUser, existingMapping, err := repo.GetUserByIDPSubject(req.ZitadelSub, tenantId)
	if err == nil && existingUser != nil {
		healPlatformAccountLink(existingUser, linkedAccountID)
		respondExistingProvisionedUser(c, existingUser, existingMapping, tenantId)
		return
	}

	group := req.Group
	if group == "" {
		group = "default"
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = strings.Split(req.Email, "@")[0]
	}

	tokenName := req.InitialTokenName
	if tokenName == "" {
		tokenName = "Default Key"
	}
	var tokenKey string
	if req.CreateInitialToken {
		tokenKey, err = app.GenerateTokenKey()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to generate token key: " + err.Error()})
			return
		}
	}

	res, txErr := repo.ProvisionTenantUserTx(repo.ProvisionTenantUserParams{
		SanitizedUsername:  sanitizeProvisionUsername(req.ZitadelSub),
		TenantID:           tenantId,
		IDPSubject:         req.ZitadelSub,
		Email:              req.Email,
		DisplayName:        displayName,
		Group:              group,
		InitialQuota:       req.InitialQuota,
		LinkedAccountID:    linkedAccountID,
		CreateInitialToken: req.CreateInitialToken,
		InitialTokenName:   tokenName,
		InitialTokenKey:    tokenKey,
	})
	if txErr != nil {
		handleProvisionTxFailure(c, res, txErr, req.ZitadelSub, tenantId, linkedAccountID)
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("Internal API provisioned user %d (zitadel_sub=%s) via key: %s", res.User.Id, req.ZitadelSub, keyName))

	resp := gin.H{
		"user_id":     res.User.Id,
		"username":    res.User.Username,
		"email":       req.Email,
		"group":       group,
		"tenant_id":   tenantId,
		"is_existing": false,
		"mapping_id":  res.Mapping.Id,
	}
	if res.Token != nil {
		resp["token"] = gin.H{
			"id":      res.Token.Id,
			"key":     tokenKey,
			"name":    tokenName,
			"warning": "Please save this key - it will not be shown again.",
		}
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": resp})
}

func handleProvisionTxFailure(c *gin.Context, res *repo.ProvisionTenantUserResult, txErr error, sub, tenantId string, linkedAccountID int64) {
	stage := ""
	if res != nil {
		stage = res.Stage
	}
	if stage == "user" {
		if winner, winnerMapping := provisionRaceWinner(sub, tenantId, linkedAccountID); winner != nil {
			common.SysLog(fmt.Sprintf("provision: lost create race for zitadel_sub=%s, returning winner user %d", sub, winner.Id))
			respondExistingProvisionedUser(c, winner, winnerMapping, tenantId)
			return
		}
	}
	msgPrefix := map[string]string{
		"begin":   "Failed to begin transaction: ",
		"user":    "Failed to create user: ",
		"mapping": "Failed to create identity mapping: ",
		"token":   "Failed to create initial token: ",
		"commit":  "Failed to commit transaction: ",
	}[stage]
	if msgPrefix == "" {
		msgPrefix = "Failed to provision user: "
	}
	c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": msgPrefix + txErr.Error()})
}

func resolveLinkedPlatformAccount(c *gin.Context, sub string) int64 {
	acct, err := resolveAccountByZitadelSub(c.Request.Context(), sub)
	if err != nil {
		common.SysLog(fmt.Sprintf("provision: account resolve errored for zitadel_sub=%s — creating UNLINKED user (fail-open): %v", sub, err))
		return 0
	}
	if acct == nil || acct.ID <= 0 {
		common.SysLog(fmt.Sprintf("provision: no platform account for zitadel_sub=%s yet — creating UNLINKED user (fail-open), will self-heal on re-provision", sub))
		return 0
	}
	return acct.ID
}

func healPlatformAccountLink(existingUser *repo.User, linkedAccountID int64) {
	if linkedAccountID <= 0 || existingUser.LurusAccountID != nil {
		return
	}
	if healErr := repo.LinkUserPlatformAccount(existingUser.Id, linkedAccountID, true); healErr != nil {
		common.SysLog(fmt.Sprintf("provision: self-heal link failed for user %d (account=%d): %v", existingUser.Id, linkedAccountID, healErr))
	} else {
		common.SysLog(fmt.Sprintf("provision: self-healed link for user %d → account %d", existingUser.Id, linkedAccountID))
	}
}

func sanitizeProvisionUsername(sub string) string {
	username := "u_" + sub
	if len(username) > 20 {
		username = username[:20]
	}
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, username)
	if sanitized == "" {
		return "u_provisioned"
	}
	return sanitized
}

func respondExistingProvisionedUser(c *gin.Context, user *repo.User, mapping *repo.UserIdentityMapping, tenantId string) {
	var mappingID int
	if mapping != nil {
		mappingID = mapping.Id
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":     user.Id,
			"username":    user.Username,
			"email":       user.Email,
			"group":       user.Group,
			"tenant_id":   tenantId,
			"is_existing": true,
			"mapping_id":  mappingID,
		},
	})
}

func provisionRaceWinner(zitadelSub, tenantID string, accountID int64) (*repo.User, *repo.UserIdentityMapping) {
	if accountID > 0 {
		if u, err := repo.GetUserByLurusAccountID(accountID); err == nil && u != nil && u.TenantId == tenantID {
			_, m, _ := repo.GetUserByIDPSubject(zitadelSub, tenantID)
			return u, m
		}
	}
	if u, m, err := repo.GetUserByIDPSubject(zitadelSub, tenantID); err == nil && u != nil {
		return u, m
	}
	return nil, nil
}
