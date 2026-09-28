package handler

// v2_project_budget_test.go — the monthly cap through the console's API
// (migration 043). The contract the page relies on: a create may carry a
// cap; an update that OMITS the field leaves the cap alone (a client built
// before 043 must not clear caps by renaming); an update that sends 0
// clears it; a negative cap is a 400, not a silent 0.

import (
	"fmt"
	"net/http"
	"testing"
)

func TestProjectV2_MonthlyBudget_CreateUpdateClear(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/projects",
		map[string]interface{}{"name": "Marketing", "monthly_budget_quota": 6_250_000}, []string{"admin"})
	AssertV2Status(t, w, http.StatusCreated)
	created := projectDataMap(t, w.Body.String())
	if got := created["monthly_budget_quota"]; got != float64(6_250_000) {
		t.Fatalf("created monthly_budget_quota = %v, want 6250000", got)
	}
	id := int(created["id"].(float64))
	path := fmt.Sprintf("/api/v2/test-tenant/projects/%d", id)

	// Rename without the field: the cap must survive.
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path,
		map[string]interface{}{"name": "Growth", "description": ""}, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	if got := projectDataMap(t, w.Body.String())["monthly_budget_quota"]; got != float64(6_250_000) {
		t.Fatalf("an update that omits monthly_budget_quota must keep the cap, got %v", got)
	}

	// List and detail both carry it (the page's column reads the list).
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/projects", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	items := AssertV2Success(t, w)["data"].(map[string]interface{})["items"].([]interface{})
	found := false
	for _, it := range items {
		row := it.(map[string]interface{})
		if int(row["id"].(float64)) == id {
			found = true
			if row["monthly_budget_quota"] != float64(6_250_000) {
				t.Fatalf("list row monthly_budget_quota = %v", row["monthly_budget_quota"])
			}
		}
	}
	if !found {
		t.Fatal("created project missing from the list")
	}

	// Explicit 0 clears.
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path,
		map[string]interface{}{"name": "Growth", "monthly_budget_quota": 0}, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	if got := projectDataMap(t, w.Body.String())["monthly_budget_quota"]; got != float64(0) {
		t.Fatalf("monthly_budget_quota: 0 must clear the cap, got %v", got)
	}

	// Negative is refused on both verbs.
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path,
		map[string]interface{}{"name": "Growth", "monthly_budget_quota": -1}, []string{"admin"})
	AssertV2Status(t, w, http.StatusBadRequest)
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/projects",
		map[string]interface{}{"name": "Neg", "monthly_budget_quota": -5}, []string{"admin"})
	AssertV2Status(t, w, http.StatusBadRequest)
}
