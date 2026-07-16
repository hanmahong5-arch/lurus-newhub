package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func GetAllTokens(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	tokens, err := repo.GetAllUserTokens(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := repo.CountUserTokens(userId)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(tokens)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchTokens(c *gin.Context) {
	userId := c.GetInt("id")
	keyword := c.Query("keyword")
	token := c.Query("token")
	tokens, err := repo.SearchUserTokens(userId, keyword, token)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    tokens,
	})
	return
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := repo.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    token,
	})
	return
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	token, err := repo.GetTokenByIds(tokenId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	tokenKey := parts[1]

	token, err := repo.GetTokenByKey(strings.TrimPrefix(tokenKey, "sk-"), false)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data": gin.H{
			"object":               "token_usage",
			"name":                 token.Name,
			"total_granted":        token.RemainQuota + token.UsedQuota,
			"total_used":           token.UsedQuota,
			"total_available":      token.RemainQuota,
			"unlimited_quota":      token.UnlimitedQuota,
			"model_limits":         token.GetModelLimitsMap(),
			"model_limits_enabled": token.ModelLimitsEnabled,
			"expires_at":           expiredAt,
		},
	})
}

func AddToken(c *gin.Context) {
	token := repo.Token{}
	err := c.ShouldBindJSON(&token)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := app.ValidateTokenName(token.Name); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := app.ValidateTokenQuota(token.RemainQuota, token.UnlimitedQuota); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := app.ValidateTokenExpiredTime(token.ExpiredTime); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := app.ValidateRateLimits(token.RateLimitRPM, token.RateLimitTPM); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	key, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	tenantId := app.GetTenantIdFromContext(common.GetContextKeyString(c, "tenant_id"))

	cleanToken := app.BuildCleanToken(c.GetInt("id"), tenantId, &token, key)
	if err = cleanToken.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	detailBytes, _ := json.Marshal(map[string]string{"name": cleanToken.Name})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, c.GetInt("id"),
		governance.ActionTokenCreated, governance.ResourceToken, cleanToken.Id,
		string(detailBytes)))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	err := repo.DeleteTokenById(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
		governance.ActionTokenDeleted, governance.ResourceToken, id, ""))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	token := repo.Token{}
	err := c.ShouldBindJSON(&token)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := app.ValidateTokenName(token.Name); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := app.ValidateTokenQuota(token.RemainQuota, token.UnlimitedQuota); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := app.ValidateTokenExpiredTime(token.ExpiredTime); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := app.ValidateRateLimits(token.RateLimitRPM, token.RateLimitTPM); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	cleanToken, err := repo.GetTokenByIds(token.Id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if token.Status == common.TokenStatusEnabled {
		if err := app.CanEnableToken(cleanToken); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		app.ApplyTokenUpdate(cleanToken, &token)
	}
	if err = cleanToken.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	action := governance.ActionTokenUpdated
	if statusOnly != "" {
		action = governance.ActionTokenStatusChanged
	}
	updateBytes, _ := json.Marshal(map[string]any{"name": cleanToken.Name, "status": cleanToken.Status})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
		action, governance.ResourceToken, cleanToken.Id, string(updateBytes)))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    cleanToken,
	})
}

type TokenBatch struct {
	Ids []int `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil || len(tokenBatch.Ids) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "参数错误",
		})
		return
	}
	userId := c.GetInt("id")
	count, err := repo.BatchDeleteTokens(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	batchBytes, _ := json.Marshal(map[string]interface{}{"count": count})
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
		governance.ActionTokenBatchDeleted, governance.ResourceToken, 0,
		string(batchBytes)))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}
