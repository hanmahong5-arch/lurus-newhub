package handler

import (
	"net/http"
	"strconv"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

func GetAllLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	// Root (platform operator) sees every tenant's logs; a tenant admin is
	// constrained to their own tenant. AdminAuth() alone does not enforce this.
	scope := repo.AllTenantsForAdmin()
	if c.GetInt("role") < common.RoleRootUser {
		scope = repo.ForTenant(c.GetString("tenant_id"))
	}
	logs, total, err := repo.GetAllLogs(scope, logType, startTimestamp, endTimestamp, modelName, username, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), channel, group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUserLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId := c.GetInt("id")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	group := c.Query("group")
	logs, total, err := repo.GetUserLogs(userId, logType, startTimestamp, endTimestamp, modelName, tokenName, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
	return
}

func SearchAllLogs(c *gin.Context) {
	keyword := c.Query("keyword")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channelID, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))

	// A tenant admin must never see another tenant's logs. Both the Meilisearch
	// index and the app.SearchLogs admin fallback are cross-tenant, so scope
	// non-root callers directly to the tenant-filtered DB search.
	if c.GetInt("role") < common.RoleRootUser {
		logs, err := repo.SearchAllLogs(repo.ForTenant(c.GetString("tenant_id")), keyword)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": gin.H{
				"items": logs,
				"total": len(logs),
				"page":  page,
			},
		})
		return
	}

	result, err := app.SearchLogs(app.LogSearchParams{
		Keyword:        keyword,
		LogType:        logType,
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		Username:       username,
		TokenName:      tokenName,
		ModelName:      modelName,
		ChannelID:      channelID,
		Group:          group,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": result.Items,
			"total": result.Total,
			"page":  result.Page,
		},
	})
}

func SearchUserLogs(c *gin.Context) {
	keyword := c.Query("keyword")
	userId := c.GetInt("id")
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	group := c.Query("group")
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))

	result, err := app.SearchLogs(app.LogSearchParams{
		Keyword:        keyword,
		UserId:         userId,
		Username:       username,
		LogType:        logType,
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		TokenName:      tokenName,
		ModelName:      modelName,
		Group:          group,
		Page:           page,
		PageSize:       pageSize,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"items": result.Items,
			"total": result.Total,
			"page":  result.Page,
		},
	})
}

func GetLogByKey(c *gin.Context) {
	key := c.Query("key")
	// The queried key is caller-supplied, so ownership must be decided from
	// the authenticated principal instead: TokenAuth sets both "id" (the
	// token owner) and "tenant_id" (the token's tenant). repo.GetLogByKey
	// refuses anything that is not the caller's own token, and returns the
	// unknown-key shape for foreign keys so this stays out of oracle duty.
	logs, err := repo.GetLogByKey(key, c.GetInt("id"), c.GetString("tenant_id"))
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data":    logs,
	})
}

func GetLogsStat(c *gin.Context) {
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	username := c.Query("username")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	// Root sees platform-wide stats; a tenant admin is scoped to their tenant.
	scope := repo.AllTenantsForAdmin()
	if c.GetInt("role") < common.RoleRootUser {
		scope = repo.ForTenant(c.GetString("tenant_id"))
	}
	stat := repo.SumUsedQuota(scope, logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	//tokenNum := repo.SumUsedToken(repo.AllTenantsForAdmin(), logType, startTimestamp, endTimestamp, modelName, username, "")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": stat.Quota,
			"rpm":   stat.Rpm,
			"tpm":   stat.Tpm,
		},
	})
	return
}

func GetLogsSelfStat(c *gin.Context) {
	username := c.GetString("username")
	logType, _ := strconv.Atoi(c.Query("type"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	tokenName := c.Query("token_name")
	modelName := c.Query("model_name")
	channel, _ := strconv.Atoi(c.Query("channel"))
	group := c.Query("group")
	// Self stat: scope to the caller's tenant (set by authHelper for every
	// session/access-token request). The username filter alone is not an
	// isolation boundary once usernames are only unique per tenant.
	quotaNum := repo.SumUsedQuota(repo.ForTenant(c.GetString("tenant_id")), logType, startTimestamp, endTimestamp, modelName, username, tokenName, channel, group)
	//tokenNum := repo.SumUsedToken(repo.ForTenant(c.GetString("tenant_id")), logType, startTimestamp, endTimestamp, modelName, username, tokenName)
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota": quotaNum.Quota,
			"rpm":   quotaNum.Rpm,
			"tpm":   quotaNum.Tpm,
			//"token": tokenNum,
		},
	})
	return
}

func DeleteHistoryLogs(c *gin.Context) {
	targetTimestamp, _ := strconv.ParseInt(c.Query("target_timestamp"), 10, 64)
	if targetTimestamp == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "target timestamp is required",
		})
		return
	}
	// Root performs platform-wide retention cleanup; a tenant admin may only
	// delete their own tenant's logs — never another tenant's history.
	scope := repo.AllTenantsForAdmin()
	if c.GetInt("role") < common.RoleRootUser {
		scope = repo.ForTenant(c.GetString("tenant_id"))
	}
	count, err := repo.DeleteOldLog(c.Request.Context(), scope, targetTimestamp, 100)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
	return
}
