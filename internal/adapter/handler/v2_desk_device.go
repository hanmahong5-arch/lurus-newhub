package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Hub Desk device-code → sk- mint (TokenHub 0.10 Desk unlock).
//
// Flow (RFC 8628-shaped, Hub-hosted — not Platform QR session as Bearer):
//  1. Desk POST /api/v2/desk/device/code → device_code + user_code + verification_uri
//  2. User opens verification_uri, signs in (OIDC session), POST approve with user_code
//  3. Desk polls POST /api/v2/desk/device/token with device_code → sk- once
//
// Platform QR/session cookies are never accepted as /v1 relay Authorization.

const (
	deskDeviceTTL        = 10 * time.Minute
	deskDevicePollMin    = 2
	deskTokenName        = "hubdesk"
	deskDeviceRedisPref  = "desk:device:"
	deskUserCodeRedisKey = "desk:usercode:"
)

type deskDevicePending struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	UserID     int    `json:"user_id,omitempty"`
	TenantID   string `json:"tenant_id,omitempty"`
	ExpiresAt  int64  `json:"expires_at"`
}

var (
	deskDeviceMem   sync.Map // device_code -> deskDevicePending
	deskUserCodeMem sync.Map // user_code -> device_code
)

func deskPublicBase() string {
	if v := strings.TrimRight(os.Getenv("RELAY_PUBLIC_BASE_URL"), "/"); v != "" {
		return v
	}
	if v := strings.TrimRight(system_setting.ServerAddress, "/"); v != "" {
		return v
	}
	return "https://hub.lurus.cn"
}

func newDeskCodes() (deviceCode, userCode string, err error) {
	var raw [24]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	deviceCode = hex.EncodeToString(raw[:])
	// 8-char Crockford-ish user code (hex of 4 bytes) — short enough to type.
	var uc [4]byte
	if _, err = rand.Read(uc[:]); err != nil {
		return "", "", err
	}
	userCode = strings.ToUpper(hex.EncodeToString(uc[:]))
	return deviceCode, userCode, nil
}

func storeDeskPending(p deskDevicePending) error {
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ttl := time.Until(time.Unix(p.ExpiresAt, 0))
	if ttl <= 0 {
		ttl = deskDeviceTTL
	}
	if common.RedisEnabled && common.RDB != nil {
		ctx := context.Background()
		if err := common.RDB.Set(ctx, deskDeviceRedisPref+p.DeviceCode, payload, ttl).Err(); err != nil {
			return err
		}
		return common.RDB.Set(ctx, deskUserCodeRedisKey+p.UserCode, p.DeviceCode, ttl).Err()
	}
	deskDeviceMem.Store(p.DeviceCode, p)
	deskUserCodeMem.Store(p.UserCode, p.DeviceCode)
	return nil
}

func loadDeskPendingByDevice(deviceCode string) (deskDevicePending, bool) {
	if common.RedisEnabled && common.RDB != nil {
		ctx := context.Background()
		raw, err := common.RDB.Get(ctx, deskDeviceRedisPref+deviceCode).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				common.SysError("desk device get: " + err.Error())
			}
			return deskDevicePending{}, false
		}
		var p deskDevicePending
		if json.Unmarshal([]byte(raw), &p) != nil {
			return deskDevicePending{}, false
		}
		return p, true
	}
	v, ok := deskDeviceMem.Load(deviceCode)
	if !ok {
		return deskDevicePending{}, false
	}
	p := v.(deskDevicePending)
	return p, true
}

func loadDeskPendingByUserCode(userCode string) (deskDevicePending, bool) {
	userCode = strings.ToUpper(strings.TrimSpace(userCode))
	if common.RedisEnabled && common.RDB != nil {
		ctx := context.Background()
		dc, err := common.RDB.Get(ctx, deskUserCodeRedisKey+userCode).Result()
		if err != nil {
			return deskDevicePending{}, false
		}
		return loadDeskPendingByDevice(dc)
	}
	v, ok := deskUserCodeMem.Load(userCode)
	if !ok {
		return deskDevicePending{}, false
	}
	return loadDeskPendingByDevice(v.(string))
}

func deleteDeskPending(p deskDevicePending) {
	if common.RedisEnabled && common.RDB != nil {
		ctx := context.Background()
		_ = common.RDB.Del(ctx, deskDeviceRedisPref+p.DeviceCode, deskUserCodeRedisKey+p.UserCode).Err()
		return
	}
	deskDeviceMem.Delete(p.DeviceCode)
	deskUserCodeMem.Delete(p.UserCode)
}

// StartDeskDeviceCode — POST /api/v2/desk/device/code (unauthenticated).
func StartDeskDeviceCode(c *gin.Context) {
	deviceCode, userCode, err := newDeskCodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "could not allocate codes"})
		return
	}
	p := deskDevicePending{
		DeviceCode: deviceCode,
		UserCode:   userCode,
		ExpiresAt:  time.Now().Add(deskDeviceTTL).Unix(),
	}
	if err := storeDeskPending(p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "could not store device code"})
		return
	}
	base := deskPublicBase()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"device_code":              deviceCode,
			"user_code":                userCode,
			"verification_uri":         base + "/desk-device",
			"verification_uri_complete": base + "/desk-device?user_code=" + userCode,
			"expires_in":               int(deskDeviceTTL.Seconds()),
			"interval":                 deskDevicePollMin,
		},
	})
}

// ApproveDeskDeviceCode — POST /api/v2/desk/device/approve (session UserAuth).
func ApproveDeskDeviceCode(c *gin.Context) {
	userID := c.GetInt("id")
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "login required"})
		return
	}
	var req struct {
		UserCode string `json:"user_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.UserCode) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "user_code required", "error_code": "MISSING_USER_CODE"})
		return
	}
	p, ok := loadDeskPendingByUserCode(req.UserCode)
	if !ok || p.ExpiresAt < time.Now().Unix() {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown or expired user_code", "error_code": "DEVICE_CODE_EXPIRED"})
		return
	}
	if p.UserID != 0 && p.UserID != userID {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "already bound to another user", "error_code": "DEVICE_CODE_BOUND"})
		return
	}
	owner, err := repo.GetUserById(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "user lookup failed"})
		return
	}
	p.UserID = userID
	p.TenantID = owner.TenantId
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	if err := storeDeskPending(p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "could not bind device code"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "approved; return to Hub Desk"})
}

// PollDeskDeviceToken — POST /api/v2/desk/device/token (unauthenticated).
func PollDeskDeviceToken(c *gin.Context) {
	var req struct {
		DeviceCode string `json:"device_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.DeviceCode) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "device_code required", "error_code": "MISSING_DEVICE_CODE"})
		return
	}
	p, ok := loadDeskPendingByDevice(req.DeviceCode)
	if !ok || p.ExpiresAt < time.Now().Unix() {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "unknown or expired device_code", "error_code": "DEVICE_CODE_EXPIRED"})
		return
	}
	if p.UserID <= 0 {
		c.JSON(http.StatusAccepted, gin.H{"success": false, "message": "authorization_pending", "error_code": "AUTHORIZATION_PENDING"})
		return
	}
	key, name, err := mintOrRotateHubDeskToken(p.UserID, p.TenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	deleteDeskPending(p)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"token":      "sk-" + key,
			"name":       name,
			"expires_at": int64(-1),
			"base_url":   deskPublicBase() + "/v1",
		},
	})
}

func mintOrRotateHubDeskToken(userID int, tenantID string) (key, name string, err error) {
	name = deskTokenName
	var existing repo.Token
	qerr := repo.DB.Where("user_id = ? AND tenant_id = ? AND name = ?", userID, tenantID, name).
		First(&existing).Error
	if qerr == nil && existing.Id > 0 {
		newKey, gerr := app.GenerateTokenKey()
		if gerr != nil {
			return "", "", gerr
		}
		if rerr := existing.RotateKey(newKey); rerr != nil {
			return "", "", rerr
		}
		return newKey, name, nil
	}
	newKey, gerr := app.GenerateTokenKey()
	if gerr != nil {
		return "", "", gerr
	}
	tok := app.BuildCleanToken(userID, tenantID, &repo.Token{
		Name:           name,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "default",
	}, newKey)
	tok.Status = common.TokenStatusEnabled
	if ierr := tok.Insert(); ierr != nil {
		return "", "", fmt.Errorf("insert hubdesk token: %w", ierr)
	}
	return newKey, name, nil
}

// DeskDevicePage — GET /desk-device — tiny approve UI (SSO cookie required for POST).
func DeskDevicePage(c *gin.Context) {
	userCode := c.Query("user_code")
	c.Header("Content-Type", "text/html; charset=utf-8")
	// ASCII-only: WireMessageLanguageGate forbids non-ASCII in c.String/c.JSON.
	c.String(http.StatusOK, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"/><title>Authorize Hub Desk</title>
<style>
body{font-family:system-ui,sans-serif;max-width:28rem;margin:3rem auto;padding:0 1rem;line-height:1.5}
code{background:#f4f4f5;padding:.1rem .3rem;border-radius:4px}
button{padding:.5rem 1rem;font-size:1rem;cursor:pointer}
.err{color:#b91c1c}.ok{color:#15803d}
</style></head><body>
<h1>Authorize Hub Desk</h1>
<p>Confirm to mint an <code>sk-...</code> key for this machine. Platform login is never used as /v1 Bearer.</p>
<label>User code <input id="uc" value="%s" autocomplete="off" style="letter-spacing:.1em;text-transform:uppercase"/></label>
<p><button id="go" type="button">Approve</button></p>
<p id="msg"></p>
<script>
const msg=document.getElementById('msg');
document.getElementById('go').onclick=async()=>{
  msg.textContent='...';
  const r=await fetch('/api/v2/desk/device/approve',{method:'POST',credentials:'include',
    headers:{'Content-Type':'application/json'},
    body:JSON.stringify({user_code:document.getElementById('uc').value.trim()})});
  const j=await r.json().catch(()=>({}));
  if(r.status===401){msg.className='err';msg.textContent='Sign in to Hub first, then return here.';location.href='/api/v2/oauth/login?redirect='+encodeURIComponent(location.pathname+location.search);return;}
  if(!r.ok||!j.success){msg.className='err';msg.textContent=j.message||('HTTP '+r.status);return;}
  msg.className='ok';msg.textContent='Approved. Return to Hub Desk.';
};
</script></body></html>`, htmlEscape(userCode))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
