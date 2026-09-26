package handler

import (
	"net/http"
	"strings"
	"testing"
)

// TestCreateTenant_DuplicateSlugIs409WithoutDriverText locks the create path's
// answer to a unique-constraint hit. It used to be a 500 whose "error" field
// was the raw driver message: constraint name, table and the offending key.
// A taken slug is the caller's mistake to fix, so it is a 409 that names the
// slug and nothing about the schema.
func TestCreateTenant_DuplicateSlugIs409WithoutDriverText(t *testing.T) {
	ctx := r3TenantAdminSetup(t)
	defer ctx.Cleanup()

	first := map[string]interface{}{
		"zitadel_org_id": "org-conflict-a",
		"slug":           "conflict-co",
		"name":           "Conflict A",
	}
	AssertV2Status(t, V2Request(ctx.Router, "POST", "/api/v2/admin/tenants", first, nil), http.StatusCreated)

	// A different org id, so CreateTenantFromIDP's idempotent short-circuit is
	// not taken and the insert really reaches the unique slug index.
	second := map[string]interface{}{
		"zitadel_org_id": "org-conflict-b",
		"slug":           "conflict-co",
		"name":           "Conflict B",
	}
	w := V2Request(ctx.Router, "POST", "/api/v2/admin/tenants", second, nil)
	resp := AssertV2Error(t, w, http.StatusConflict)

	if code, _ := resp["error_code"].(string); code != "TENANT_CONFLICT" {
		t.Errorf("error_code = %q, want TENANT_CONFLICT", code)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "conflict-co") {
		t.Errorf("message should name the slug, got %q", msg)
	}
	body := strings.ToLower(w.Body.String())
	for _, leak := range []string{"unique", "constraint", "duplicate key", "sqlstate", "tenants."} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks driver text %q: %s", leak, w.Body.String())
		}
	}
}
