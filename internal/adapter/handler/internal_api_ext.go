package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var usernameRegexp = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// resolveAccountByZitadelSub resolves an OIDC subject to its lurus-platform
// account. Indirected through a package var (defaulting to the
// gRPC-with-HTTP-fallback resolver) so provisioning tests can stub the platform
// dependency without a live identity service. Production keeps the default.
// NOTE(idp-migration): the underlying gRPC call keeps the GetAccountByZitadelSub
// name because the shared lurus-proto-go stub has no idp_subject RPC yet (renaming
// it would not compile); only the wire body/JSON fields are neutralized. The
// function-name keeps the historical spelling for the same reason.
var resolveAccountByZitadelSub = common.GetAccountByZitadelSubGRPC

// InternalLogin is no longer supported — auth is delegated to the OIDC provider.
// POST /internal/auth/login
func InternalLogin(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"success":    false,
		"message":    "Password-based login is no longer supported. Use OIDC.",
		"error_code": "DEPRECATED",
	})
}

// ===== User CRUD =====

// InternalCreateUser creates a new user via the internal API.
// POST /internal/user
func InternalCreateUser(c *gin.Context) {
	var req struct {
		Username    string `json:"username" binding:"required"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Group       string `json:"group"`
		Quota       int    `json:"quota"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	username := strings.TrimSpace(req.Username)
	if len(username) < 3 || len(username) > 20 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Username must be 3-20 characters",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}
	if !usernameRegexp.MatchString(username) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Username contains invalid characters",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}

	if req.Email != "" && !strings.Contains(req.Email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid email format",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}

	// This endpoint's contract carries no tenant, but the GORM tenant plugin
	// rejects INSERTs into tenant-scoped tables when the context has no tenant
	// ("tenant_id is required for create operations" → spurious 500). Scope the
	// whole handler to the default tenant — the same default used by
	// /internal/user/by-zitadel-sub and /internal/user/provision — so the
	// uniqueness checks and the create all operate within one tenant.
	db := repo.GetTenantDBWithID("default")

	// Idempotency check
	idempotencyKey := c.GetHeader("X-Idempotency-Key")
	if idempotencyKey != "" {
		existing := &repo.User{Username: username}
		if err := db.Where("username = ?", username).First(existing).Error; err == nil && existing.Id > 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"id":           existing.Id,
					"username":     existing.Username,
					"display_name": existing.DisplayName,
					"email":        existing.Email,
					"group":        existing.Group,
					"quota":        existing.Quota,
					"is_duplicate": true,
				},
			})
			return
		}
	}

	var existingCount int64
	db.Model(&repo.User{}).Where("username = ?", username).Count(&existingCount)
	if existingCount > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"success":    false,
			"message":    "Username already exists",
			"error_code": "USER_EXISTS",
		})
		return
	}

	if req.Email != "" {
		var emailCount int64
		db.Model(&repo.User{}).Where("email = ?", req.Email).Count(&emailCount)
		if emailCount > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"success":    false,
				"message":    "Email already exists",
				"error_code": "USER_EXISTS",
			})
			return
		}
	}

	group := req.Group
	if group == "" {
		group = "default"
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = username
	}

	user := &repo.User{
		Username: username,
		// Set on the struct as well as via the tenant-scoped context: the plugin
		// only stamps the column when registered, and the struct must reflect the
		// row that was written.
		TenantId:    "default",
		Email:       req.Email,
		DisplayName: displayName,
		Group:       group,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       req.Quota,
	}

	if err := db.Create(user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create user: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"group":        user.Group,
			"quota":        user.Quota,
		},
	})
}

// InternalDeleteUser deletes a user by ID.
// DELETE /internal/user/:id
func InternalDeleteUser(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	user, err := repo.GetUserById(userId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	if user.Role >= common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "Cannot delete admin/root user",
			"error_code": "FORBIDDEN",
		})
		return
	}

	if err = repo.DeleteUserById(userId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete user: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog("Internal API deleted user " + strconv.Itoa(userId) + " via key: " + keyName)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "User deleted successfully",
	})
}

// InternalGetUserByZitadelSub returns user by OIDC subject ID.
// GET /internal/user/by-zitadel-sub/:sub
func InternalGetUserByZitadelSub(c *gin.Context) {
	sub := c.Param("sub")
	if sub == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "OIDC subject ID is required",
		})
		return
	}

	tenantId := c.DefaultQuery("tenant_id", "default")

	user, mapping, err := repo.GetUserByIDPSubject(sub, tenantId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found for OIDC subject: " + sub,
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":           user.Id,
			"username":     user.Username,
			"display_name": user.DisplayName,
			"email":        user.Email,
			"role":         user.Role,
			"status":       user.Status,
			"group":        user.Group,
			"tenant_id":    user.TenantId,
			"mapping": gin.H{
				"id": mapping.Id,
				// idp_subject is canonical; zitadel_user_id kept for back-compat
				// with consumers not yet migrated. TODO(idp-migration): drop the
				// deprecated key once all consumers read idp_subject.
				"idp_subject":        mapping.IDPSubject,
				"zitadel_user_id":    mapping.IDPSubject,
				"tenant_id":          mapping.TenantID,
				"preferred_username": mapping.PreferredUsername,
			},
		},
	})
}

// ===== User Provisioning =====

// InternalProvisionUser atomically creates a user, identity mapping, and optional initial API token.
// Idempotent: if the OIDC subject already maps to a user, returns the existing user.
// POST /internal/user/provision
func InternalProvisionUser(c *gin.Context) {
	var req struct {
		// IDPSubject is the canonical OIDC subject field (idp_subject). ZitadelSub
		// (zitadel_sub) is accepted for back-compat with callers not yet migrated;
		// see the dual-accept normalization below. Neither carries binding:required
		// because exactly one of the two must be present (validated manually) —
		// gui-switch already sends idp_subject, older clients still send zitadel_sub.
		IDPSubject         string `json:"idp_subject"`
		ZitadelSub         string `json:"zitadel_sub"`
		Email              string `json:"email" binding:"required"`
		DisplayName        string `json:"display_name"`
		TenantID           string `json:"tenant_id"`
		Group              string `json:"group"`
		InitialQuota       int    `json:"initial_quota"`
		CreateInitialToken bool   `json:"create_initial_token"`
		InitialTokenName   string `json:"initial_token_name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	// Dual-accept: prefer the canonical idp_subject; fall back to the deprecated
	// zitadel_sub. Collapse onto req.ZitadelSub so the rest of the handler (which
	// threads that field through) is unchanged.
	if req.IDPSubject != "" {
		req.ZitadelSub = req.IDPSubject
	}
	if req.ZitadelSub == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Missing OIDC subject: provide idp_subject (preferred) or zitadel_sub",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}

	if !strings.Contains(req.Email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid email format",
			"error_code": "VALIDATION_FAILED",
		})
		return
	}

	tenantId := req.TenantID
	if tenantId == "" {
		tenantId = "default"
	}

	// Best-effort resolve the platform account for this OIDC subject so the
	// provisioned user + token link to the unified wallet (the relay settlement
	// path in quota.go engages only when token.IdentityAccountID > 0). FAIL-OPEN:
	// a resolution miss or error must NEVER block provisioning — an identity
	// platform outage cannot be allowed to become a login outage, mirroring every
	// other newhub→platform caller. An unlinked user is created instead and
	// self-heals on the next provision (below) or via the
	// /internal/admin/backfill-token-accounts endpoint.
	var linkedAccountID int64
	if acct, rerr := resolveAccountByZitadelSub(c.Request.Context(), req.ZitadelSub); rerr != nil {
		common.SysLog(fmt.Sprintf("provision: account resolve errored for zitadel_sub=%s — creating UNLINKED user (fail-open): %v", req.ZitadelSub, rerr))
	} else if acct == nil || acct.ID <= 0 {
		common.SysLog(fmt.Sprintf("provision: no platform account for zitadel_sub=%s yet — creating UNLINKED user (fail-open), will self-heal on re-provision", req.ZitadelSub))
	} else {
		linkedAccountID = acct.ID
	}

	// Idempotency: check if mapping already exists
	existingUser, existingMapping, err := repo.GetUserByIDPSubject(req.ZitadelSub, tenantId)
	if err == nil && existingUser != nil {
		// Self-heal: a prior provision may have created this user before its
		// platform account existed (unlinked). If it is now resolvable, backfill
		// the link onto the user and its zero-account tokens so the wallet path
		// engages on the next relay. Best-effort — a backfill hiccup must not
		// fail the idempotent response.
		if linkedAccountID > 0 && existingUser.LurusAccountID == nil {
			if healErr := backfillUserAccountLink(existingUser.Id, linkedAccountID); healErr != nil {
				common.SysLog(fmt.Sprintf("provision: self-heal link failed for user %d (account=%d): %v", existingUser.Id, linkedAccountID, healErr))
			} else {
				common.SysLog(fmt.Sprintf("provision: self-healed link for user %d → account %d", existingUser.Id, linkedAccountID))
			}
		}
		respondExistingProvisionedUser(c, existingUser, existingMapping, tenantId)
		return
	}

	// Derive a safe username from OIDC subject
	username := "u_" + req.ZitadelSub
	if len(username) > 20 {
		username = username[:20]
	}
	// Sanitize: only allow alphanumeric + underscore
	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, username)
	if sanitized == "" {
		sanitized = "u_provisioned"
	}

	group := req.Group
	if group == "" {
		group = "default"
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = strings.Split(req.Email, "@")[0]
	}

	// Begin transaction for atomicity. Scope it to the tenant so the GORM tenant
	// plugin's create callback finds a tenant_id in Statement.Context — a bare
	// repo.DB.Begin() carries no tenant and the three tx.Create calls below
	// (user/mapping/token) fail with "tenant_id is required for create
	// operations" → spurious 500. GetTenantDBWithID stamps the tenant on the
	// DB context and .Begin() inherits Statement.Context, mirroring the verified
	// InternalCreateUser path above.
	tx := repo.GetTenantDBWithID(tenantId).Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to begin transaction: " + tx.Error.Error(),
		})
		return
	}

	// Ensure unique username within tenant
	finalUsername := sanitized
	suffix := 1
	for {
		var count int64
		tx.Model(&repo.User{}).Where("username = ? AND tenant_id = ?", finalUsername, tenantId).Count(&count)
		if count == 0 {
			break
		}
		suffix++
		base := sanitized
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if len(candidate) > 20 {
			base = base[:20-len(fmt.Sprintf("_%d", suffix))]
			candidate = fmt.Sprintf("%s_%d", base, suffix)
		}
		finalUsername = candidate
	}

	// Step 1: Create user
	user := &repo.User{
		Username:    finalUsername,
		TenantId:    tenantId,
		Email:       req.Email,
		DisplayName: displayName,
		Group:       group,
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       req.InitialQuota,
	}
	if linkedAccountID > 0 {
		user.LurusAccountID = &linkedAccountID
	}

	if err := tx.Create(user).Error; err != nil {
		tx.Rollback()
		// Concurrent double-provision: another in-flight request committed the
		// same identity first — the unique index on users.lurus_account_id (when
		// linked) or on the identity mapping fires here. Resolve the race into a
		// clean idempotent response by returning the winner instead of a 500
		// (mirrors zita_bootstrap.go's GetUserByLurusAccountID race probe). If no
		// winner exists the failure was not a race and surfaces as the error.
		if winner, winnerMapping := provisionRaceWinner(req.ZitadelSub, tenantId, linkedAccountID); winner != nil {
			common.SysLog(fmt.Sprintf("provision: lost create race for zitadel_sub=%s, returning winner user %d", req.ZitadelSub, winner.Id))
			respondExistingProvisionedUser(c, winner, winnerMapping, tenantId)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create user: " + err.Error(),
		})
		return
	}

	// Step 2: Create identity mapping
	now := time.Now()
	mapping := &repo.UserIdentityMapping{
		LurusUserID:       user.Id,
		IDPSubject:        req.ZitadelSub,
		TenantID:          tenantId,
		Email:             req.Email,
		DisplayName:       displayName,
		PreferredUsername: finalUsername,
		LastSyncAt:        &now,
		IsActive:          true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := tx.Create(mapping).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create identity mapping: " + err.Error(),
		})
		return
	}

	// Step 3 (optional): Create initial API token
	var tokenData gin.H
	if req.CreateInitialToken {
		tokenName := req.InitialTokenName
		if tokenName == "" {
			tokenName = "Default Key"
		}

		tokenKey, err := app.GenerateTokenKey()
		if err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to generate token key: " + err.Error(),
			})
			return
		}

		token := &repo.Token{
			UserId:         user.Id,
			TenantId:       tenantId,
			Name:           tokenName,
			Key:            tokenKey,
			CreatedTime:    now.Unix(),
			AccessedTime:   now.Unix(),
			Status:         common.TokenStatusEnabled,
			ExpiredTime:    -1,
			RemainQuota:    req.InitialQuota,
			UnlimitedQuota: req.InitialQuota == 0,
			Group:          group,
		}
		if linkedAccountID > 0 {
			// Linked to the unified wallet: attribute relay spend to the platform
			// account (this is what engages quota.go's wallet settlement), and lift
			// the TOKEN-level quota cap so the token's own RemainQuota can't bind a
			// wallet-funded user. NOTE: this does NOT by itself make the wallet the
			// sole gate — the user-level quota check (pre_consume_quota.go:86, on
			// users.quota) is unchanged and still applies. Making the wallet the
			// sole blocking gate is the separate BILLING_UNIFIED_ENABLED enablement
			// gate, which also reconciles user-level quota → wallet balance.
			token.IdentityAccountID = linkedAccountID
			token.UnlimitedQuota = true
		}

		if err := tx.Create(token).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": "Failed to create initial token: " + err.Error(),
			})
			return
		}

		tokenData = gin.H{
			"id":      token.Id,
			"key":     tokenKey,
			"name":    tokenName,
			"warning": "Please save this key - it will not be shown again.",
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to commit transaction: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("Internal API provisioned user %d (zitadel_sub=%s) via key: %s", user.Id, req.ZitadelSub, keyName))

	resp := gin.H{
		"user_id":     user.Id,
		"username":    finalUsername,
		"email":       req.Email,
		"group":       group,
		"tenant_id":   tenantId,
		"is_existing": false,
		"mapping_id":  mapping.Id,
	}
	if tokenData != nil {
		resp["token"] = tokenData
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    resp,
	})
}

// respondExistingProvisionedUser writes the idempotent "already provisioned"
// response shared by the mapping-exists fast path and the create-race winner
// path. mapping may be nil (the race path probes by account and may not re-read
// the mapping) — mapping_id is then 0.
func respondExistingProvisionedUser(c *gin.Context, user *repo.User, mapping *repo.UserIdentityMapping, tenantId string) {
	var mappingID int
	if mapping != nil {
		mappingID = mapping.Id
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":     user.Id,
			"username":    user.Username,
			"email":       user.Email,
			"group":       user.Group,
			"tenant_id":   tenantId,
			"is_existing": true,
			"mapping_id":  mappingID,
		},
	})
}

// backfillUserAccountLink links an existing user to a platform account and
// propagates the account id onto that user's not-yet-linked tokens. Mirrors the
// /internal/admin/backfill-token-accounts batch path for the single-user case.
// Both updates are guarded so this is a no-op once linked (idempotent).
func backfillUserAccountLink(userID int, accountID int64) error {
	// Atomic: user link and token propagation commit together or not at all. A
	// non-transactional two-step would leave the user linked but tokens stranded
	// on a mid-way failure — and the `lurus_account_id IS NULL` guard above would
	// then never re-trigger the heal on retry, permanently orphaning the tokens.
	return repo.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&repo.User{}).
			Where("id = ? AND lurus_account_id IS NULL", userID).
			Update("lurus_account_id", accountID).Error; err != nil {
			return err
		}
		// Set unlimited_quota too, matching the fresh-provision linked-token path
		// (the commit invariant "linked tokens get UnlimitedQuota=true"). Without
		// it a self-healed token keeps its local RemainQuota cap, so a wallet-funded
		// user is both wallet-debited AND 402-stranded once that cap drains.
		return tx.Model(&repo.Token{}).
			Where("user_id = ? AND (identity_account_id = 0 OR identity_account_id IS NULL)", userID).
			Updates(map[string]interface{}{"identity_account_id": accountID, "unlimited_quota": true}).Error
	})
}

// provisionRaceWinner resolves a concurrent-provision race into the request that
// committed first. Prefers the account-link probe when linked (the unique index
// that fires on a same-account race), falling back to the zitadel-sub mapping
// (the authoritative idempotency key). Returns (nil, nil) when no winner exists,
// i.e. the create failure was not a race and should surface as an error.
func provisionRaceWinner(zitadelSub, tenantID string, accountID int64) (*repo.User, *repo.UserIdentityMapping) {
	if accountID > 0 {
		// The unique index on users.lurus_account_id is GLOBAL (cross-tenant), so
		// GetUserByLurusAccountID (which bypasses tenant isolation) can return a
		// user from a DIFFERENT tenant. Only accept it as our race winner when the
		// tenant matches — a cross-tenant account collision is NOT our race, and
		// returning it would leak another tenant's user identity and misroute that
		// user's LLM spend to the foreign account's wallet. On mismatch, fall
		// through to the tenant-scoped sub probe (which finds nothing), so the
		// create error surfaces instead of a false idempotent success.
		if u, err := repo.GetUserByLurusAccountID(accountID); err == nil && u != nil && u.TenantId == tenantID {
			_, m, _ := repo.GetUserByIDPSubject(zitadelSub, tenantID)
			return u, m
		}
	}
	if u, m, err := repo.GetUserByIDPSubject(zitadelSub, tenantID); err == nil && u != nil {
		return u, m
	}
	return nil, nil
}

// ===== Token CRUD =====

// InternalGetUserTokens returns paginated tokens for a user.
// GET /internal/token/user/:id
func InternalGetUserTokens(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid user ID",
		})
		return
	}

	if _, err = repo.GetUserById(userId, false); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	offset := (page - 1) * pageSize
	tokens, err := repo.GetAllUserTokens(userId, offset, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to get tokens: " + err.Error(),
		})
		return
	}

	total, _ := repo.CountUserTokens(userId)

	for _, t := range tokens {
		t.Clean()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tokens":    tokens,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// InternalCreateToken creates a new API token for a user.
// POST /internal/token
func InternalCreateToken(c *gin.Context) {
	var req struct {
		UserId         int    `json:"user_id" binding:"required"`
		Name           string `json:"name" binding:"required"`
		UnlimitedQuota bool   `json:"unlimited_quota"`
		RemainQuota    int    `json:"remain_quota"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	user, err := repo.GetUserById(req.UserId, false)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "User not found",
			"error_code": "USER_NOT_FOUND",
		})
		return
	}

	if user.Status == common.UserStatusDisabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success":    false,
			"message":    "User is disabled",
			"error_code": "USER_DISABLED",
		})
		return
	}

	idempotencyKey := c.GetHeader("X-Idempotency-Key")
	if idempotencyKey != "" {
		var existing repo.Token
		if err := repo.DB.Where("user_id = ? AND name = ?", req.UserId, req.Name).First(&existing).Error; err == nil && existing.Id > 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"id":           existing.Id,
					"name":         existing.Name,
					"is_duplicate": true,
				},
			})
			return
		}
	}

	// Generate a unique token key (BUG FIX: was missing before)
	tokenKey, err := app.GenerateTokenKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to generate token key: " + err.Error(),
		})
		return
	}

	token := &repo.Token{
		UserId:         req.UserId,
		TenantId:       user.TenantId,
		Name:           req.Name,
		Key:            tokenKey,
		UnlimitedQuota: req.UnlimitedQuota,
		RemainQuota:    req.RemainQuota,
		CreatedTime:    time.Now().Unix(),
		AccessedTime:   time.Now().Unix(),
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		Group:          user.Group,
	}

	if err = token.Insert(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to create token: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog("Internal API created token for user " + strconv.Itoa(req.UserId) + " via key: " + keyName)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":      token.Id,
			"key":     tokenKey,
			"name":    token.Name,
			"warning": "Please save this key - it will not be shown again.",
		},
	})
}

// InternalGetToken returns a single token by ID (key field is redacted).
// GET /internal/token/:id
func InternalGetToken(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	token, err := repo.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Token not found",
			"error_code": "TOKEN_NOT_FOUND",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":                   token.Id,
			"user_id":              token.UserId,
			"tenant_id":            token.TenantId,
			"name":                 token.Name,
			"status":               token.Status,
			"created_time":         token.CreatedTime,
			"accessed_time":        token.AccessedTime,
			"expired_time":         token.ExpiredTime,
			"remain_quota":         token.RemainQuota,
			"used_quota":           token.UsedQuota,
			"unlimited_quota":      token.UnlimitedQuota,
			"model_limits_enabled": token.ModelLimitsEnabled,
			"model_limits":         token.GetModelLimits(),
			"allow_ips":            token.AllowIps,
			"group":                token.Group,
			"cross_group_retry":    token.CrossGroupRetry,
		},
	})
}

// InternalGetTokenUsage returns usage statistics for a token.
// GET /internal/token/:id/usage
func InternalGetTokenUsage(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	token, err := repo.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Token not found",
			"error_code": "TOKEN_NOT_FOUND",
		})
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"token_id":        token.Id,
			"name":            token.Name,
			"status":          token.Status,
			"total_granted":   token.RemainQuota + token.UsedQuota,
			"total_used":      token.UsedQuota,
			"total_available": token.RemainQuota,
			"unlimited_quota": token.UnlimitedQuota,
			"expires_at":      expiredAt,
			"last_accessed":   token.AccessedTime,
		},
	})
}

// InternalUpdateToken updates a token's properties.
// PUT /internal/token/:id
func InternalUpdateToken(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	token, err := repo.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Token not found",
			"error_code": "TOKEN_NOT_FOUND",
		})
		return
	}

	var req struct {
		Name               *string `json:"name"`
		Status             *int    `json:"status"`
		ExpiredTime        *int64  `json:"expired_time"`
		RemainQuota        *int    `json:"remain_quota"`
		UnlimitedQuota     *bool   `json:"unlimited_quota"`
		ModelLimitsEnabled *bool   `json:"model_limits_enabled"`
		ModelLimits        *string `json:"model_limits"`
		AllowIps           *string `json:"allow_ips"`
		Group              *string `json:"group"`
		CrossGroupRetry    *bool   `json:"cross_group_retry"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request: " + err.Error(),
		})
		return
	}

	if req.Name != nil {
		if err := app.ValidateTokenName(*req.Name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    err.Error(),
				"error_code": "VALIDATION_FAILED",
			})
			return
		}
		token.Name = *req.Name
	}
	if req.Status != nil {
		token.Status = *req.Status
	}
	if req.ExpiredTime != nil {
		token.ExpiredTime = *req.ExpiredTime
	}
	if req.RemainQuota != nil {
		token.RemainQuota = *req.RemainQuota
	}
	if req.UnlimitedQuota != nil {
		token.UnlimitedQuota = *req.UnlimitedQuota
	}
	if req.ModelLimitsEnabled != nil {
		token.ModelLimitsEnabled = *req.ModelLimitsEnabled
	}
	if req.ModelLimits != nil {
		token.ModelLimits = *req.ModelLimits
	}
	if req.AllowIps != nil {
		token.AllowIps = req.AllowIps
	}
	if req.Group != nil {
		token.Group = *req.Group
	}
	if req.CrossGroupRetry != nil {
		token.CrossGroupRetry = *req.CrossGroupRetry
	}

	if err := token.Update(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to update token: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("Internal API updated token %d via key: %s", tokenId, keyName))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token updated successfully",
		"data": gin.H{
			"id":     token.Id,
			"name":   token.Name,
			"status": token.Status,
		},
	})
}

// InternalDeleteToken deletes (revokes) a token by ID.
// DELETE /internal/token/:id
func InternalDeleteToken(c *gin.Context) {
	tokenId, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenId <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid token ID",
		})
		return
	}

	token, err := repo.GetTokenById(tokenId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success":    false,
			"message":    "Token not found",
			"error_code": "TOKEN_NOT_FOUND",
		})
		return
	}

	if err := token.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "Failed to delete token: " + err.Error(),
		})
		return
	}

	keyName := c.GetString("internal_api_key_name")
	common.SysLog(fmt.Sprintf("Internal API deleted token %d (user=%d) via key: %s", tokenId, token.UserId, keyName))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token deleted successfully",
	})
}
