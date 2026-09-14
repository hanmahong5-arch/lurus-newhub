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
	governance.SetAuditWriter(&pinnedAuditWriter{db: db})

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
		{"unknown resource", "channel", "sensitive_write"},
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
