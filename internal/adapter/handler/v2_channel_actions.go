/*
Copyright (C) 2025 LurusTech

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

package handler

// ============================================================================
// V2 Channel Actions — test connection + upstream model sync
// Routes:
//   POST /api/v2/:tenant_slug/channels/:id/test
//   GET  /api/v2/:tenant_slug/channels/:id/upstream-models
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// validateChannelEgress is the single SSRF gate for a channel's outbound
// targets — its base_url and its optional proxy. Every write path
// (CreateChannelV2/UpdateChannelV2) and every outbound sink (TestChannelV2/
// FetchUpstreamModelsV2) funnels through here, so a tenant admin cannot point
// channel egress at an internal address and make the gateway relay or reflect
// in-cluster responses.
func validateChannelEgress(channel *repo.Channel) error {
	if err := app.ValidateOutboundURL(channel.GetBaseURL()); err != nil {
		return fmt.Errorf("channel base_url rejected: %v", err)
	}
	if err := app.ValidateOutboundProxy(channel.GetSetting().Proxy); err != nil {
		return fmt.Errorf("channel proxy rejected: %v", err)
	}
	return nil
}

// channelTestHTTPClient is a package-level client so tests can swap in a
// mock transport. Uses the same 120 s budget as playgroundHTTPClient.
var channelTestHTTPClient = &http.Client{Timeout: 120 * time.Second}

// testChannelRequest is the minimal chat completion request sent to verify
// that a channel is reachable and returns a valid response.
var testChannelRequestBody = map[string]interface{}{
	"model": "gpt-3.5-turbo",
	"messages": []map[string]string{
		{"role": "user", "content": "hi"},
	},
	"max_tokens": 4,
}

// TestChannelV2 sends a minimal chat completion request through the channel
// and returns success/latency_ms/error.
//
// Route: POST /api/v2/:tenant_slug/channels/:id/test
func TestChannelV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}

	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel ID"})
		return
	}

	channel, err := repo.GetChannelById(channelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return
	}

	// Verify tenant ownership
	if channel.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return
	}

	// Resolve the upstream endpoint.
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Channel has no base URL configured"})
		return
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// SSRF guard (defense in depth for channels persisted before the write-time
	// gate): never let a one-click test probe an internal address.
	if err := validateChannelEgress(channel); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
		return
	}

	// Pick the first configured model as test model when available.
	testReqBody := testChannelRequestBody
	models := channel.GetModels()
	if len(models) > 0 {
		testModel := strings.TrimSpace(models[0])
		if testModel != "" {
			testReqBody = map[string]interface{}{
				"model":      testModel,
				"messages":   []map[string]string{{"role": "user", "content": "hi"}},
				"max_tokens": 4,
			}
		}
	}

	bodyBytes, err := json.Marshal(testReqBody)
	if err != nil {
		common.SysError(fmt.Sprintf("TestChannelV2: marshal request: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to build test request"})
		return
	}

	endpoint := baseURL + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create request: " + err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	tik := time.Now()
	resp, err := channelTestHTTPClient.Do(req)
	latencyMs := time.Since(tik).Milliseconds()
	if err != nil {
		recordChannelTestAudit(c, tenantCtx.UserID, channel.Id, false)
		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"latency_ms": latencyMs,
			"error":      err.Error(),
		})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Read a small prefix for error detection.
	const maxBody = 8 << 10 // 8 KB
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))

	if resp.StatusCode != http.StatusOK {
		errMsg := extractJSONError(raw)
		if errMsg == "" {
			errMsg = fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode)
		}
		recordChannelTestAudit(c, tenantCtx.UserID, channel.Id, false)
		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"latency_ms": latencyMs,
			"error":      errMsg,
		})
		return
	}

	if errMsg := extractJSONError(raw); errMsg != "" {
		recordChannelTestAudit(c, tenantCtx.UserID, channel.Id, false)
		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"latency_ms": latencyMs,
			"error":      errMsg,
		})
		return
	}

	recordChannelTestAudit(c, tenantCtx.UserID, channel.Id, true)
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"latency_ms": latencyMs,
	})
}

// recordChannelTestAudit logs a live outbound test call (it uses the
// tenant's stored channel key) so a "who tested this channel and when" trail
// exists even though the test itself never mutates channel state.
func recordChannelTestAudit(c *gin.Context, actorID, channelID int, success bool) {
	details := fmt.Sprintf(`{"success":%t}`, success)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionChannelTested, governance.ResourceChannel, channelID, details))
}

// FetchUpstreamModelsV2 fetches the model list from the channel's /v1/models
// endpoint and returns a diff against the currently configured models.
// Read-only — does NOT modify channel.Models.
//
// Route: GET /api/v2/:tenant_slug/channels/:id/upstream-models
func FetchUpstreamModelsV2(c *gin.Context) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return
	}
	if !requireTenantAdmin(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return
	}

	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel ID"})
		return
	}

	channel, err := repo.GetChannelById(channelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return
	}

	// Verify tenant ownership
	if channel.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return
	}

	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Channel has no base URL configured"})
		return
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// SSRF guard (defense in depth): the upstream-models probe reflects the
	// upstream body back to the caller — the cleanest read-SSRF vector — so it
	// must never reach an internal address.
	if err := validateChannelEgress(channel); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
		return
	}

	endpoint := baseURL + "/v1/models"
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create request: " + err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+channel.Key)

	resp, err := channelTestHTTPClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "Failed to reach upstream: " + err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode),
		})
		return
	}

	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "Failed to parse upstream model list: " + err.Error()})
		return
	}

	upstream := make([]string, 0, len(listResp.Data))
	for _, m := range listResp.Data {
		if strings.TrimSpace(m.ID) != "" {
			upstream = append(upstream, m.ID)
		}
	}

	// Build lookup sets for diff.
	current := make(map[string]struct{})
	for _, m := range channel.GetModels() {
		if m != "" {
			current[m] = struct{}{}
		}
	}
	upstreamSet := make(map[string]struct{}, len(upstream))
	for _, m := range upstream {
		upstreamSet[m] = struct{}{}
	}

	newModels := make([]string, 0)
	for _, m := range upstream {
		if _, ok := current[m]; !ok {
			newModels = append(newModels, m)
		}
	}

	missing := make([]string, 0)
	for m := range current {
		if _, ok := upstreamSet[m]; !ok {
			missing = append(missing, m)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"upstream": upstream,
			"new":      newModels,
			"missing":  missing,
		},
	})
}

// extractJSONError reads an "error.message" field from a JSON response body.
// Returns "" when no error field is found.
func extractJSONError(body []byte) string {
	b := bytes.TrimSpace(body)
	if len(b) == 0 || b[0] != '{' {
		return ""
	}
	var envelope struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return ""
	}
	if envelope.Error == nil {
		return ""
	}
	return strings.TrimSpace(envelope.Error.Message)
}
