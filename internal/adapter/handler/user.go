package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/logger"
	"github.com/LurusTech/lurus-hub/internal/pkg/search"

	"github.com/gin-gonic/gin"
)

func GetAllUsers(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	var users []*repo.User
	var total int64
	var err error
	// Non-root callers see only their own tenant's users.
	if c.GetInt("role") >= common.RoleRootUser {
		users, total, err = repo.GetAllUsers(pageInfo)
	} else {
		users, total, err = repo.GetUsersByTenant(c.GetString("tenant_id"), pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)

	common.ApiSuccess(c, pageInfo)
	return
}

func SearchUsers(c *gin.Context) {
	keyword := c.Query("keyword")
	group := c.Query("group")
	pageInfo := common.GetPageQuery(c)

	isRoot := c.GetInt("role") >= common.RoleRootUser
	callerTenant := c.GetString("tenant_id")

	// Parse status parameter
	status := 0
	if statusStr := c.Query("status"); statusStr != "" {
		if s, err := strconv.Atoi(statusStr); err == nil {
			status = s
		}
	}

	// Try Meilisearch first if enabled. Root/platform-operator only: the
	// Meilisearch index is not tenant-filtered, so non-root callers fall through
	// to the tenant-scoped DB query below.
	if isRoot && search.IsEnabled() {
		page := (pageInfo.GetStartIdx() / pageInfo.GetPageSize()) + 1
		if page < 1 {
			page = 1
		}

		results, total, err := search.SearchUsers(keyword, group, status, page, pageInfo.GetPageSize())
		if err == nil {
			users := make([]*repo.User, 0, len(results))
			for _, result := range results {
				user := &repo.User{}
				if id, ok := result["id"].(float64); ok {
					user.Id = int(id)
				}
				if username, ok := result["username"].(string); ok {
					user.Username = username
				}
				if email, ok := result["email"].(string); ok {
					user.Email = email
				}
				if displayName, ok := result["display_name"].(string); ok {
					user.DisplayName = displayName
				}
				if role, ok := result["role"].(float64); ok {
					user.Role = int(role)
				}
				if statusVal, ok := result["status"].(float64); ok {
					user.Status = int(statusVal)
				}
				if quota, ok := result["quota"].(float64); ok {
					user.Quota = int(quota)
				}
				if usedQuota, ok := result["used_quota"].(float64); ok {
					user.UsedQuota = int(usedQuota)
				}
				if groupVal, ok := result["group"].(string); ok {
					user.Group = groupVal
				}
				users = append(users, user)
			}

			pageInfo.SetTotal(int(total))
			pageInfo.SetItems(users)
			common.ApiSuccess(c, pageInfo)
			return
		}

		common.SysLog(fmt.Sprintf("Meilisearch search failed, falling back to database: %v", err))
	}

	var users []*repo.User
	var total int64
	var err error
	if isRoot {
		users, total, err = repo.SearchUsers(keyword, group, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	} else {
		users, total, err = repo.SearchUsersByTenant(callerTenant, keyword, group, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)
	common.ApiSuccess(c, pageInfo)
	return
}

func GetUser(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := repo.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	myRole := c.GetInt("role")
	if err := app.CheckPermission(myRole, user.Role); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无权获取同级或更高等级用户的信息",
		})
		return
	}
	if enforceTenantScope(c, user.TenantId) {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user,
	})
	return
}

func GenerateAccessToken(c *gin.Context) {
	id := c.GetInt("id")
	user, err := repo.GetUserById(id, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	randI := common.GetRandomInt(4)
	key, err := common.GenerateRandomKey(29 + randI)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "生成失败",
		})
		common.SysLog("failed to generate key: " + err.Error())
		return
	}
	user.SetAccessToken(key)

	if repo.DB.Where("access_token = ?", user.AccessToken).First(user).RowsAffected != 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "请重试，系统生成的 UUID 竟然重复了！",
		})
		return
	}

	if err := user.Update(); err != nil {
		common.ApiError(c, err)
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, id,
		governance.ActionAuthTokenRotated, governance.ResourceUser, id,
		`{"kind":"access_token"}`))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    user.AccessToken,
	})
	return
}

func GetSelf(c *gin.Context) {
	id := c.GetInt("id")
	userRole := c.GetInt("role")
	user, err := repo.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// Hide admin remarks from regular users
	user.Remark = ""

	permissions := calculateUserPermissions(userRole)
	userSetting := user.GetSetting()

	responseData := map[string]interface{}{
		"id":              user.Id,
		"username":        user.Username,
		"display_name":    user.DisplayName,
		"role":            user.Role,
		"status":          user.Status,
		"email":           user.Email,
		"group":           user.Group,
		"quota":           user.Quota,
		"used_quota":      user.UsedQuota,
		"request_count":   user.RequestCount,
		"setting":         user.Setting,
		"sidebar_modules": userSetting.SidebarModules,
		"permissions":     permissions,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    responseData,
	})
	return
}

// calculateUserPermissions computes permission flags based on user role.
func calculateUserPermissions(userRole int) map[string]interface{} {
	permissions := map[string]interface{}{}

	if userRole == common.RoleRootUser {
		permissions["sidebar_settings"] = false
		permissions["sidebar_modules"] = map[string]interface{}{}
	} else if userRole == common.RoleAdminUser {
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]interface{}{
			"admin": map[string]interface{}{
				"setting": false,
			},
		}
	} else {
		permissions["sidebar_settings"] = true
		permissions["sidebar_modules"] = map[string]interface{}{
			"admin": false,
		}
	}

	return permissions
}

// generateDefaultSidebarConfig generates a role-based default sidebar configuration.
func generateDefaultSidebarConfig(userRole int) string {
	defaultConfig := map[string]interface{}{}

	defaultConfig["chat"] = map[string]interface{}{
		"enabled":    true,
		"playground": true,
		"chat":       true,
	}

	defaultConfig["console"] = map[string]interface{}{
		"enabled":    true,
		"detail":     true,
		"token":      true,
		"log":        true,
		"midjourney": true,
		"task":       true,
	}

	defaultConfig["personal"] = map[string]interface{}{
		"enabled":  true,
		"topup":    true,
		"personal": true,
	}

	if userRole == common.RoleAdminUser {
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    false,
		}
	} else if userRole == common.RoleRootUser {
		defaultConfig["admin"] = map[string]interface{}{
			"enabled":    true,
			"channel":    true,
			"models":     true,
			"redemption": true,
			"user":       true,
			"setting":    true,
		}
	}

	configBytes, err := json.Marshal(defaultConfig)
	if err != nil {
		common.SysLog("generate default sidebar config failed: " + err.Error())
		return ""
	}

	return string(configBytes)
}

func GetUserModels(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		id = c.GetInt("id")
	}
	user, err := repo.GetUserCache(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groups := app.GetUserUsableGroups(user.Group)
	var models []string
	for group := range groups {
		for _, g := range repo.GetGroupEnabledModels(group) {
			if !common.StringsContains(models, g) {
				models = append(models, g)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    models,
	})
	return
}

func UpdateUser(c *gin.Context) {
	var updatedUser repo.User
	err := json.NewDecoder(c.Request.Body).Decode(&updatedUser)
	if err != nil || updatedUser.Id == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	if err := common.Validate.Struct(&updatedUser); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "输入不合法 " + err.Error(),
		})
		return
	}
	originUser, err := repo.GetUserById(updatedUser.Id, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if enforceTenantScope(c, originUser.TenantId) {
		return
	}
	myRole := c.GetInt("role")
	if err := app.CheckPermission(myRole, originUser.Role); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无权更新同权限等级或更高权限等级的用户信息",
		})
		return
	}
	if err := app.CheckRolePromotion(myRole, updatedUser.Role); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无权将其他用户权限等级提升到大于等于自己的权限等级",
		})
		return
	}
	if err := app.CheckSelfDemotion(c.GetInt("id"), updatedUser.Id, myRole, updatedUser.Role); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "不能降低自己的权限等级",
		})
		return
	}
	if err := updatedUser.Edit(); err != nil {
		common.ApiError(c, err)
		return
	}
	if originUser.Quota != updatedUser.Quota {
		repo.RecordLog(originUser.Id, repo.LogTypeManage, fmt.Sprintf("管理员将用户额度从 %s修改为 %s", logger.LogQuota(originUser.Quota), logger.LogQuota(updatedUser.Quota)))
		governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
			governance.ActionUserQuotaAdjusted, governance.ResourceUser, originUser.Id,
			fmt.Sprintf(`{"old_quota":%d,"new_quota":%d}`, originUser.Quota, updatedUser.Quota)))
	}
	action := governance.ActionUserUpdated
	auditDetail := fmt.Sprintf(`{"username":%q}`, updatedUser.Username)
	// Role 0 means "not provided" in the payload; repo.Edit skips persisting it,
	// so only emit the role-change audit event when the change was actually saved.
	if updatedUser.Role != 0 && originUser.Role != updatedUser.Role {
		action = governance.ActionUserRoleChanged
		auditDetail = fmt.Sprintf(`{"old_role":%d,"new_role":%d}`, originUser.Role, updatedUser.Role)
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		action, governance.ResourceUser, originUser.Id, auditDetail))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

func UpdateSelf(c *gin.Context) {
	var requestData map[string]interface{}
	err := json.NewDecoder(c.Request.Body).Decode(&requestData)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}

	// Handle sidebar_modules update
	if sidebarModules, exists := requestData["sidebar_modules"]; exists {
		userId := c.GetInt("id")
		user, err := repo.GetUserById(userId, false)
		if err != nil {
			common.ApiError(c, err)
			return
		}

		currentSetting := user.GetSetting()
		if sidebarModulesStr, ok := sidebarModules.(string); ok {
			currentSetting.SidebarModules = sidebarModulesStr
		}

		user.SetSetting(currentSetting)
		if err := user.Update(); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "更新设置失败: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "设置更新成功",
		})
		return
	}

	// Update display name / username
	var user repo.User
	requestDataBytes, err := json.Marshal(requestData)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	if err = json.Unmarshal(requestDataBytes, &user); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}

	if err := common.Validate.Struct(&user); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "输入不合法 " + err.Error(),
		})
		return
	}

	cleanUser := repo.User{
		Id:          c.GetInt("id"),
		Username:    user.Username,
		DisplayName: user.DisplayName,
	}
	if err := cleanUser.Update(); err != nil {
		common.ApiError(c, err)
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, cleanUser.Id,
		governance.ActionUserSelfUpdated, governance.ResourceUser, cleanUser.Id,
		fmt.Sprintf(`{"username":%q}`, cleanUser.Username)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}

type UpdateUserSettingRequest struct {
	QuotaWarningType           string  `json:"notify_type"`
	QuotaWarningThreshold      float64 `json:"quota_warning_threshold"`
	WebhookUrl                 string  `json:"webhook_url,omitempty"`
	WebhookSecret              string  `json:"webhook_secret,omitempty"`
	NotificationEmail          string  `json:"notification_email,omitempty"`
	BarkUrl                    string  `json:"bark_url,omitempty"`
	GotifyUrl                  string  `json:"gotify_url,omitempty"`
	GotifyToken                string  `json:"gotify_token,omitempty"`
	GotifyPriority             int     `json:"gotify_priority,omitempty"`
	AcceptUnsetModelRatioModel bool    `json:"accept_unset_model_ratio_model"`
	RecordIpLog                bool    `json:"record_ip_log"`
}

func UpdateUserSetting(c *gin.Context) {
	var req UpdateUserSettingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}

	if req.QuotaWarningType != dto.NotifyTypeEmail && req.QuotaWarningType != dto.NotifyTypeWebhook && req.QuotaWarningType != dto.NotifyTypeBark && req.QuotaWarningType != dto.NotifyTypeGotify {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无效的预警类型",
		})
		return
	}

	if req.QuotaWarningThreshold <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "预警阈值必须大于0",
		})
		return
	}

	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		if req.WebhookUrl == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Webhook地址不能为空",
			})
			return
		}
		if _, err := url.ParseRequestURI(req.WebhookUrl); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的Webhook地址",
			})
			return
		}
	}

	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		if !strings.Contains(req.NotificationEmail, "@") {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的邮箱地址",
			})
			return
		}
	}

	if req.QuotaWarningType == dto.NotifyTypeBark {
		if req.BarkUrl == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Bark推送URL不能为空",
			})
			return
		}
		if _, err := url.ParseRequestURI(req.BarkUrl); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的Bark推送URL",
			})
			return
		}
		if !strings.HasPrefix(req.BarkUrl, "https://") && !strings.HasPrefix(req.BarkUrl, "http://") {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Bark推送URL必须以http://或https://开头",
			})
			return
		}
	}

	if req.QuotaWarningType == dto.NotifyTypeGotify {
		if req.GotifyUrl == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Gotify服务器地址不能为空",
			})
			return
		}
		if req.GotifyToken == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Gotify令牌不能为空",
			})
			return
		}
		if _, err := url.ParseRequestURI(req.GotifyUrl); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无效的Gotify服务器地址",
			})
			return
		}
		if !strings.HasPrefix(req.GotifyUrl, "https://") && !strings.HasPrefix(req.GotifyUrl, "http://") {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "Gotify服务器地址必须以http://或https://开头",
			})
			return
		}
	}

	userId := c.GetInt("id")
	user, err := repo.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	settings := dto.UserSetting{
		NotifyType:            req.QuotaWarningType,
		QuotaWarningThreshold: req.QuotaWarningThreshold,
		AcceptUnsetRatioModel: req.AcceptUnsetModelRatioModel,
		RecordIpLog:           req.RecordIpLog,
	}

	if req.QuotaWarningType == dto.NotifyTypeWebhook {
		settings.WebhookUrl = req.WebhookUrl
		if req.WebhookSecret != "" {
			settings.WebhookSecret = req.WebhookSecret
		}
	}

	if req.QuotaWarningType == dto.NotifyTypeEmail && req.NotificationEmail != "" {
		settings.NotificationEmail = req.NotificationEmail
	}

	if req.QuotaWarningType == dto.NotifyTypeBark {
		settings.BarkUrl = req.BarkUrl
	}

	if req.QuotaWarningType == dto.NotifyTypeGotify {
		settings.GotifyUrl = req.GotifyUrl
		settings.GotifyToken = req.GotifyToken
		if req.GotifyPriority < 0 || req.GotifyPriority > 10 {
			settings.GotifyPriority = 5
		} else {
			settings.GotifyPriority = req.GotifyPriority
		}
	}

	user.SetSetting(settings)
	if err := user.Update(); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "更新设置失败: " + err.Error(),
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, userId,
		governance.ActionUserSelfUpdated, governance.ResourceUser, userId,
		fmt.Sprintf(`{"notify_type":%q}`, req.QuotaWarningType)))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "设置已更新",
	})
}

// Logout clears the v1 session cookie.
func Logout(c *gin.Context) {
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorUser, c.GetInt("id"),
		governance.ActionAuthLogout, governance.ResourceUser, c.GetInt("id"), ""))
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
	})
}
