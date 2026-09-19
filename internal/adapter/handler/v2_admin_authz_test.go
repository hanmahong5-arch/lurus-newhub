package handler

// v2_admin_authz_test.go — L4 (auth-security-17/18, console-ux-36):
// CreateGrantV2/RevokeGrantV2/ListGrantsV2/GetAuthzCatalogV2. Drives the
// real handlers with an isolated SQLite DB and a real audit writer pinned
// to it (pinnedAuditWriter, v2_pricing_write_test.go) so the audit-firing
// claim is checked against a genuinely persisted row, not a spy call count.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var authzTestDBCounter atomic.Int64

func setupAuthzTestDB(t *testing.T) func() {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbName := fmt.Sprintf("file:authztest%d?mode=memory&cache=shared", authzTestDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, tbl := range []interface{}{&repo.User{}, &entity.AdminPermissionGrant{}, &entity.AuditEvent{}, &entity.AuditChainHead{}} {
		if err := db.AutoMigrate(tbl); err != nil {
			t.Fatalf("migrate %T: %v", tbl, err)
		}
	}

	prevDB, prevRedis := repo.DB, common.RedisEnabled
	repo.DB = db
	common.RedisEnabled = false
	pinAuditWriter(t, db)

	return func() {
		repo.DB, common.RedisEnabled = prevDB, prevRedis
		if sqlDB, e := db.DB(); e == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

// seedGranteeUser inserts a user row with the given role — CreateGrantV2
// (B-F4, cycle-8 L4 repair round) now looks the grantee up and refuses a
// user_id that doesn't resolve to an existing admin-or-above user, so every
// test that expects a grant to actually be created must seed one first.
func seedGranteeUser(t *testing.T, id, role int) {
	t.Helper()
	if err := repo.DB.Create(&repo.User{Id: id, Username: fmt.Sprintf("grantee-%d", id), Role: role, Status: common.UserStatusEnabled}).Error; err != nil {
		t.Fatalf("seed grantee user id=%d: %v", id, err)
	}
}

// buildAuthzRouter wires the real handlers behind a stub auth that fixes
// the acting root's id — mirrors adminInviteTestDBCounter's mockAuth
// convention (RootJWTAuth's session branch sets exactly "id").
func buildAuthzRouter(actorID int) *gin.Engine {
	r := gin.New()
	mockAuth := func(c *gin.Context) {
		c.Set("id", actorID)
		c.Next()
	}
	g := r.Group("/api/v2/admin/authz", mockAuth)
	g.GET("/grants", ListGrantsV2)
	g.POST("/grants", CreateGrantV2)
	g.DELETE("/grants/:id", RevokeGrantV2)
	g.GET("/catalog", GetAuthzCatalogV2)
	return r
}

// buildAuthzRouterJWTPath mirrors buildAuthzRouter but stands in for
// RootJWTAuth's Bearer-JWT branch: no "id" is ever set (that branch never
// populates it — admin_jwt_auth.go), only "admin_sub" — the shape B-F11
// documents grantedBySubDetail exists for.
func buildAuthzRouterJWTPath(adminSub string) *gin.Engine {
	r := gin.New()
	mockAuth := func(c *gin.Context) {
		c.Set("admin_sub", adminSub)
		c.Next()
	}
	g := r.Group("/api/v2/admin/authz", mockAuth)
	g.POST("/grants", CreateGrantV2)
	g.DELETE("/grants/:id", RevokeGrantV2)
	return r
}

func TestAuthzGrants_CreateFiresAuditRow(t *testing.T) {
	defer setupAuthzTestDB(t)()
	seedGranteeUser(t, 42, common.RoleAdminUser)

	const actorID = 1
	r := buildAuthzRouter(actorID)

	body, _ := json.Marshal(map[string]interface{}{
		"user_id":  42,
		"resource": "audit",
		"action":   "read",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	ev := pollAuditRow(t, governance.ActionPermissionGranted, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionPermissionGranted)
	}
	if ev.ActorID != actorID {
		t.Errorf("ActorID = %d, want %d", ev.ActorID, actorID)
	}
	if !bytes.Contains([]byte(ev.Details), []byte(`"grantee_user_id":42`)) {
		t.Errorf("Details = %s, want it to name grantee_user_id:42", ev.Details)
	}

	// A second grant for the SAME (user, resource, action) while the first
	// is active is rejected.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("duplicate grant: status = %d, want 409; body=%s", w2.Code, w2.Body.String())
	}
	var conflictResp map[string]interface{}
	_ = json.Unmarshal(w2.Body.Bytes(), &conflictResp)
	if conflictResp["error_code"] != "GRANT_EXISTS" {
		t.Errorf("error_code = %v, want GRANT_EXISTS", conflictResp["error_code"])
	}

	// List surfaces the grant.
	reqList := httptest.NewRequest(http.MethodGet, "/api/v2/admin/authz/grants", nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)
	var listResp struct {
		Data []entity.AdminPermissionGrant `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list body: %v", err)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("ListGrantsV2 returned %d grants, want 1", len(listResp.Data))
	}
	grantID := listResp.Data[0].Id

	// Revoke fires ActionPermissionRevoked and a re-list shows it revoked.
	reqRevoke := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/admin/authz/grants/%d", grantID), nil)
	wRevoke := httptest.NewRecorder()
	r.ServeHTTP(wRevoke, reqRevoke)
	if wRevoke.Code != http.StatusOK {
		t.Fatalf("revoke: status = %d, want 200; body=%s", wRevoke.Code, wRevoke.Body.String())
	}
	if revEv := pollAuditRow(t, governance.ActionPermissionRevoked, 2*time.Second); revEv == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionPermissionRevoked)
	}

	// Revoking again 404s (already revoked).
	wRevoke2 := httptest.NewRecorder()
	r.ServeHTTP(wRevoke2, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/admin/authz/grants/%d", grantID), nil))
	if wRevoke2.Code != http.StatusNotFound {
		t.Fatalf("second revoke: status = %d, want 404; body=%s", wRevoke2.Code, wRevoke2.Body.String())
	}
}

func TestAuthzGrants_CatalogRejectsUnknownResource(t *testing.T) {
	defer setupAuthzTestDB(t)()

	r := buildAuthzRouter(1)

	// GET /catalog surfaces the audit:read entry and the two fixed roles.
	reqCatalog := httptest.NewRequest(http.MethodGet, "/api/v2/admin/authz/catalog", nil)
	wCatalog := httptest.NewRecorder()
	r.ServeHTTP(wCatalog, reqCatalog)
	if wCatalog.Code != http.StatusOK {
		t.Fatalf("GET /catalog: status = %d, want 200; body=%s", wCatalog.Code, wCatalog.Body.String())
	}
	if !bytes.Contains(wCatalog.Body.Bytes(), []byte(`"audit"`)) {
		t.Errorf("catalog body = %s, want an audit resource entry", wCatalog.Body.String())
	}

	cases := []struct {
		name             string
		resource, action string
	}{
		// R7 (repair-round ruling / B-6): "channel"/"sensitive_write" was
		// removed from this table — catalog.go:26 (cycle-9 L2) made that
		// pair VALID, so it no longer exercises the unknown-resource
		// branch; see TestAuthzGrants_CreateChannelSensitiveWrite_Succeeds
		// below for the positive case this pair now needs. "wallet" is not
		// (and is not planned to become) a resource in the catalogue.
		{"unknown resource", "wallet", "write"},
		{"unknown action for a known resource", "audit", "write"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]interface{}{
				"user_id": 7, "resource": tc.resource, "action": tc.action,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
			var resp map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["error_code"] != "GRANT_INVALID" {
				t.Errorf("error_code = %v, want GRANT_INVALID", resp["error_code"])
			}
		})
	}

	// A non-null tenant_id is also GRANT_INVALID this cycle (grants are
	// global; O5 amendment).
	t.Run("non-null tenant_id rejected", func(t *testing.T) {
		tenantID := "some-tenant"
		body, _ := json.Marshal(map[string]interface{}{
			"user_id": 7, "resource": "audit", "action": "read", "tenant_id": tenantID,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
		}
	})
}

// TestAuthzGrants_CreateChannelSensitiveWrite_Succeeds is R7's positive
// case (repair-round ruling / B-6): channel:sensitive_write (catalog.go:26,
// cycle-9 L2) is a real grantable pair through the SAME
// POST /api/v2/admin/authz/grants endpoint the UAT probe uses — nothing
// before this test actually proved it could be granted through the API,
// only that IsValid(...) agreed with it in isolation
// (TestCatalog_ContainsChannelSensitiveWrite,
// internal/app/authz/catalog_test.go).
func TestAuthzGrants_CreateChannelSensitiveWrite_Succeeds(t *testing.T) {
	defer setupAuthzTestDB(t)()
	seedGranteeUser(t, 43, common.RoleAdminUser)

	r := buildAuthzRouter(1)

	body, _ := json.Marshal(map[string]interface{}{
		"user_id": 43, "resource": "channel", "action": "sensitive_write",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if resp["success"] != true {
		t.Fatalf("success = %v, want true; body=%s", resp["success"], w.Body.String())
	}

	granted, err := repo.HasActivePermissionGrant(43, "channel", "sensitive_write")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant: %v", err)
	}
	if !granted {
		t.Fatalf("HasActivePermissionGrant(43, channel, sensitive_write) = false right after a 201 grant")
	}
}

// TestAuthzGrants_JWTPathWriteRecordsGrantedBySub is the B-F11 oracle
// (cycle-8 §8 L4 ruling): on RootJWTAuth's Bearer-JWT branch, "id" is never
// set, so GrantedBy/ActorID are 0 for the write — but the write is still
// allowed (no 400), and the audit Details record the JWT subject under
// granted_by_sub so the row is not attributed to nobody.
func TestAuthzGrants_JWTPathWriteRecordsGrantedBySub(t *testing.T) {
	defer setupAuthzTestDB(t)()
	seedGranteeUser(t, 42, common.RoleAdminUser)

	r := buildAuthzRouterJWTPath("root-operator@example.test")

	body, _ := json.Marshal(map[string]interface{}{
		"user_id": 42, "resource": "audit", "action": "read",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — a JWT-path write must still be allowed; body=%s", w.Code, w.Body.String())
	}

	ev := pollAuditRow(t, governance.ActionPermissionGranted, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionPermissionGranted)
	}
	if ev.ActorID != 0 {
		t.Errorf("ActorID = %d, want 0 (RootJWTAuth's JWT branch never sets \"id\")", ev.ActorID)
	}
	if !bytes.Contains([]byte(ev.Details), []byte(`"granted_by_sub":"root-operator@example.test"`)) {
		t.Errorf("Details = %s, want granted_by_sub naming the JWT subject", ev.Details)
	}

	var createResp struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create body: %v", err)
	}

	// Revoke on the same JWT path also records granted_by_sub.
	reqRevoke := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/admin/authz/grants/%d", createResp.Data.ID), nil)
	wRevoke := httptest.NewRecorder()
	r.ServeHTTP(wRevoke, reqRevoke)
	if wRevoke.Code != http.StatusOK {
		t.Fatalf("revoke: status = %d, want 200; body=%s", wRevoke.Code, wRevoke.Body.String())
	}
	revEv := pollAuditRow(t, governance.ActionPermissionRevoked, 2*time.Second)
	if revEv == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionPermissionRevoked)
	}
	if !bytes.Contains([]byte(revEv.Details), []byte(`"granted_by_sub":"root-operator@example.test"`)) {
		t.Errorf("revoke Details = %s, want granted_by_sub naming the JWT subject", revEv.Details)
	}
}

// TestAuthzGrants_RejectsNonAdminGrantee is the B-F4 oracle (cycle-8 L4
// repair round): CreateGrantV2 used to accept any positive user_id with no
// existence/role check, so a mistyped id minted a silently inert "active"
// row. Two ways that can go wrong: the user_id doesn't resolve to any row,
// or it resolves to a row whose role is below common.RoleAdminUser (a
// grant that could never pass RootOrGranted's RoleAdminUser floor anyway).
func TestAuthzGrants_RejectsNonAdminGrantee(t *testing.T) {
	defer setupAuthzTestDB(t)()
	seedGranteeUser(t, 55, common.RoleCommonUser)

	r := buildAuthzRouter(1)

	cases := []struct {
		name   string
		userID int
	}{
		{"user_id does not resolve to any row", 987654},
		{"user_id resolves to a role below RoleAdminUser", 55},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]interface{}{
				"user_id": tc.userID, "resource": "audit", "action": "read",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
			var resp map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["error_code"] != "GRANT_INVALID" {
				t.Errorf("error_code = %v, want GRANT_INVALID", resp["error_code"])
			}

			granted, err := repo.HasActivePermissionGrant(tc.userID, "audit", "read")
			if err != nil {
				t.Fatalf("HasActivePermissionGrant: %v", err)
			}
			if granted {
				t.Fatalf("a grant row was created for a rejected grantee (user_id=%d)", tc.userID)
			}
		})
	}
}

// TestGrant_TTLBounds is the L1 oracle (cycle-9 plan section 3): ttl_seconds
// is bounded 1..7776000 (90 days) inclusive; 0, negative, and over-90-days
// all answer 400 GRANT_INVALID, the same error_code every other
// CreateGrantV2 rejection uses, not a new one.
func TestGrant_TTLBounds(t *testing.T) {
	defer setupAuthzTestDB(t)()
	seedGranteeUser(t, 42, common.RoleAdminUser)

	r := buildAuthzRouter(1)

	cases := []struct {
		name string
		ttl  int64
	}{
		{"zero", 0},
		{"negative", -1},
		{"over 90 days", 7776001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]interface{}{
				"user_id": 42, "resource": "audit", "action": "read", "ttl_seconds": tc.ttl,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("ttl_seconds=%d: status = %d, want 400; body=%s", tc.ttl, w.Code, w.Body.String())
			}
			var resp map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["error_code"] != "GRANT_INVALID" {
				t.Errorf("ttl_seconds=%d: error_code = %v, want GRANT_INVALID", tc.ttl, resp["error_code"])
			}
			granted, err := repo.HasActivePermissionGrant(42, "audit", "read")
			if err != nil {
				t.Fatalf("HasActivePermissionGrant: %v", err)
			}
			if granted {
				t.Fatalf("ttl_seconds=%d: a grant row was created despite the out-of-bounds ttl", tc.ttl)
			}
		})
	}

	// The boundary values are valid, not rejected.
	for _, tc := range []struct {
		name string
		ttl  int64
	}{{"minimum (1 second)", 1}, {"maximum (90 days)", 7776000}} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]interface{}{
				"user_id": 42, "resource": "audit", "action": "read", "ttl_seconds": tc.ttl,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusCreated {
				t.Fatalf("ttl_seconds=%d: status = %d, want 201; body=%s", tc.ttl, w.Code, w.Body.String())
			}
			// Revoke so the next boundary case does not collide with the
			// still-live "one active grant per triple" invariant.
			var createResp struct {
				Data struct {
					ID int `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
				t.Fatalf("unmarshal create body: %v", err)
			}
			wRevoke := httptest.NewRecorder()
			r.ServeHTTP(wRevoke, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v2/admin/authz/grants/%d", createResp.Data.ID), nil))
			if wRevoke.Code != http.StatusOK {
				t.Fatalf("cleanup revoke: status = %d, want 200; body=%s", wRevoke.Code, wRevoke.Body.String())
			}
		})
	}
}

// TestAuthzGrants_CreateWithTTLSetsExpiresAtAndAuditRecordsTTL is the L1
// oracle for the ttl_seconds happy path: the created row expires_at is
// populated, the list response derives expired:false while still live, and
// the create audit detail records the ttl.
func TestAuthzGrants_CreateWithTTLSetsExpiresAtAndAuditRecordsTTL(t *testing.T) {
	defer setupAuthzTestDB(t)()
	seedGranteeUser(t, 42, common.RoleAdminUser)

	const actorID = 1
	r := buildAuthzRouter(actorID)

	body, _ := json.Marshal(map[string]interface{}{
		"user_id": 42, "resource": "audit", "action": "read", "ttl_seconds": 3600,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}

	// A-3: the 201 body itself carries expires_at ~= created_at + ttl, read
	// directly off the response, not inferred from the later GET.
	beforeCreate := common.GetTimestamp()
	var createResp struct {
		Data struct {
			ID        int    `json:"id"`
			ExpiresAt *int64 `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create body: %v", err)
	}
	if createResp.Data.ExpiresAt == nil {
		t.Fatalf("201 body expires_at is nil for a grant created with ttl_seconds=3600")
	}
	if diff := *createResp.Data.ExpiresAt - (beforeCreate + 3600); diff < -5 || diff > 5 {
		t.Fatalf("201 body expires_at = %d, want ~= now+3600 (%d)", *createResp.Data.ExpiresAt, beforeCreate+3600)
	}

	ev := pollAuditRow(t, governance.ActionPermissionGranted, 2*time.Second)
	if ev == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionPermissionGranted)
	}
	if !bytes.Contains([]byte(ev.Details), []byte(`"ttl_seconds":3600`)) {
		t.Errorf("Details = %s, want ttl_seconds:3600 recorded", ev.Details)
	}

	reqList := httptest.NewRequest(http.MethodGet, "/api/v2/admin/authz/grants", nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)
	var listResp struct {
		Data []struct {
			ID        int    `json:"id"`
			ExpiresAt *int64 `json:"expires_at"`
			Expired   bool   `json:"expired"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list body: %v", err)
	}
	if len(listResp.Data) != 1 {
		t.Fatalf("list returned %d grants, want 1", len(listResp.Data))
	}
	if listResp.Data[0].ExpiresAt == nil {
		t.Fatalf("list response expires_at is nil for a grant created with ttl_seconds=3600")
	}
	if listResp.Data[0].Expired {
		t.Fatalf("list response expired = true for a grant that has not expired yet")
	}

	// A grant created with NO ttl_seconds carries expires_at:null and
	// expired:false, the flag-off equivalent is byte-identical to pre-L1
	// behaviour.
	seedGranteeUser(t, 43, common.RoleAdminUser)
	body2, _ := json.Marshal(map[string]interface{}{
		"user_id": 43, "resource": "audit", "action": "read",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("no-ttl create: status = %d, want 201; body=%s", w2.Code, w2.Body.String())
	}

	// expires_at must come back as an explicit JSON null, not an omitted key:
	// the console distinguishes "never expires" from "this build does not
	// return the field" by whether the key is there at all.
	var noTTLResp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &noTTLResp); err != nil {
		t.Fatalf("unmarshal no-ttl create body: %v", err)
	}
	rawExpiry, present := noTTLResp.Data["expires_at"]
	if !present {
		t.Fatalf("no-ttl 201 body omits expires_at entirely; body=%s", w2.Body.String())
	}
	if string(rawExpiry) != "null" {
		t.Fatalf("no-ttl 201 body expires_at = %s, want null", rawExpiry)
	}

	// ...and the same row reads back through the real ListGrantsV2 handler as
	// expires_at:null / expired:false. A nil expiry deriving as expired would
	// retire every open-ended grant the moment this flag shipped.
	reqList2 := httptest.NewRequest(http.MethodGet, "/api/v2/admin/authz/grants", nil)
	wList2 := httptest.NewRecorder()
	r.ServeHTTP(wList2, reqList2)
	var listResp2 struct {
		Data []struct {
			UserId    int    `json:"user_id"`
			ExpiresAt *int64 `json:"expires_at"`
			Expired   bool   `json:"expired"`
		} `json:"data"`
	}
	if err := json.Unmarshal(wList2.Body.Bytes(), &listResp2); err != nil {
		t.Fatalf("unmarshal no-ttl list body: %v", err)
	}
	sawNoTTLRow := false
	for _, row := range listResp2.Data {
		if row.UserId != 43 {
			continue
		}
		sawNoTTLRow = true
		if row.ExpiresAt != nil {
			t.Errorf("list row for user 43 expires_at = %d, want null", *row.ExpiresAt)
		}
		if row.Expired {
			t.Errorf("list row for user 43 expired = true, want false for a grant with no expiry")
		}
	}
	if !sawNoTTLRow {
		t.Fatalf("list response has no row for user_id 43; body=%s", wList2.Body.String())
	}

	found := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var rows []entity.AuditEvent
		if err := repo.DB.Where("action = ?", governance.ActionPermissionGranted).Find(&rows).Error; err == nil {
			for _, row := range rows {
				if bytes.Contains([]byte(row.Details), []byte(`"grantee_user_id":43`)) {
					if !bytes.Contains([]byte(row.Details), []byte(`"ttl_seconds":null`)) {
						t.Errorf("no-ttl create Details = %s, want ttl_seconds:null", row.Details)
					}
					found = true
					break
				}
			}
		}
		if found {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !found {
		t.Fatalf("no audit row found naming grantee_user_id:43")
	}
}

// TestAuthzGrants_ListMarksExpiredRowTrue is the A-2 oracle: a row seeded
// with expires_at genuinely in the past must come back expired:true through
// the real ListGrantsV2 handler, while a future-expiring row on the same
// list comes back expired:false. Replacing ListGrantsV2's derived
// "Expired: g.ExpiresAt != nil && *g.ExpiresAt <= now" with a constant
// false must turn this red.
func TestAuthzGrants_ListMarksExpiredRowTrue(t *testing.T) {
	defer setupAuthzTestDB(t)()

	past := common.GetTimestamp() - 3600
	future := common.GetTimestamp() + 3600
	if err := repo.DB.Create(&entity.AdminPermissionGrant{
		UserId: 51, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp() - 7200, ExpiresAt: &past,
	}).Error; err != nil {
		t.Fatalf("seed expired grant: %v", err)
	}
	if err := repo.DB.Create(&entity.AdminPermissionGrant{
		UserId: 52, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp(), ExpiresAt: &future,
	}).Error; err != nil {
		t.Fatalf("seed future-expiring grant: %v", err)
	}

	r := buildAuthzRouter(1)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/admin/authz/grants", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var listResp struct {
		Data []struct {
			UserID  int  `json:"user_id"`
			Expired bool `json:"expired"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list body: %v", err)
	}
	var sawExpired, sawFuture bool
	for _, row := range listResp.Data {
		switch row.UserID {
		case 51:
			sawExpired = true
			if !row.Expired {
				t.Errorf("user_id 51 (expires_at in the past) expired = false, want true")
			}
		case 52:
			sawFuture = true
			if row.Expired {
				t.Errorf("user_id 52 (expires_at in the future) expired = true, want false")
			}
		}
	}
	if !sawExpired || !sawFuture {
		t.Fatalf("list response = %+v, missing one of the seeded rows", listResp.Data)
	}
}

// TestGrant_ReGrantAfterExpiry_ThroughHandler is a HANDLER-LEVEL oracle over
// the hand-seeded buildAuthzRouter — not the REAL-CHAIN oracle for "an
// expired grant does not authorise the route it was granted for" (that
// claim is proved end-to-end by TestRootOrGranted_ExpiredGrant403 in
// internal/adapter/middleware/root_or_granted_test.go and the expired-grant
// case in internal/adapter/handler/router/audit_routes_root_or_granted_test.go,
// both of which drive the real router/middleware chain). This test's job is
// narrower: through the real CreateGrantV2 handler, (1) re-granting the
// same (user, resource, action) after expiry answers 201 not 409 — the
// partial-unique-index trap the plan calls out — (2) exactly one row for
// that triple stays revoked_at IS NULL afterward, and (3) the recycled old
// row's supersession is itself audited (R1, cycle-9 L1 repair ruling).
func TestGrant_ReGrantAfterExpiry_ThroughHandler(t *testing.T) {
	defer setupAuthzTestDB(t)()
	seedGranteeUser(t, 42, common.RoleAdminUser)

	past := common.GetTimestamp() - 60
	expired := entity.AdminPermissionGrant{
		UserId: 42, Resource: "audit", Action: "read", GrantedBy: 1,
		CreatedAt: common.GetTimestamp() - 120, ExpiresAt: &past,
	}
	if err := repo.DB.Create(&expired).Error; err != nil {
		t.Fatalf("seed expired grant: %v", err)
	}

	r := buildAuthzRouter(1)
	body, _ := json.Marshal(map[string]interface{}{
		"user_id": 42, "resource": "audit", "action": "read",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/admin/authz/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("re-grant after expiry through the real handler: status = %d, want 201 (not 409); body=%s", w.Code, w.Body.String())
	}

	granted, err := repo.HasActivePermissionGrant(42, "audit", "read")
	if err != nil {
		t.Fatalf("HasActivePermissionGrant: %v", err)
	}
	if !granted {
		t.Fatalf("HasActivePermissionGrant = false right after the handler re-grant succeeded")
	}

	// Exactly one active row for (42, audit, read) remains — the old
	// expired row must have been stamped revoked_at, not left dangling.
	var rows []entity.AdminPermissionGrant
	if err := repo.DB.Where("user_id = ? AND resource = ? AND action = ?", 42, "audit", "read").Find(&rows).Error; err != nil {
		t.Fatalf("list rows: %v", err)
	}
	activeCount := 0
	for _, row := range rows {
		if row.RevokedAt == nil {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("active (revoked_at IS NULL) rows for (42,audit,read) = %d, want exactly 1", activeCount)
	}

	// R1: the recycled old row's supersession is itself audited — a
	// permission_revoked row naming the OLD grant id with
	// reason:superseded_expired, alongside the permission_granted row for
	// the new one.
	grantedEv := pollAuditRow(t, governance.ActionPermissionGranted, 2*time.Second)
	if grantedEv == nil {
		t.Fatalf("no %s audit row found within timeout", governance.ActionPermissionGranted)
	}

	var revokedRow *entity.AuditEvent
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var evs []entity.AuditEvent
		if err := repo.DB.Where("action = ?", governance.ActionPermissionRevoked).Find(&evs).Error; err == nil {
			for i := range evs {
				if bytes.Contains([]byte(evs[i].Details), []byte(fmt.Sprintf(`"grant_id":%d`, expired.Id))) {
					revokedRow = &evs[i]
					break
				}
			}
		}
		if revokedRow != nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if revokedRow == nil {
		t.Fatalf("no %s audit row naming grant_id:%d (the superseded expired row) found within timeout", governance.ActionPermissionRevoked, expired.Id)
	}
	if !bytes.Contains([]byte(revokedRow.Details), []byte(`"reason":"superseded_expired"`)) {
		t.Errorf("revoked-row Details = %s, want reason:superseded_expired", revokedRow.Details)
	}
	if !bytes.Contains([]byte(revokedRow.Details), []byte(`"grantee_user_id":42`)) {
		t.Errorf("revoked-row Details = %s, want grantee_user_id:42", revokedRow.Details)
	}
	// The recycle happens inside the request that created the replacement
	// grant, so it must be attributed to the calling admin (actor 1 here, from
	// buildAuthzRouter) — not to ActorSystem/0, which is what a background
	// sweep would write. Recording it as a system event would make "who
	// retired that grant" unanswerable from the audit trail.
	if revokedRow.ActorType != governance.ActorAdmin {
		t.Errorf("revoked-row ActorType = %q, want %q", revokedRow.ActorType, governance.ActorAdmin)
	}
	if revokedRow.ActorID != 1 {
		t.Errorf("revoked-row ActorID = %d, want 1 (the admin who called CreateGrantV2)", revokedRow.ActorID)
	}
}
