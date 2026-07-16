package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Option keys the admin sets (via the existing V2 admin options API /
// options table) to publish a Switch desktop release on the mirror.
// Deliberately no "...key"/"...secret"/"...token" suffixes — GetOptions
// filters those out of the admin listing.
const (
	optSwitchAppVersion     = "switch_app.latest_version" // required, semver ("v" prefix optional)
	optSwitchAppNotes       = "switch_app.notes"          // optional release notes
	optSwitchAppDownloadURL = "switch_app.download_url"   // required, https, host must be this Hub or *.lurus.cn
	optSwitchAppSHA256      = "switch_app.sha256"         // optional (client verifies the .sha256 sidecar today)
	optSwitchAppSigURL      = "switch_app.sig_url"        // optional ed25519 signature sidecar URL
)

// GetSwitchAppRelease returns the latest Switch desktop app release for the
// CN-survivable self-update mirror. The Switch client tries this endpoint
// FIRST and falls back to GitHub Releases on any failure, so "not configured"
// is a 404, never a 200 with junk.
//
// Route: GET /api/v2/switch/app/releases/latest (public, no auth — same as
// the sibling /switch/tools/versions endpoint).
//
// Contract (mirrors the client's HubReleaseChecker in lurus-switch
// internal/updater/hub_checker.go — keep the two in lockstep):
//
//	200 OK: {"success":true,"data":{"version","notes","download_url","sha256","sig_url"}}
//	404:    mirror has no published release (client falls back to GitHub)
//
// Integrity note: the client pins download_url to https + (this Hub's host or
// *.lurus.cn) and fetches "<download_url>.sha256" / "<download_url>.sig"
// sidecars, so artifacts should be hosted at stable URLs on the Hub's own
// domain with their sidecar files alongside — presigned/expiring URLs break
// the sidecar convention.
func GetSwitchAppRelease(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	version := common.OptionMap[optSwitchAppVersion]
	notes := common.OptionMap[optSwitchAppNotes]
	downloadURL := common.OptionMap[optSwitchAppDownloadURL]
	sha256 := common.OptionMap[optSwitchAppSHA256]
	sigURL := common.OptionMap[optSwitchAppSigURL]
	common.OptionMapRWMutex.RUnlock()

	if version == "" || downloadURL == "" {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "no Switch app release published on this hub",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"version":      version,
			"notes":        notes,
			"download_url": downloadURL,
			"sha256":       sha256,
			"sig_url":      sigURL,
		},
	})
}
