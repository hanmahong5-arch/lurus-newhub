package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/gin-gonic/gin"
)

// downloadLogTimeout bounds the detached download bookkeeping started by
// DownloadArtifact.
const downloadLogTimeout = 5 * time.Second

// Package-level release service (initialized in main.go)
var releaseService *app.ReleaseService

// InitReleaseService initializes the release service
func InitReleaseService() {
	releaseRepo := repo.NewReleaseRepository(repo.DB)
	releaseService = app.NewReleaseService(releaseRepo)
}

// ListReleases handles GET /api/v1/releases
// Query params: product_id, release_type, include_prerelease, page, page_size
func ListReleases(c *gin.Context) {
	// Parse query parameters
	productId := c.Query("product_id")
	releaseType := c.DefaultQuery("release_type", "stable")
	includePrerelease := c.DefaultQuery("include_prerelease", "false") == "true"
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	// Validate page size
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	params := repo.ListReleasesParams{
		ProductId:         productId,
		ReleaseType:       releaseType,
		IncludePrerelease: includePrerelease,
		Page:              page,
		PageSize:          pageSize,
	}

	response, err := releaseService.ListReleases(c.Request.Context(), params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch releases",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// GetLatestRelease handles GET /api/v1/releases/latest/:product_id
// Query params: current_version (optional)
func GetLatestRelease(c *gin.Context) {
	productId := c.Param("product_id")
	currentVersion := c.Query("current_version")

	if productId == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "product_id is required",
		})
		return
	}

	response, err := releaseService.GetLatestRelease(c.Request.Context(), productId, currentVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch latest release",
		})
		return
	}

	if response.Release == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "No release found for this product",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// GetReleaseByID handles GET /api/v1/releases/:id
func GetReleaseByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid release ID",
		})
		return
	}

	release, err := releaseService.GetReleaseByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch release",
		})
		return
	}

	if release == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Release not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    release,
	})
}

// DownloadArtifact handles GET /api/v1/releases/:id/download/:artifact_id
// Redirects to MinIO presigned URL and logs the download
func DownloadArtifact(c *gin.Context) {
	releaseIdStr := c.Param("id")
	artifactIdStr := c.Param("artifact_id")

	releaseId, err := strconv.ParseInt(releaseIdStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid release ID",
		})
		return
	}

	artifactId, err := strconv.ParseInt(artifactIdStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid artifact ID",
		})
		return
	}

	// Get artifact from database
	artifactRepo := repo.NewReleaseRepository(repo.DB)
	artifact, err := artifactRepo.GetArtifactByID(c.Request.Context(), artifactId)
	if err != nil || artifact == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Artifact not found",
		})
		return
	}

	// Verify artifact belongs to the release
	if artifact.ReleaseId != releaseId {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Artifact does not belong to this release",
		})
		return
	}

	// Log download (async, non-blocking). Everything the goroutine needs is
	// resolved here, before it is started: it outlives the handler, and once the
	// handler returns the request context is cancelled and gin recycles the
	// Context (and its Request) for the next request. Touching either from the
	// goroutine would drop the download bookkeeping and race the reuse.
	ipAddress := c.ClientIP()
	userAgent := c.GetHeader("User-Agent")
	referer := c.GetHeader("Referer")

	svc := releaseService
	go func() {
		logCtx, cancel := context.WithTimeout(context.Background(), downloadLogTimeout)
		defer cancel()
		_ = svc.HandleDownload(logCtx, artifactId, ipAddress, userAgent, referer)
	}()

	// Generate download URL
	downloadURL, err := releaseService.GenerateDownloadURL(c.Request.Context(), artifact)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to generate download URL",
		})
		return
	}

	// Redirect to MinIO presigned URL
	c.Redirect(http.StatusFound, downloadURL)
}

// GetChangelog handles GET /api/v1/releases/:id/changelog
func GetChangelog(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid release ID",
		})
		return
	}

	changelog, err := releaseService.GetChangelog(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch changelog",
		})
		return
	}

	if changelog == "" {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Changelog not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"changelog_md": changelog,
		},
	})
}
