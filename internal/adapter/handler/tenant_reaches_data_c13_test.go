package handler

// tenant_reaches_data_c13_test.go — cycle-13 L9: the tenant named in the URL
// has to reach the data.
//
// Three families of oracle live here:
//
//  1. ProvisionV2 writes the bridged user (and therefore the relay token it
//     mints) into the tenant the URL resolved, not the literal "default";
//     an account already pinned to another tenant is refused instead of
//     being handed a token that bills someone else's pool.
//  2. A suspended tenant is locked out of every Switch-facing surface that
//     authenticates by raw relay token (user/info, user/topup, heartbeat,
//     reconciliation) and of the cookie arm of the OIDC-authenticated
//     credit-pool readback. Each cell is served by a distinct gate site, so
//     deleting any one of them reddens only its own cell.
//  3. The tenant-wide project spend report carries the same admin gate its
//     sibling tenant-wide aggregate (/logs/stat/all) carries.
//
// The fixtures drive the real handlers through real routers (the raw-token
// handlers with no middleware, exactly as api-v2-router.go mounts them; the
// credit-pool route through the real middleware.OIDCAuth session arm), and
// the assertions read rows back out of the database rather than trusting the
// response envelope alone.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// ProvisionV2 — the slug in the URL is the tenant the account lands in.
// ---------------------------------------------------------------------------

// TestProvisionV2_FirstUserLandsInURLTenant: a platform account with no hub
// user yet, provisioning through /api/v2/<slug>/provision, gets a user AND a
// relay token stamped with that slug's tenant. Before cycle-13 L9 both said
// "default", so every switch-provisioned customer's relay spend was attributed
// to (and drawn from) the bootstrap tenant no matter which slug they called.
func TestProvisionV2_FirstUserLandsInURLTenant(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991301", map[string]string{"plan_code": "cc_pro", "quota": "120000"}))

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", code, resp)
	}

	user, err := repo.GetUserByLurusAccountID(991301)
	if err != nil {
		t.Fatalf("bridged user not created: %v", err)
	}
	if user.TenantId != ctx.TenantID {
		t.Errorf("user.tenant_id = %q, want %q — the account must land in the tenant the URL resolved",
			user.TenantId, ctx.TenantID)
	}

	var row repo.Token
	if err := repo.DB.Where("user_id = ? AND name = ?", user.Id, "switch-provision-cc_pro").
		First(&row).Error; err != nil {
		t.Fatalf("relay token not found: %v", err)
	}
	if row.TenantId != ctx.TenantID {
		t.Errorf("token.tenant_id = %q, want %q — relay spend on this key debits the token's tenant pool",
			row.TenantId, ctx.TenantID)
	}
}

// TestProvisionV2_ExistingUserOfAnotherTenantRefused: the account already has
// a hub user in tenant B; calling tenant A's provision URL must not hand back
// a working key for B (which is what returning B's token silently did), and
// the refusal leaves a durable row naming the reason.
func TestProvisionV2_ExistingUserOfAnotherTenantRefused(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)
	if err := ctx.DB.AutoMigrate(&entity.AuditEvent{}, &entity.AuditChainHead{}); err != nil {
		t.Fatalf("migrate audit tables: %v", err)
	}
	pinAuditWriter(t, ctx.DB)

	other := &repo.Tenant{
		Id: "c13-beta", Slug: "c13-beta", Name: "C13 Beta",
		IDPOrgID: "org_c13_beta", Status: repo.TenantStatusEnabled,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("create other tenant: %v", err)
	}
	accountID := int64(991302)
	existing := &repo.User{
		Username: "lurus_991302", DisplayName: "C13 Cross Tenant",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Group: "default", TenantId: other.Id, LurusAccountID: &accountID,
	}
	if err := ctx.DB.Create(existing).Error; err != nil {
		t.Fatalf("create existing user: %v", err)
	}

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991302", map[string]string{"plan_code": "cc_pro", "quota": "120000"}))

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — the account belongs to %q, the URL asked for %q; body=%v",
			code, other.Id, ctx.TenantID, resp)
	}
	if resp["error_code"] != "TENANT_MISMATCH" {
		t.Errorf("error_code = %v, want TENANT_MISMATCH; body=%v", resp["error_code"], resp)
	}

	var minted int64
	if err := repo.DB.Model(&repo.Token{}).Where("user_id = ?", existing.Id).Count(&minted).Error; err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if minted != 0 {
		t.Errorf("tokens minted for the cross-tenant account = %d, want 0", minted)
	}

	var events []entity.AuditEvent
	if err := ctx.DB.Where("action = ? AND resource = ?",
		governance.ActionAuthFailed, governance.ResourceTenant).Find(&events).Error; err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit rows action=%s resource=%s = %d, want exactly 1 — the refusal's only durable record",
			governance.ActionAuthFailed, governance.ResourceTenant, len(events))
	}
	var details map[string]interface{}
	if err := json.Unmarshal([]byte(events[0].Details), &details); err != nil {
		t.Fatalf("parse audit details %q: %v", events[0].Details, err)
	}
	if details["reason"] != "tenant_mismatch" {
		t.Errorf("audit details reason = %v, want tenant_mismatch (details=%v)", details["reason"], details)
	}
	if details["tenant_id"] != ctx.TenantID || details["user_tenant_id"] != other.Id {
		t.Errorf("audit details = %v, want tenant_id=%q user_tenant_id=%q", details, ctx.TenantID, other.Id)
	}
}

// TestProvisionV2_SuspendedTenantRefused: provisioning into a suspended tenant
// creates nothing. Without the gate the endpoint happily minted a relay key
// for a tenant an operator had just locked.
func TestProvisionV2_SuspendedTenantRefused(t *testing.T) {
	r, ctx, key := setupProvisionTest(t)
	if err := ctx.DB.Model(&repo.Tenant{}).Where("id = ?", ctx.TenantID).
		Update("status", repo.TenantStatusSuspended).Error; err != nil {
		t.Fatalf("suspend tenant: %v", err)
	}

	tok := provSignRS256(t, key, "test-kid",
		provClaims("991303", map[string]string{"plan_code": "cc_pro", "quota": "120000"}))

	code, resp := provisionReq(t, r, ctx.TenantID, map[string]any{"entitlement_token": tok})
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a suspended tenant; body=%v", code, resp)
	}
	if resp["error_code"] != "TENANT_DISABLED" {
		t.Errorf("error_code = %v, want TENANT_DISABLED; body=%v", resp["error_code"], resp)
	}
	if _, err := repo.GetUserByLurusAccountID(991303); err == nil {
		t.Error("a user was provisioned into a suspended tenant despite the 403")
	}
}

// ---------------------------------------------------------------------------
// Suspended tenant vs the Switch-facing surfaces.
// ---------------------------------------------------------------------------

var c13ReachDBCounter atomic.Int64

type c13ReachCtx struct {
	router *gin.Engine
	db     *gorm.DB
	tenant *repo.Tenant
	user   *repo.User
	token  *repo.Token
}

// setupC13TenantReach wires the four raw-token Switch routes exactly as
// api-v2-router.go mounts them (no auth middleware — each handler does its own
// inline Token.Key lookup) plus the credit-pool readback behind the REAL
// middleware.OIDCAuth, driven by a session cookie so the session-fallback arm
// is the one under test.
func setupC13TenantReach(t *testing.T) *c13ReachCtx {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := fmt.Sprintf("file:c13reach%d?mode=memory&cache=shared", c13ReachDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	tables := []interface{}{
		&repo.User{}, &repo.Token{}, &repo.Tenant{}, &repo.Redemption{}, &repo.Log{},
		&entity.TenantCreditPool{}, &entity.TenantCreditPoolDraw{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	t.Cleanup(func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		if sqlDB, _ := db.DB(); sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	slug := fmt.Sprintf("c13reach%d", c13ReachDBCounter.Load())
	tenant := &repo.Tenant{
		Id: slug, Slug: slug, Name: "C13 Reach",
		IDPOrgID: "org_" + slug, Status: repo.TenantStatusEnabled,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(tenant).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user := &repo.User{
		Username: "c13reachuser" + slug, DisplayName: "C13 Reach User",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Email: "c13reach@test.local", Group: "default",
		TenantId: tenant.Id, Quota: 500_000,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := &repo.Token{
		UserId: user.Id, TenantId: tenant.Id, Key: common.GetRandomString(32),
		Status: common.TokenStatusEnabled, Name: "c13-reach-token",
		CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(),
		ExpiredTime: -1, RemainQuota: 200_000, Group: "default",
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	// OIDCAuth refuses everything when OIDC is off, so switch it on against a
	// local JWKS stub (the session arm never fetches a key, but InitOIDCAuth
	// builds the manager eagerly) and switch it back off afterwards so the
	// rest of the binary sees the state it had.
	//
	// The stub serves the package's shared callback key, not an empty set:
	// middleware's jwksManager is created once per test binary (sync.Once),
	// so whichever test calls InitOIDCAuth first seeds the key cache every
	// later test verifies against. An empty set here, run first under
	// -shuffle, left all 11 OIDC callback/claim tests failing with "public
	// key not found for kid" (CI seed 1790172913506686629). Every harness in
	// this package must serve the same key under the same kid — see
	// oidcCallbackSharedKey.
	sharedJWKS := middleware.JWKSet{Keys: []middleware.JWK{
		rsaPublicKeyToJWKForCallbackTest(&oidcCallbackSharedKey(t).PublicKey),
	}}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sharedJWKS)
	}))
	t.Cleanup(jwks.Close)
	t.Setenv("OIDC_ENABLED", "true")
	t.Setenv("OIDC_ISSUER", jwks.URL)
	t.Setenv("OIDC_JWKS_URI", jwks.URL+"/jwks")
	t.Setenv("OIDC_CLIENT_ID", "c13-reach-client")
	if err := middleware.InitOIDCAuth(); err != nil {
		t.Fatalf("InitOIDCAuth: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("OIDC_ENABLED", "false")
		_ = middleware.InitOIDCAuth()
	})

	router := gin.New()
	router.Use(sessions.Sessions("c13reachsession", cookie.NewStore([]byte("c13-reach-secret"))))
	// Logged-in browser session for `user`: this is what the cookie arm of
	// OIDCAuth (handleSessionFallback) reads.
	router.Use(func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("id", user.Id)
		s.Set("oauth_access_token", "c13-reach-session-token")
		s.Set("oauth_token_expires_at", time.Now().Add(time.Hour).Unix())
		_ = s.Save()
		c.Next()
	})
	apiV2 := router.Group("/api/v2")
	apiV2.GET("/switch/user/info", GetSwitchUserInfo)
	apiV2.POST("/switch/user/topup", SwitchUserTopup)
	apiV2.POST("/switch/heartbeat", UserHeartbeat)
	apiV2.POST("/switch/reconciliation", SwitchReconciliation)
	apiV2.POST("/switch/redeem", SwitchRedeemAnonymous)
	apiV2.GET("/:tenant_slug/credit-pool/me", middleware.OIDCAuth(), GetCreditPoolForEndUser)

	return &c13ReachCtx{router: router, db: db, tenant: tenant, user: user, token: token}
}

func (c *c13ReachCtx) do(t *testing.T, method, path string, body string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if withAuth {
		req.Header.Set("Authorization", c.token.Key)
	}
	w := httptest.NewRecorder()
	c.router.ServeHTTP(w, req)
	return w
}

func (c *c13ReachCtx) setStatus(t *testing.T, status int) {
	t.Helper()
	if err := c.db.Model(&repo.Tenant{}).Where("id = ?", c.tenant.Id).
		Update("status", status).Error; err != nil {
		t.Fatalf("set tenant status %d: %v", status, err)
	}
}

func (c *c13ReachCtx) seedCode(t *testing.T, key string, quota int) {
	t.Helper()
	r := &repo.Redemption{
		TenantId: c.tenant.Id, Key: key, Name: "c13-reach-code", Quota: quota,
		Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(),
	}
	if err := c.db.Create(r).Error; err != nil {
		t.Fatalf("seed redemption: %v", err)
	}
}

func (c *c13ReachCtx) codeStatus(t *testing.T, key string) int {
	t.Helper()
	var row repo.Redemption
	if err := c.db.Where("key = ?", key).First(&row).Error; err != nil {
		t.Fatalf("read redemption %q: %v", key, err)
	}
	return row.Status
}

func (c *c13ReachCtx) userQuota(t *testing.T) int {
	t.Helper()
	var row repo.User
	if err := c.db.Where("id = ?", c.user.Id).First(&row).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	return row.Quota
}

// c13Message pulls the top-level message out of a response envelope.
func c13Message(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v raw=%s", err, w.Body.String())
	}
	msg, _ := env["message"].(string)
	return msg
}

// c13ErrorCode pulls the top-level error_code out of a response envelope.
func c13ErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v raw=%s", err, w.Body.String())
	}
	code, _ := env["error_code"].(string)
	return code
}

// TestSuspendedTenantCannotReachSwitchSurfaces is the table the plan asks for:
// with the tenant enabled every surface answers as it does today; with the
// tenant suspended every surface refuses, the redemption code survives
// unspent, and no balance moves. Flipping the tenant back restores today's
// behaviour, so the gate reads the current status rather than latching.
func TestSuspendedTenantCannotReachSwitchSurfaces(t *testing.T) {
	c := setupC13TenantReach(t)
	const bodyHeartbeat = `{"fingerprint":"c13-fp","app_version":"1.0.0"}`
	const bodyReconcile = `{"start_time":0,"end_time":0}`

	// ---- enabled: today's behaviour ------------------------------------
	c.seedCode(t, "C13REACHCODEENABLED00000000000AA", 5_000)
	quotaBefore := c.userQuota(t)

	if w := c.do(t, http.MethodGet, "/api/v2/switch/user/info", "", true); w.Code != http.StatusOK {
		t.Fatalf("enabled user/info: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w := c.do(t, http.MethodPost, "/api/v2/switch/user/topup",
		`{"key":"C13REACHCODEENABLED00000000000AA"}`, true); w.Code != http.StatusOK {
		t.Fatalf("enabled topup: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := c.userQuota(t); got != quotaBefore+5_000 {
		t.Fatalf("enabled topup credited %d, want %d", got-quotaBefore, 5_000)
	}
	w := c.do(t, http.MethodPost, "/api/v2/switch/heartbeat", bodyHeartbeat, true)
	if w.Code != http.StatusOK {
		t.Fatalf("enabled heartbeat: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := decodeHeartbeat(t, w).Data.Status; got != heartbeatStatusActive {
		t.Fatalf("enabled heartbeat status = %q, want active", got)
	}
	if w := c.do(t, http.MethodPost, "/api/v2/switch/reconciliation", bodyReconcile, true); w.Code != http.StatusOK {
		t.Fatalf("enabled reconciliation: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w := c.do(t, http.MethodGet, "/api/v2/"+c.tenant.Slug+"/credit-pool/me", "", false); w.Code != http.StatusOK {
		t.Fatalf("enabled credit-pool/me: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	// ---- suspended: every surface refuses -------------------------------
	c.setStatus(t, repo.TenantStatusSuspended)
	c.seedCode(t, "C13REACHCODESUSPENDED0000000000B", 7_000)
	quotaBefore = c.userQuota(t)

	t.Run("suspended/user_info", func(t *testing.T) {
		w := c.do(t, http.MethodGet, "/api/v2/switch/user/info", "", true)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
		}
		if got := c13ErrorCode(t, w); got != "TENANT_DISABLED" {
			t.Errorf("error_code = %q, want TENANT_DISABLED; body=%s", got, w.Body.String())
		}
	})

	t.Run("suspended/user_topup", func(t *testing.T) {
		w := c.do(t, http.MethodPost, "/api/v2/switch/user/topup",
			`{"key":"C13REACHCODESUSPENDED0000000000B"}`, true)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
		}
		// The machine-readable half of the refusal. The Switch client's
		// billing caller (internal/billing/client.go doRequest) does not
		// classify the sentence, so without this field a suspended tenant is
		// indistinguishable from "the code was rejected" on the wire.
		if got := c13ErrorCode(t, w); got != "TENANT_DISABLED" {
			t.Errorf("error_code = %q, want TENANT_DISABLED; body=%s", got, w.Body.String())
		}
		// The human half: the same sentence the anonymous redeem path answers
		// with for the same condition, so the customer reads one wording.
		if got := c13Message(t, w); got != switchTenantSuspendedMessage {
			t.Errorf("message = %q, want the shared suspended-reseller sentence %q",
				got, switchTenantSuspendedMessage)
		}
		if got := c.codeStatus(t, "C13REACHCODESUSPENDED0000000000B"); got != common.RedemptionCodeStatusEnabled {
			t.Errorf("redemption status = %d, want %d — a refused topup must not burn the code",
				got, common.RedemptionCodeStatusEnabled)
		}
		if got := c.userQuota(t); got != quotaBefore {
			t.Errorf("user quota = %d, want %d — nothing may be credited into a suspended tenant",
				got, quotaBefore)
		}
	})

	t.Run("suspended/heartbeat", func(t *testing.T) {
		w := c.do(t, http.MethodPost, "/api/v2/switch/heartbeat", bodyHeartbeat, true)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
		}
		if got := c13ErrorCode(t, w); got != "TENANT_DISABLED" {
			t.Errorf("error_code = %q, want TENANT_DISABLED; body=%s", got, w.Body.String())
		}
		if got := decodeHeartbeat(t, w).Data.Status; got != heartbeatStatusSuspended {
			t.Errorf("heartbeat data.status = %q, want %q — a suspended tenant must not read as active",
				got, heartbeatStatusSuspended)
		}
	})

	t.Run("suspended/reconciliation", func(t *testing.T) {
		w := c.do(t, http.MethodPost, "/api/v2/switch/reconciliation", bodyReconcile, true)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
		}
		if got := c13ErrorCode(t, w); got != "TENANT_DISABLED" {
			t.Errorf("error_code = %q, want TENANT_DISABLED; body=%s", got, w.Body.String())
		}
	})

	// The anonymous activation path takes the same decision inline rather
	// than through repo.TenantGate (it has no token or session, so the tenant
	// comes from the redemption row, and it refuses the bootstrap "default"
	// tenant outright where the gate would exempt it). That inline arm is the
	// reason switch_tenant_gate_completeness_test.go exempts this handler, so
	// the exemption gets an oracle rather than a promise.
	t.Run("suspended/redeem_anonymous", func(t *testing.T) {
		c.seedCode(t, "C13REACHCODEREDEEMSUSPENDED000CC", 9_000)
		w := c.do(t, http.MethodPost, "/api/v2/switch/redeem",
			`{"code":"C13REACHCODEREDEEMSUSPENDED000CC","fingerprint":"c13-reach-fp"}`, false)
		// This endpoint reports business failures as 200 + success:false (the
		// Switch client classifies on the sentence) — do-not-regress.
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (envelope-level failure); body=%s", w.Code, w.Body.String())
		}
		var env map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v raw=%s", err, w.Body.String())
		}
		if ok, _ := env["success"].(bool); ok {
			t.Fatalf("success = true, want false — a suspended reseller must not activate a device; body=%s",
				w.Body.String())
		}
		if got := c13Message(t, w); got != switchTenantSuspendedMessage {
			t.Errorf("message = %q, want the suspended-reseller sentence %q (a different sentence means the "+
				"refusal came from somewhere else and this cell proves nothing)", got, switchTenantSuspendedMessage)
		}
		if got := c.codeStatus(t, "C13REACHCODEREDEEMSUSPENDED000CC"); got != common.RedemptionCodeStatusEnabled {
			t.Errorf("redemption status = %d, want %d — the refusal must land before repo.Redeem",
				got, common.RedemptionCodeStatusEnabled)
		}
	})

	t.Run("suspended/credit_pool_me_cookie_arm", func(t *testing.T) {
		w := c.do(t, http.MethodGet, "/api/v2/"+c.tenant.Slug+"/credit-pool/me", "", false)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
		}
		if got := c13ErrorCode(t, w); got != "TENANT_DISABLED" {
			t.Errorf("error_code = %q, want TENANT_DISABLED; body=%s", got, w.Body.String())
		}
	})

	// ---- re-enabled: the gate reads status, it does not latch ------------
	c.setStatus(t, repo.TenantStatusEnabled)
	if w := c.do(t, http.MethodGet, "/api/v2/switch/user/info", "", true); w.Code != http.StatusOK {
		t.Errorf("re-enabled user/info: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w := c.do(t, http.MethodPost, "/api/v2/switch/heartbeat", bodyHeartbeat, true); w.Code != http.StatusOK {
		t.Errorf("re-enabled heartbeat: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w := c.do(t, http.MethodGet, "/api/v2/"+c.tenant.Slug+"/credit-pool/me", "", false); w.Code != http.StatusOK {
		t.Errorf("re-enabled credit-pool/me: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tenant-wide project spend is an admin report.
// ---------------------------------------------------------------------------

// TestGetProjectSpendV2_ForbiddenForNormalUser mirrors
// TestGetAllLogStatV2_ForbiddenForNormalUser (v2_log_stat_test.go): the report
// aggregates every member's consume rows for the whole tenant, so it carries
// the tenant-admin gate its sibling aggregate carries. The per-project CRUD
// list stays readable by a plain member (the token page's picker needs it) —
// TestProjectV2_WritesRequireTenantAdmin_ReadsDoNot pins that half.
func TestGetProjectSpendV2_ForbiddenForNormalUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet,
		"/api/v2/"+ctx.TenantID+"/projects/spend", nil, nil)
	AssertV2Status(t, w, http.StatusForbidden)

	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet,
		"/api/v2/"+ctx.TenantID+"/projects/spend", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
}
