package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Platform-driven per-account, per-product key issuance (migration 049).
//
//	POST   /internal/v1/provisioning/accounts/:account_id/keys         create (idempotent)
//	POST   /internal/v1/provisioning/accounts/:account_id/keys/rotate  rotate
//	DELETE /internal/v1/provisioning/accounts/:account_id/keys         revoke
//	GET    /internal/v1/provisioning/accounts/:account_id/keys         list
//
// One live key per (account, product): the partial unique index on
// account_key_bindings is the cross-replica backstop, accountKeyCreateMu only
// keeps the in-process common case from burning a user/token insert.
var accountKeyCreateMu sync.Mutex

type accountKeyReq struct {
	Product    string   `json:"product"`
	Name       string   `json:"name"`
	TenantSlug string   `json:"tenant_slug"`
	Quota      int      `json:"quota"`
	Models     []string `json:"models"`
	ExpiresAt  int64    `json:"expires_at"`
}

func parseAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("account_id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid account_id", "error_code": "INVALID_ACCOUNT_ID"})
		return 0, false
	}
	return id, true
}

// normalizeProduct validates product against the relay allow-list.
func normalizeProduct(p string) (string, bool) {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" || len(p) > 32 || !ratio_setting.IsAllowedSourceProduct(p) {
		return "", false
	}
	return p, true
}

func maskedKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

func accountKeyMeta(b *repo.AccountKeyBinding, t *repo.Token) gin.H {
	h := gin.H{
		"account_id": b.IdentityAccountID,
		"product":    b.Product,
		"token_id":   b.TokenId,
		"tenant_id":  b.TenantId,
		"created_at": b.CreatedAt,
	}
	if t != nil {
		h["name"] = t.Name
		h["status"] = t.Status
		h["expires_at"] = t.ExpiredTime
		h["unlimited_quota"] = t.UnlimitedQuota
		h["remain_quota"] = t.RemainQuota
		h["used_quota"] = t.UsedQuota
		h["last_used_at"] = t.LastUsedAt
		h["key_masked"] = maskedKey(t.Key)
	}
	return h
}

func accountKeyAudit(c *gin.Context, action string, tokenID int64, accountID int64, product string) {
	apiKeyName := c.GetString("internal_api_key_name")
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorSystem, 0,
		action, governance.ResourceToken, int(tokenID),
		fmt.Sprintf(`{"account_id":%d,"product":%q,"key_name":%q}`, accountID, product, apiKeyName)))
}

func callerKey(c *gin.Context) *repo.InternalApiKey {
	v, _ := c.Get("internal_api_key")
	k, _ := v.(*repo.InternalApiKey)
	return k
}

// bindingTenantAllowed enforces the same cross-tenant guard as the tenant
// provisioning endpoints against the binding's tenant.
func bindingTenantAllowed(c *gin.Context, tenantID string) bool {
	if repo.InternalKeyAllowedForTenant(callerKey(c), tenantID) {
		return true
	}
	c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "API key not authorized for this tenant", "error_code": "TENANT_NOT_AUTHORIZED"})
	return false
}

// CreateAccountKey is POST .../accounts/:account_id/keys.
func CreateAccountKey(c *gin.Context) {
	accountID, ok := parseAccountID(c)
	if !ok {
		return
	}
	var req accountKeyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request: " + err.Error(), "error_code": "VALIDATION_FAILED"})
		return
	}
	product, ok := normalizeProduct(req.Product)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "unknown or missing product", "error_code": "UNKNOWN_PRODUCT"})
		return
	}
	if req.Quota < 0 || len(req.Models) > 100 || len(req.Name) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid quota/models/name", "error_code": "VALIDATION_FAILED"})
		return
	}
	idemKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if idemKey == "" {
		idemKey = strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
	}
	if len(idemKey) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Idempotency-Key too long", "error_code": "VALIDATION_FAILED"})
		return
	}

	accountKeyCreateMu.Lock()
	defer accountKeyCreateMu.Unlock()

	if existing, err := repo.GetLiveAccountKeyBinding(accountID, product); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "binding lookup failed: " + err.Error()})
		return
	} else if existing != nil {
		respondExistingAccountKey(c, existing)
		return
	}

	// Resolve the target tenant.
	tenantID := "default"
	if slug := strings.TrimSpace(req.TenantSlug); slug != "" {
		tenant, err := repo.GetTenantBySlug(slug)
		if err != nil || tenant == nil {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Tenant not found", "error_code": "TENANT_NOT_FOUND"})
			return
		}
		tenantID = tenant.Id
	}
	// Ensure the account's newhub user (the bridge auto-create path; the wallet
	// link is users.lurus_account_id).
	user, err := repo.GetUserByLurusAccountID(accountID)
	if err != nil || user == nil {
		if !bindingTenantAllowed(c, tenantID) {
			return
		}
		user, err = autoCreateBridgedUser(accountID, tenantID)
		if err != nil {
			var se *seatLimitError
			if errors.As(err, &se) {
				c.JSON(http.StatusConflict, gin.H{"success": false, "message": se.Error(), "error_code": "TENANT_SEAT_LIMIT"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "failed to ensure user: " + err.Error()})
			return
		}
	} else {
		if req.TenantSlug != "" && user.TenantId != tenantID {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": "account already belongs to another tenant", "error_code": "TENANT_MISMATCH"})
			return
		}
		tenantID = user.TenantId
	}
	if !bindingTenantAllowed(c, tenantID) {
		return
	}

	tokenKey, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to generate token key"})
		return
	}
	expired := int64(-1)
	if req.ExpiresAt > 0 {
		expired = req.ExpiresAt
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = product
	}
	now := time.Now().Unix()
	creator := 0
	if k := callerKey(c); k != nil {
		creator = k.CreatedBy
	}
	token := &repo.Token{
		UserId:            user.Id,
		TenantId:          tenantID,
		Name:              name,
		Key:               tokenKey,
		CreatedTime:       now,
		AccessedTime:      now,
		Status:            common.TokenStatusEnabled,
		ExpiredTime:       expired,
		Group:             "default",
		CreatorUserId:     creator,
		IdentityAccountID: accountID,
		SourceProduct:     product,
		UnlimitedQuota:    req.Quota == 0,
		RemainQuota:       req.Quota,
	}
	if len(req.Models) > 0 {
		token.ModelLimitsEnabled = true
		token.ModelLimits = strings.Join(req.Models, ",")
	}
	binding := &repo.AccountKeyBinding{
		IdentityAccountID: accountID, Product: product, TenantId: tenantID,
		IdempotencyKey: idemKey, CreatedAt: now,
	}

	txErr := repo.DB.Transaction(func(tx *gorm.DB) error {
		if err := repo.WithTenantID(tx, tenantID).Create(token).Error; err != nil {
			return err
		}
		binding.TokenId = int64(token.Id)
		return tx.Create(binding).Error
	})
	if txErr != nil {
		// Lost a cross-replica race: the unique index fired. Return the winner.
		if winner, _ := repo.GetLiveAccountKeyBinding(accountID, product); winner != nil {
			respondExistingAccountKey(c, winner)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create key: " + txErr.Error()})
		return
	}

	accountKeyAudit(c, governance.ActionTokenCreated, binding.TokenId, accountID, product)
	data := accountKeyMeta(binding, token)
	data["key"] = tokenKey
	data["is_existing"] = false
	data["warning"] = "Please save this key - it will not be shown again."
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": data})
}

func respondExistingAccountKey(c *gin.Context, b *repo.AccountKeyBinding) {
	if !bindingTenantAllowed(c, b.TenantId) {
		return
	}
	t, _ := repo.GetTokenUnscopedTenant(b.TokenId)
	data := accountKeyMeta(b, t)
	data["is_existing"] = true
	data["hint"] = "plaintext key is only returned on first creation; use rotate to obtain a new key"
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// bindingFromRequest loads the live binding for the account and the product in
// the query string (?product=) or JSON body.
func bindingFromRequest(c *gin.Context, accountID int64) (*repo.AccountKeyBinding, *repo.Token, string, bool) {
	raw := c.Query("product")
	if raw == "" {
		var body accountKeyReq
		_ = c.ShouldBindJSON(&body)
		raw = body.Product
	}
	product, ok := normalizeProduct(raw)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "unknown or missing product", "error_code": "UNKNOWN_PRODUCT"})
		return nil, nil, "", false
	}
	b, err := repo.GetLiveAccountKeyBinding(accountID, product)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "binding lookup failed: " + err.Error()})
		return nil, nil, "", false
	}
	if b == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "no key bound for this account/product", "error_code": "KEY_NOT_FOUND"})
		return nil, nil, "", false
	}
	if !bindingTenantAllowed(c, b.TenantId) {
		return nil, nil, "", false
	}
	t, err := repo.GetTokenUnscopedTenant(b.TokenId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Token vanished (deleted elsewhere): drop the stale binding.
			_ = repo.DB.Delete(b).Error
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "no key bound for this account/product", "error_code": "KEY_NOT_FOUND"})
			return nil, nil, "", false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return nil, nil, "", false
	}
	return b, t, product, true
}

// RotateAccountKey is POST .../accounts/:account_id/keys/rotate.
func RotateAccountKey(c *gin.Context) {
	accountID, ok := parseAccountID(c)
	if !ok {
		return
	}
	b, t, product, ok := bindingFromRequest(c, accountID)
	if !ok {
		return
	}
	newKey, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to generate token key"})
		return
	}
	if err := t.RotateKey(newKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to rotate key: " + err.Error()})
		return
	}
	accountKeyAudit(c, governance.ActionAuthTokenRotated, b.TokenId, accountID, product)
	data := accountKeyMeta(b, t)
	data["key"] = newKey
	data["warning"] = "Please save this key - it will not be shown again. The previous key is no longer valid."
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// RevokeAccountKey is DELETE .../accounts/:account_id/keys?product=.
func RevokeAccountKey(c *gin.Context) {
	accountID, ok := parseAccountID(c)
	if !ok {
		return
	}
	b, t, product, ok := bindingFromRequest(c, accountID)
	if !ok {
		return
	}
	if err := t.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to revoke key: " + err.Error()})
		return
	}
	if err := repo.DB.Delete(b).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to release binding: " + err.Error()})
		return
	}
	accountKeyAudit(c, governance.ActionTokenDeleted, b.TokenId, accountID, product)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Key revoked"})
}

// ListAccountKeys is GET .../accounts/:account_id/keys.
func ListAccountKeys(c *gin.Context) {
	accountID, ok := parseAccountID(c)
	if !ok {
		return
	}
	bs, err := repo.ListLiveAccountKeyBindings(accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(bs))
	for i := range bs {
		if !repo.InternalKeyAllowedForTenant(callerKey(c), bs[i].TenantId) {
			continue
		}
		t, _ := repo.GetTokenUnscopedTenant(bs[i].TokenId)
		items = append(items, accountKeyMeta(&bs[i], t))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "total": len(items)}})
}
