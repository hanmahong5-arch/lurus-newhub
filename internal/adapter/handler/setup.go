package handler

import (
	"time"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type Setup struct {
	Status       bool   `json:"status"`
	RootInit     bool   `json:"root_init"`
	DatabaseType string `json:"database_type"`
}

type SetupRequest struct {
	Username           string `json:"username"`
	SelfUseModeEnabled bool   `json:"SelfUseModeEnabled"`
	DemoSiteEnabled    bool   `json:"DemoSiteEnabled"`
}

func GetSetup(c *gin.Context) {
	setup := Setup{
		Status: constant.IsSetup(),
	}
	if constant.IsSetup() {
		c.JSON(200, gin.H{
			"success": true,
			"data":    setup,
		})
		return
	}
	setup.RootInit = repo.RootUserExists()
	// Runtime is PG-only (2026-06); the SQLite arm exists only for the
	// hermetic glebarez unit-test tier.
	setup.DatabaseType = "postgres"
	if common.UsingSQLite {
		setup.DatabaseType = "sqlite"
	}
	c.JSON(200, gin.H{
		"success": true,
		"data":    setup,
	})
}

func PostSetup(c *gin.Context) {
	// Atomically claim the setup slot. Only ONE request can win;
	// all others are rejected immediately, eliminating the TOCTOU race.
	if !constant.TryClaimSetup() {
		c.JSON(200, gin.H{
			"success": false,
			"message": "系统已经初始化完成",
		})
		return
	}

	// From here on, this goroutine is the sole owner of setup.
	// If anything fails, revert so setup can be retried.
	committed := false
	defer func() {
		if !committed {
			constant.SetSetup(false)
		}
	}()

	// Check if root user already exists
	rootExists := repo.RootUserExists()

	var req SetupRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": "请求参数有误",
		})
		return
	}

	// If root doesn't exist, create the initial admin account
	if !rootExists {
		// 按字符（rune）计数，与提示语的“1-12 个字符”一致；len() 数的是
		// UTF-8 字节，5 个汉字（15 字节）会被误判超长。
		if n := utf8.RuneCountInString(req.Username); n == 0 || n > 12 {
			c.JSON(200, gin.H{
				"success": false,
				"message": "用户名长度必须在1-12个字符之间",
			})
			return
		}

		rootUser := repo.User{
			Username:    req.Username,
			Role:        common.RoleRootUser,
			Status:      common.UserStatusEnabled,
			DisplayName: "Root User",
			AccessToken: nil,
			Quota:       100000000,
			TenantId:    "default",
		}
		// Setup runs before any tenant context exists, so wrap the DB to inject
		// the default tenant — the tenant_plugin's beforeCreate hook rejects
		// inserts on tenant-scoped tables when no tenant ID is in context.
		err = repo.WithTenantID(repo.DB, "default").Create(&rootUser).Error
		if err != nil {
			c.JSON(200, gin.H{
				"success": false,
				"message": "创建管理员账号失败: " + err.Error(),
			})
			return
		}
	}

	// Set operation modes
	operation_setting.SelfUseModeEnabled = req.SelfUseModeEnabled
	operation_setting.DemoSiteEnabled = req.DemoSiteEnabled

	// Save operation modes to database for persistence
	err = repo.UpdateOption("SelfUseModeEnabled", boolToString(req.SelfUseModeEnabled))
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": "保存自用模式设置失败: " + err.Error(),
		})
		return
	}

	err = repo.UpdateOption("DemoSiteEnabled", boolToString(req.DemoSiteEnabled))
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": "保存演示站点模式设置失败: " + err.Error(),
		})
		return
	}

	// Persist the setup record (atomic flag already set by TryClaimSetup)
	setup := repo.Setup{
		Version:       common.Version,
		InitializedAt: time.Now().Unix(),
	}
	err = repo.DB.Create(&setup).Error
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"message": "系统初始化失败: " + err.Error(),
		})
		return
	}

	committed = true
	c.JSON(200, gin.H{
		"success": true,
		"message": "系统初始化成功",
	})
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
