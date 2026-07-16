/*
Copyright (C) 2025 LurusTech

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

package handler

// ============================================================================
// Cross-tenant channel IDOR regression tests.
//
// GetChannelV2 / UpdateChannelV2 / DeleteChannelV2 / TestChannelV2 /
// FetchUpstreamModelsV2 all fetch the channel by its bare integer id
// (repo.GetChannelById) but — before the fix — never checked that the
// channel's TenantId matched the caller's tenantCtx.TenantID. Any tenant
// admin could read/mutate/delete/exercise another tenant's channel (and its
// secret upstream key) just by guessing/incrementing the numeric id.
// ============================================================================

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

const victimChannelKey = "sk-victim-SECRET-KEY-DO-NOT-LEAK"

// seedVictimChannel creates a channel owned by a different tenant than the
// caller's — this is the "B" side of an A-admin-attacks-B-channel scenario.
func seedVictimChannel(t *testing.T, ctx *V2TestContext, name string, baseURL *string) *repo.Channel {
	t.Helper()
	victim := &repo.Channel{
		Name:        name,
		TenantId:    "other-tenant-xyz",
		Key:         victimChannelKey,
		Status:      common.ChannelStatusEnabled,
		Type:        1,
		Models:      "gpt-4",
		Group:       "default",
		BaseURL:     baseURL,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("failed to seed victim channel: %v", err)
	}
	return victim
}

func TestGetChannelV2_CrossTenantForbidden(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedVictimChannel(t, ctx, "Victim Channel", nil)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", victim.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)
	if strings.Contains(w.Body.String(), victimChannelKey) {
		t.Errorf("cross-tenant channel key leaked in response body: %s", w.Body.String())
	}
}

func TestUpdateChannelV2_CrossTenantForbidden(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedVictimChannel(t, ctx, "Victim Channel", nil)
	originalBaseURL := victim.BaseURL
	originalKey := victim.Key

	body := map[string]interface{}{
		"base_url": "https://attacker.evil",
		"key":      "sk-attacker",
	}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", victim.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)

	var reloaded repo.Channel
	if err := ctx.DB.First(&reloaded, victim.Id).Error; err != nil {
		t.Fatalf("failed to reload victim channel: %v", err)
	}
	if reloaded.Key != originalKey {
		t.Errorf("victim channel key was mutated by cross-tenant update: got %q, want %q", reloaded.Key, originalKey)
	}
	gotBaseURL := ""
	if reloaded.BaseURL != nil {
		gotBaseURL = *reloaded.BaseURL
	}
	wantBaseURL := ""
	if originalBaseURL != nil {
		wantBaseURL = *originalBaseURL
	}
	if gotBaseURL != wantBaseURL {
		t.Errorf("victim channel base_url was mutated by cross-tenant update: got %q, want %q", gotBaseURL, wantBaseURL)
	}
}

func TestDeleteChannelV2_CrossTenantForbidden(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	victim := seedVictimChannel(t, ctx, "Victim Channel", nil)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", victim.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)

	var count int64
	if err := ctx.DB.Model(&repo.Channel{}).Where("id = ?", victim.Id).Count(&count).Error; err != nil {
		t.Fatalf("failed to count victim channel: %v", err)
	}
	if count != 1 {
		t.Errorf("victim channel was deleted by cross-tenant request: count=%d, want 1", count)
	}
}

func TestChannelV2_CrossTenantForbidden(t *testing.T) {
	// If the fix ever regresses, this mock upstream would otherwise be hit —
	// its presence proves the 403 must fire *before* any outbound call.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("outbound call reached upstream — tenant check did not short-circuit")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	baseURL := upstream.URL
	victim := seedVictimChannel(t, ctx, "Victim Channel", &baseURL)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/test", victim.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)
}

func TestFetchUpstreamModelsV2_ChannelCrossTenantForbidden(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("outbound call reached upstream — tenant check did not short-circuit")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	baseURL := upstream.URL
	victim := seedVictimChannel(t, ctx, "Victim Channel", &baseURL)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/upstream-models", victim.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusForbidden)
}

// TestChannelV2_SameTenantControl guards against over-tightening: a tenant
// admin accessing its own channel must still succeed (200), not regress to
// 403 once the tenant-ownership check lands.
func TestChannelV2_SameTenantControl(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	mine := SeedV2Channel(t, ctx, "Mine")

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", mine.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	if data["name"] != "Mine" {
		t.Errorf("expected name='Mine', got %v", data["name"])
	}
}
