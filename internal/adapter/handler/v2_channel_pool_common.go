package handler

// Shared helpers for the account-pool ops endpoints (import, health, key
// test/restore, key settings). All of them are platform-staff only and scoped
// to the caller's tenant exactly like TestChannelV2.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// keyFingerprint is the first 16 hex chars of SHA-256(key): enough to dedupe
// and to reference a key in audit rows and API results without ever carrying
// the key itself.
func keyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])[:16]
}

// loadStaffChannel runs the shared preamble: tenant context, platform-staff
// gate (403), channel id, channel lookup (404) and tenant ownership (403).
// On failure it has already written the response.
func loadStaffChannel(c *gin.Context) (*middleware.TenantContext, *repo.Channel, bool) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Tenant context not found"})
		return nil, nil, false
	}
	if !isPlatformStaff(c, tenantCtx) {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Admin role required"})
		return nil, nil, false
	}
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel ID"})
		return nil, nil, false
	}
	ch, err := repo.GetChannelById(channelID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return nil, nil, false
	}
	if ch.TenantId != tenantCtx.TenantID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "Access denied"})
		return nil, nil, false
	}
	return tenantCtx, ch, true
}

// channelKeyCount is the number of addressable keys (a single-key channel has one).
func channelKeyCount(ch *repo.Channel) int {
	if !ch.ChannelInfo.IsMultiKey {
		return 1
	}
	return len(ch.GetKeys())
}

// parseKeyIdx reads :idx and bounds it against the channel; writes 400 on failure.
func parseKeyIdx(c *gin.Context, ch *repo.Channel) (int, bool) {
	idx, err := strconv.Atoi(c.Param("idx"))
	if err != nil || idx < 0 || idx >= channelKeyCount(ch) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid key index"})
		return 0, false
	}
	return idx, true
}

// channelKeyAt returns the plaintext key at idx (internal use only).
func channelKeyAt(ch *repo.Channel, idx int) string {
	if !ch.ChannelInfo.IsMultiKey {
		return ch.Key
	}
	keys := ch.GetKeys()
	if idx < 0 || idx >= len(keys) {
		return ""
	}
	return keys[idx]
}

// effectiveKeyProxy is the proxy a request through key idx would use.
func effectiveKeyProxy(ch *repo.Channel, idx int) string {
	if p := ch.ChannelInfo.KeyProxy(idx); ch.ChannelInfo.IsMultiKey && p != "" {
		return p
	}
	return ch.GetSetting().Proxy
}

// keyProbeFn sends one minimal chat request with the given key and returns
// the latency and a short error ("" = the key works). A var so tests swap it.
var keyProbeFn = defaultKeyProbe

func defaultKeyProbe(ctx context.Context, baseURL, model, key, proxy string) (int64, string) {
	client := channelTestHTTPClient
	if proxy != "" {
		pc, err := app.NewProxyHttpClient(proxy)
		if err != nil {
			return 0, "invalid proxy: " + err.Error()
		}
		client = pc
	}
	if model == "" {
		return 0, "channel lists no model to probe with"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 4,
	})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return 0, "build request: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	tik := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(tik).Milliseconds()
	if err != nil {
		return latency, "request failed"
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		if m := extractJSONError(raw); m != "" {
			return latency, truncateText(m, 200)
		}
		return latency, fmt.Sprintf("upstream returned HTTP %d", resp.StatusCode)
	}
	if m := extractJSONError(raw); m != "" {
		return latency, truncateText(m, 200)
	}
	return latency, ""
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// keyState is the part of a key's health that a restore proof binds to.
type keyState struct {
	ChannelStatus int
	KeyStatus     int
	DisabledTime  int64
	Reason        string
	CooldownUntil int64
}

// snapshotKeyState reads the current state of key idx from the channel row
// plus the live cooldown table.
func snapshotKeyState(ch *repo.Channel, idx int) keyState {
	st := keyState{ChannelStatus: ch.Status, KeyStatus: common.ChannelStatusEnabled}
	if ch.ChannelInfo.IsMultiKey {
		if s, ok := ch.ChannelInfo.MultiKeyStatusList[idx]; ok {
			st.KeyStatus = s
		}
		st.DisabledTime = ch.ChannelInfo.MultiKeyDisabledTime[idx]
		st.Reason = ch.ChannelInfo.MultiKeyDisabledReason[idx]
		st.CooldownUntil = ch.ChannelInfo.MultiKeyCooldownUntil[idx]
		if live := app.ChannelCoolingUntil(ch.Id, 0); live > st.CooldownUntil {
			st.CooldownUntil = live
		}
		return st
	}
	st.KeyStatus = ch.Status
	if v, ok := ch.GetOtherInfo()["status_reason"].(string); ok {
		st.Reason = v
	}
	if v, ok := ch.GetOtherInfo()["status_time"].(float64); ok {
		st.DisabledTime = int64(v)
	}
	st.CooldownUntil = app.ChannelCoolingUntil(ch.Id, 0)
	return st
}

// healthy reports that nothing is holding the key back at the given instant.
func (s keyState) healthy(now int64) bool {
	return s.ChannelStatus == common.ChannelStatusEnabled &&
		s.KeyStatus == common.ChannelStatusEnabled &&
		s.CooldownUntil <= now
}
