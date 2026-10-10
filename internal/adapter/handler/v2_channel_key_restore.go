package handler

// Probe-then-restore for one key of a channel.
//
//	POST /api/v2/:tenant_slug/channels/:id/keys/:idx/test     -> restore_proof
//	POST /api/v2/:tenant_slug/channels/:id/keys/:idx/restore  {"proof": "..."}
//
// A passing probe returns restore_proof = HMAC over the key's CURRENT
// disabled/cooling state. Restore recomputes the HMAC over the state as it is
// now and only lifts the block when they match, so a probe result can never be
// used to undo a state it did not observe (the key was re-disabled, cooled
// again, or already restored in between). The idea follows gpt-load
// internal/control/credential_probe.go (MIT); the code is original.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// restoreProofMaxAge bounds how long a probe result may be used.
const restoreProofMaxAge = 15 * time.Minute

// restoreProofNow is the clock seam for tests.
var restoreProofNow = time.Now

func restoreProofMAC(channelID, idx int, st keyState, issued int64) string {
	m := hmac.New(sha256.New, []byte("key-restore-proof:"+common.CryptoSecret))
	_, _ = fmt.Fprintf(m, "%d|%d|%d|%d|%d|%d|%s|%d", channelID, idx, st.ChannelStatus, st.KeyStatus, st.DisabledTime, st.CooldownUntil, st.Reason, issued)
	return hex.EncodeToString(m.Sum(nil))
}

func makeRestoreProof(channelID, idx int, st keyState, now time.Time) string {
	issued := now.Unix()
	return strconv.FormatInt(issued, 10) + "." + restoreProofMAC(channelID, idx, st, issued)
}

// verifyRestoreProof reports whether proof was issued for exactly st and is
// still fresh.
func verifyRestoreProof(proof string, channelID, idx int, st keyState, now time.Time) bool {
	issuedStr, mac, ok := strings.Cut(proof, ".")
	if !ok {
		return false
	}
	issued, err := strconv.ParseInt(issuedStr, 10, 64)
	if err != nil || now.Sub(time.Unix(issued, 0)) > restoreProofMaxAge || issued > now.Unix()+60 {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(restoreProofMAC(channelID, idx, st, issued)))
}

// TestChannelKeyV2 probes one key and, when it passes and the key is blocked,
// returns a restore proof bound to the blocked state.
//
// Route: POST /api/v2/:tenant_slug/channels/:id/keys/:idx/test
func TestChannelKeyV2(c *gin.Context) {
	tenantCtx, ch, ok := loadStaffChannel(c)
	if !ok {
		return
	}
	idx, ok := parseKeyIdx(c, ch)
	if !ok {
		return
	}
	if err := validateChannelEgress(ch); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
		return
	}
	proxy := effectiveKeyProxy(ch, idx)
	if err := app.ValidateOutboundProxy(proxy); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "key proxy rejected: " + err.Error()})
		return
	}
	baseURL := ch.GetBaseURL()
	if baseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Channel has no base URL configured"})
		return
	}
	model := ""
	if ms := ch.GetModels(); len(ms) > 0 {
		model = strings.TrimSpace(ms[0])
	}
	// Snapshot BEFORE the probe: the proof must certify the state the operator
	// was looking at, not one that changed while the request was in flight.
	before := snapshotKeyState(ch, idx)
	latency, errMsg := keyProbeFn(c.Request.Context(), baseURL, model, channelKeyAt(ch, idx), proxy)
	passed := errMsg == ""

	data := gin.H{
		"ok":          passed,
		"latency_ms":  latency,
		"fingerprint": keyFingerprint(channelKeyAt(ch, idx)),
		"key_index":   idx,
	}
	if !passed {
		data["error"] = errMsg
	}
	now := restoreProofNow()
	if passed && !before.healthy(now.Unix()) {
		data["restore_proof"] = makeRestoreProof(ch.Id, idx, before, now)
		data["restore_proof_expires_at"] = now.Add(restoreProofMaxAge).Unix()
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionChannelTested, governance.ResourceChannel, ch.Id,
		fmt.Sprintf(`{"key_index":%d,"fingerprint":%q,"success":%t}`, idx, keyFingerprint(channelKeyAt(ch, idx)), passed)))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// RestoreChannelKeyV2 lifts the block on one key if (and only if) the proof
// still matches the key's current state.
//
// Route: POST /api/v2/:tenant_slug/channels/:id/keys/:idx/restore
func RestoreChannelKeyV2(c *gin.Context) {
	tenantCtx, ch, ok := loadStaffChannel(c)
	if !ok {
		return
	}
	idx, ok := parseKeyIdx(c, ch)
	if !ok {
		return
	}
	var req struct {
		Proof string `json:"proof"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Proof) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "proof is required", "error_code": "proof_required"})
		return
	}
	now := restoreProofNow()
	st := snapshotKeyState(ch, idx)
	if !verifyRestoreProof(req.Proof, ch.Id, idx, st, now) {
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "The key state changed since it was tested (or the proof expired); test it again",
			"error_code": "proof_stale",
		})
		return
	}
	if err := applyKeyRestore(ch, idx); err != nil {
		common.SysError("restore key: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to restore key"})
		return
	}
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, tenantCtx.UserID,
		governance.ActionChannelKeyRestored, governance.ResourceChannel, ch.Id,
		fmt.Sprintf(`{"key_index":%d,"fingerprint":%q}`, idx, keyFingerprint(channelKeyAt(ch, idx)))))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"restored": true, "key_index": idx}})
}

// applyKeyRestore clears the disabled status, reason, time and cooldown of key
// idx (or re-enables a single-key channel) and refreshes routing.
func applyKeyRestore(ch *repo.Channel, idx int) error {
	if ch.ChannelInfo.IsMultiKey {
		delete(ch.ChannelInfo.MultiKeyStatusList, idx)
		delete(ch.ChannelInfo.MultiKeyDisabledTime, idx)
		delete(ch.ChannelInfo.MultiKeyDisabledReason, idx)
		delete(ch.ChannelInfo.MultiKeyCooldownUntil, idx)
		if err := ch.SaveChannelInfo(); err != nil {
			return err
		}
		// A channel switched off because every key was parked comes back with its first key.
		if ch.Status == common.ChannelStatusAutoDisabled {
			if err := enableChannelRow(ch.Id); err != nil {
				return err
			}
		}
	} else if ch.Status != common.ChannelStatusEnabled {
		if err := enableChannelRow(ch.Id); err != nil {
			return err
		}
	}
	app.ClearChannelCooldown(ch.Id)
	AsyncGo(func() { repo.InitChannelCache() })
	return nil
}

func enableChannelRow(channelID int) error {
	if err := repo.DB.Model(&repo.Channel{}).Where("id = ?", channelID).
		Update("status", common.ChannelStatusEnabled).Error; err != nil {
		return err
	}
	return repo.UpdateAbilityStatus(channelID, true)
}
