package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/gin-gonic/gin"
)

// SwitchConfigPreset represents a cloud-hosted configuration template for an AI CLI tool.
type SwitchConfigPreset struct {
	ID          string                 `json:"id"`
	Tool        string                 `json:"tool"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Category    string                 `json:"category"`
	ConfigJSON  map[string]interface{} `json:"config_json"`
	IsOfficial  bool                   `json:"is_official"`
	CreatedAt   time.Time              `json:"created_at"`
}

// ListSwitchPresets handles GET /api/v2/switch/presets
// Query params: tool (required for filtering), category (optional), page, page_size
// No authentication required — presets are public read-only resources.
func ListSwitchPresets(c *gin.Context) {
	tool := c.Query("tool")
	category := c.Query("category")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	presets, err := repo.ListSwitchConfigPresets(c.Request.Context(), tool, category, pageSize, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "failed to fetch presets",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    presets,
		"page":    page,
		"limit":   pageSize,
	})
}

// CreateSwitchPreset handles POST /api/v2/switch/presets (admin-only)
// Body: { tool, name, description, category, config_json, is_official }
func CreateSwitchPreset(c *gin.Context) {
	var body struct {
		Tool        string                 `json:"tool"        binding:"required"`
		Name        string                 `json:"name"        binding:"required"`
		Description string                 `json:"description"`
		Category    string                 `json:"category"`
		ConfigJSON  map[string]interface{} `json:"config_json" binding:"required"`
		IsOfficial  bool                   `json:"is_official"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "invalid request body: " + err.Error(),
		})
		return
	}

	preset, err := repo.CreateSwitchConfigPreset(c.Request.Context(), body.Tool, body.Name, body.Description, body.Category, body.ConfigJSON, body.IsOfficial)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "failed to create preset",
		})
		return
	}

	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, c.GetInt("id"),
		governance.ActionSwitchPresetCreated, governance.ResourceSwitchPreset, 0,
		fmt.Sprintf(`{"preset_id":%q,"tool":%q,"name":%q,"is_official":%t}`, preset.ID, body.Tool, body.Name, body.IsOfficial)))

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    preset,
	})
}
