package router

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// TestAdminChannelsFacadeRealChain_StaffVsRoot is the acceptance oracle for
// the ops-channels auth shape: platform staff (role admin) can list through
// /api/v2/admin/channels, while fleet-wide probes stay root-only — same split
// as deprecated /api/channel. Mutation target: moving the facade under
// RootJWTAuth turns the staff list red; dropping RootAuth on /test turns the
// admin probe red.
func TestAdminChannelsFacadeRealChain_StaffVsRoot(t *testing.T) {
	f, cleanup := setupChannelSensitiveWriteRealChain(t)
	defer cleanup()
	_ = seedRealChainChannel(t, f, "facade-staff")

	wStaffList := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodGet, "/api/v2/admin/channels", nil)
	if wStaffList.Code != http.StatusOK {
		t.Fatalf("staff GET /api/v2/admin/channels: status=%d body=%s, want 200 (AdminJWTAuth)", wStaffList.Code, wStaffList.Body.String())
	}

	wStaffGroups := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodGet, "/api/v2/admin/groups", nil)
	if wStaffGroups.Code != http.StatusOK {
		t.Fatalf("staff GET /api/v2/admin/groups: status=%d body=%s, want 200", wStaffGroups.Code, wStaffGroups.Body.String())
	}

	wStaffTestAll := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodGet, "/api/v2/admin/channels/test", nil)
	// RootAuth keeps the v1 envelope shape: HTTP 200 + success:false.
	var denied struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(wStaffTestAll.Body.Bytes(), &denied); err != nil {
		t.Fatalf("staff GET /test unmarshal: %v body=%s", err, wStaffTestAll.Body.String())
	}
	if denied.Success {
		t.Fatalf("staff GET /api/v2/admin/channels/test must be refused by RootAuth; body=%s", wStaffTestAll.Body.String())
	}
	// Do not call the root-positive /test path here: TestAllChannels fans out
	// async probes that race the in-memory DB teardown. Staff refusal alone
	// proves RootAuth is on the chain (without it, success would be true).
}

// TestAdminChannelsFacadeRealChain_SensitiveWrite keeps channel:sensitive_write
// on the admin facade PUT (same handler as /api/channel).
func TestAdminChannelsFacadeRealChain_SensitiveWrite(t *testing.T) {
	f, cleanup := setupChannelSensitiveWriteRealChain(t)
	defer cleanup()
	ch := seedRealChainChannel(t, f, "facade-sensitive")

	w := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodPut, "/api/v2/admin/channels", map[string]interface{}{
		"id": ch.Id, "type": ch.Type, "base_url": "https://8.8.8.8",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("staff PUT sensitive field without grant: status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	var resp struct {
		Success   bool   `json:"success"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Success || resp.ErrorCode != "PERMISSION_DENIED" {
		t.Fatalf("body=%s, want PERMISSION_DENIED", w.Body.String())
	}

	if _, _, err := repo.CreatePermissionGrant(f.adminID, "channel", "sensitive_write", f.rootID); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	wGranted := f.serveAs(t, f.adminID, common.RoleAdminUser, http.MethodPut, "/api/v2/admin/channels", map[string]interface{}{
		"id": ch.Id, "type": ch.Type, "base_url": "https://9.9.9.9",
	})
	if wGranted.Code != http.StatusOK {
		t.Fatalf("staff PUT with grant: status=%d body=%s, want 200", wGranted.Code, wGranted.Body.String())
	}
}
