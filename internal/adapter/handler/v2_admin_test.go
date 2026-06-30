package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// ============================================================================
// V2 Admin Controller Tests
// These controllers use v1 session authentication with root role requirement
// ============================================================================

func TestListUserMappingsV2_RootOnly(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create some user mappings
	mapping := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_user_123",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "mapped@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ctx.DB.Create(mapping)

	// Try as normal user (should fail)
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.NormalUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/mappings", nil, headers)
	AssertV2Status(t, w, http.StatusForbidden)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Platform admin role required" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}

	// Try as root user (should succeed)
	headers["X-Test-User-ID"] = strconv.Itoa(ctx.RootUser.Id)
	w = V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/mappings", nil, headers)
	AssertV2Status(t, w, http.StatusOK)
	resp = AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	mappings := data["mappings"].([]interface{})
	if len(mappings) < 1 {
		t.Errorf("expected at least 1 mapping, got %d", len(mappings))
	}
}

func TestDeleteUserMappingV2_SoftDelete(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a user mapping
	mapping := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_soft_delete",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "softdelete@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ctx.DB.Create(mapping)

	// Soft delete (default behavior)
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	path := fmt.Sprintf("/api/v2/admin/mappings/%d", mapping.Id)
	w := V2Request(ctx.Router, http.MethodDelete, path, nil, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["hard_delete"].(bool) != false {
		t.Error("expected hard_delete=false for soft delete")
	}

	// Verify mapping is deactivated (not deleted)
	var updatedMapping repo.UserIdentityMapping
	ctx.DB.First(&updatedMapping, mapping.Id)
	if updatedMapping.IsActive {
		t.Error("expected mapping to be deactivated (is_active=false)")
	}
}

func TestDeleteUserMappingV2_HardDelete(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a user mapping
	mapping := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_hard_delete",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "harddelete@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ctx.DB.Create(mapping)

	// Hard delete with ?hard=true
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	path := fmt.Sprintf("/api/v2/admin/mappings/%d?hard=true", mapping.Id)
	w := V2Request(ctx.Router, http.MethodDelete, path, nil, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["hard_delete"].(bool) != true {
		t.Error("expected hard_delete=true for hard delete")
	}

	// Verify mapping is actually deleted
	var count int64
	ctx.DB.Model(&repo.UserIdentityMapping{}).Where("id = ?", mapping.Id).Count(&count)
	if count != 0 {
		t.Error("expected mapping to be completely deleted")
	}
}

func TestGetSystemStatsV2_AllStats(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create some data to have stats
	SeedV2Token(t, ctx, ctx.NormalUser.Id, "Test Token")
	SeedV2Channel(t, ctx, "Test Channel")
	SeedV2Redemption(t, ctx, ctx.NormalUser.Id)

	// Create a user mapping
	mapping := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_stats_user",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "stats@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ctx.DB.Create(mapping)

	// Get stats as root user
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/stats", nil, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})

	// Verify all stat categories are present
	expectedCategories := []string{"users", "tokens", "channels", "tenants", "mappings", "quota", "billing"}
	for _, cat := range expectedCategories {
		if data[cat] == nil {
			t.Errorf("expected %s category in stats", cat)
		}
	}

	// Verify users stats
	users := data["users"].(map[string]interface{})
	if users["total"].(float64) < 3 { // root, admin, normal
		t.Errorf("expected at least 3 users, got %v", users["total"])
	}

	// Verify tokens stats
	tokens := data["tokens"].(map[string]interface{})
	if tokens["total"].(float64) < 1 {
		t.Errorf("expected at least 1 token, got %v", tokens["total"])
	}

	// Verify channels stats
	channels := data["channels"].(map[string]interface{})
	if channels["total"].(float64) < 1 {
		t.Errorf("expected at least 1 channel, got %v", channels["total"])
	}

	// Verify billing stats (API only returns redemptions_total)
	billing := data["billing"].(map[string]interface{})
	if billing["redemptions_total"].(float64) < 1 {
		t.Errorf("expected at least 1 redemption, got %v", billing["redemptions_total"])
	}
}

func TestGetUserMappingV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a user mapping
	mapping := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_get_mapping",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "getmapping@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ctx.DB.Create(mapping)

	// Get mapping as root user
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	path := fmt.Sprintf("/api/v2/admin/mappings/%d", mapping.Id)
	w := V2Request(ctx.Router, http.MethodGet, path, nil, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})

	// Verify mapping data
	mappingData := data["mapping"].(map[string]interface{})
	if mappingData["email"] != "getmapping@test.local" {
		t.Errorf("expected email='getmapping@test.local', got %v", mappingData["email"])
	}

	// Verify associated user data
	userData := data["user"].(map[string]interface{})
	if userData["id"].(float64) != float64(ctx.NormalUser.Id) {
		t.Errorf("expected user id=%d, got %v", ctx.NormalUser.Id, userData["id"])
	}
}

func TestGetUserMappingV2_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/mappings/99999", nil, headers)

	AssertV2Status(t, w, http.StatusNotFound)
}

func TestListUserMappingsV2_FilterByTenant(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create mappings in different tenants
	mapping1 := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_tenant1",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "tenant1@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	mapping2 := &repo.UserIdentityMapping{
		TenantID:      "other-tenant-filter",
		IDPSubject: "zitadel_tenant2",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "tenant2@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ctx.DB.Create(mapping1)
	ctx.DB.Create(mapping2)

	// Filter by tenant
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	path := fmt.Sprintf("/api/v2/admin/mappings?tenant_id=%s", ctx.TenantID)
	w := V2Request(ctx.Router, http.MethodGet, path, nil, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 mapping for tenant, got %d", total)
	}
}

func TestListUserMappingsV2_Pagination(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create 15 mappings
	for i := 0; i < 15; i++ {
		mapping := &repo.UserIdentityMapping{
			TenantID:      ctx.TenantID,
			IDPSubject: fmt.Sprintf("zitadel_page_%d", i),
			LurusUserID:   ctx.NormalUser.Id,
			Email:         fmt.Sprintf("page%d@test.local", i),
			IsActive:      true,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		ctx.DB.Create(mapping)
	}

	// Get first page
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/mappings?page=1&page_size=10", nil, headers)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	mappings := data["mappings"].([]interface{})
	if len(mappings) != 10 {
		t.Errorf("expected 10 mappings on first page, got %d", len(mappings))
	}

	total := int(data["total"].(float64))
	if total != 15 {
		t.Errorf("expected total=15, got %d", total)
	}
}

func TestDeleteUserMappingV2_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodDelete, "/api/v2/admin/mappings/99999", nil, headers)

	AssertV2Status(t, w, http.StatusNotFound)
}

func TestListUserMappingsV2_FilterByZitadelUser(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create mappings with different zitadel user IDs
	mapping1 := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_filter_target",
		LurusUserID:   ctx.NormalUser.Id,
		Email:         "filter1@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	mapping2 := &repo.UserIdentityMapping{
		TenantID:      ctx.TenantID,
		IDPSubject: "zitadel_other_user",
		LurusUserID:   ctx.AdminUser.Id,
		Email:         "filter2@test.local",
		IsActive:      true,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	ctx.DB.Create(mapping1)
	ctx.DB.Create(mapping2)

	// Filter by zitadel_user_id
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/mappings?zitadel_user_id=zitadel_filter_target", nil, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 mapping for zitadel_filter_target, got %d", total)
	}
}

func TestGetUserMappingV2_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/mappings/invalid", nil, headers)

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Invalid mapping ID" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestGetSystemStatsV2_NonRootRejected(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Try as normal user (not root)
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.NormalUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/stats", nil, headers)

	AssertV2Status(t, w, http.StatusForbidden)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Platform admin role required" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}

	// Try as admin user (still not root)
	headers["X-Test-User-ID"] = strconv.Itoa(ctx.AdminUser.Id)
	w = V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/stats", nil, headers)

	AssertV2Status(t, w, http.StatusForbidden)
}

func TestListUserMappingsV2_NoFilter(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a few mappings
	for i := 0; i < 3; i++ {
		mapping := &repo.UserIdentityMapping{
			TenantID:      ctx.TenantID,
			IDPSubject: fmt.Sprintf("zitadel_nofilter_%d", i),
			LurusUserID:   ctx.NormalUser.Id,
			Email:         fmt.Sprintf("nofilter%d@test.local", i),
			IsActive:      true,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		ctx.DB.Create(mapping)
	}

	// List all without any filter
	headers := map[string]string{
		"X-Test-User-ID": strconv.Itoa(ctx.RootUser.Id),
	}
	w := V2Request(ctx.Router, http.MethodGet, "/api/v2/admin/mappings", nil, headers)

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total < 3 {
		t.Errorf("expected at least 3 mappings, got %d", total)
	}

	mappings := data["mappings"].([]interface{})
	if len(mappings) < 3 {
		t.Errorf("expected at least 3 mappings in response, got %d", len(mappings))
	}
}
