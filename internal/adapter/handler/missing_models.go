package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// GetMissingModels returns the list of model names that are referenced by channels
// but do not have corresponding records in the models meta table.
// This helps administrators quickly discover models that need configuration.
//
// Non-root callers are answered over their own tenant's routable channels
// (platform-shared plus their own); root keeps the platform-wide answer.
// Unscoped, this hint enumerated every tenant's model names.
func GetMissingModels(c *gin.Context) {
	tenantID, isRoot := tenantScopeForDiscovery(c)
	var missing []string
	var err error
	if isRoot {
		missing, err = repo.GetMissingModels()
	} else {
		missing, err = repo.GetMissingModelsForTenant(tenantID)
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    missing,
	})
}
