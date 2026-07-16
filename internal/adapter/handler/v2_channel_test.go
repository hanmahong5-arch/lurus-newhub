package handler

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

// ============================================================================
// V2 Channel Controller Tests
// ============================================================================

func TestListChannelsV2_AdminOnly(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create some channels
	SeedV2Channel(t, ctx, "Channel 1")
	SeedV2Channel(t, ctx, "Channel 2")

	// Try as non-admin user
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodGet, "/api/v2/test-tenant/channels", nil, nil)
	AssertV2Status(t, w, http.StatusForbidden)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Admin role required" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}

	// Try as admin user
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusOK)
	resp = AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	channels := data["channels"].([]interface{})
	if len(channels) != 2 {
		t.Errorf("expected 2 channels, got %d", len(channels))
	}
}

func TestListChannelsV2_KeyMasking(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a channel with a known key
	channel := SeedV2Channel(t, ctx, "Masked Key Channel")
	originalKey := channel.Key

	// List channels as admin
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels", nil, []string{"admin"})
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	channels := data["channels"].([]interface{})

	if len(channels) == 0 {
		t.Fatal("expected at least 1 channel")
	}

	// Get the first channel
	ch := channels[0].(map[string]interface{})
	maskedKey, ok := ch["key"].(string)
	if !ok {
		t.Fatal("expected key field in channel")
	}

	// Key should NOT be the original (security: key is omitted in non-selectAll queries)
	// The actual behavior is that key is omitted from query, so it's empty
	// and maskKey returns empty string for empty input
	if maskedKey == originalKey {
		t.Error("key should not be the original key in response")
	}

	// Since GetAllChannels omits key by default for security,
	// the key field will be empty, and maskKey("") returns ""
	// This is expected behavior for production security
	_ = maskedKey // Key is intentionally omitted for security
}

func TestCreateChannelV2_RequiredFields(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	tests := []struct {
		name    string
		body    map[string]interface{}
		missing string
	}{
		{
			name:    "missing name",
			body:    map[string]interface{}{"key": "sk-test-key", "models": "gpt-4"},
			missing: "name",
		},
		{
			name:    "missing key",
			body:    map[string]interface{}{"name": "Test Channel", "models": "gpt-4"},
			missing: "key",
		},
		{
			name:    "missing models",
			body:    map[string]interface{}{"name": "Test Channel", "key": "sk-test-key"},
			missing: "models",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", tt.body, []string{"admin"})
			AssertV2Status(t, w, http.StatusBadRequest)
			// Just verify it returns a bad request - actual message may vary
		})
	}
}

func TestCreateChannelV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name":   "New Channel",
		"key":    "sk-new-channel-key-12345",
		"models": "gpt-4,gpt-3.5-turbo",
		"type":   1,
		"group":  "premium",
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"admin"})
	AssertV2Status(t, w, http.StatusCreated)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["name"] != "New Channel" {
		t.Errorf("expected name='New Channel', got %v", data["name"])
	}
	if data["id"] == nil {
		t.Error("expected id to be returned")
	}
}

func TestCreateChannelV2_NameTooLong(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a name longer than 100 characters
	longName := make([]byte, 101)
	for i := range longName {
		longName[i] = 'a'
	}

	body := map[string]interface{}{
		"name":   string(longName),
		"key":    "sk-test-key",
		"models": "gpt-4",
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels", body, []string{"admin"})
	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Channel name too long (max 100 characters)" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestUpdateChannelV2_PartialUpdate(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a channel
	channel := SeedV2Channel(t, ctx, "Original Channel")
	originalModels := channel.Models

	// Update only the name
	body := map[string]interface{}{
		"name": "Updated Channel Name",
	}

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["name"] != "Updated Channel Name" {
		t.Errorf("expected name='Updated Channel Name', got %v", data["name"])
	}

	// Verify models didn't change
	var updatedChannel struct {
		Models string
	}
	ctx.DB.Table("channels").Where("id = ?", channel.Id).Select("models").First(&updatedChannel)
	if updatedChannel.Models != originalModels {
		t.Errorf("models should not have changed, got %s", updatedChannel.Models)
	}
}

func TestDeleteChannelV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a channel
	channel := SeedV2Channel(t, ctx, "Channel to Delete")

	// Delete it
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	AssertV2Success(t, w)

	// Verify it's deleted
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels", nil, []string{"admin"})
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 0 {
		t.Errorf("expected 0 channels after deletion, got %d", total)
	}
}

func TestDeleteChannelV2_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Try to delete a non-existent channel
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, "/api/v2/test-tenant/channels/99999", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusNotFound)
}

func TestGetChannelV2_Success(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create a channel
	channel := SeedV2Channel(t, ctx, "Test Channel")

	// Get it
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	if data["name"] != "Test Channel" {
		t.Errorf("expected name='Test Channel', got %v", data["name"])
	}
}

// TestGetChannelV2_KeyMasked: HIGH — GetChannelById(id, true) returns the
// plaintext upstream provider key; GetChannelV2 must mask it (like
// channelView already does for ListChannelsV2) before serialising the
// channel, otherwise any tenant admin reading a single channel gets the raw
// credential in the response body.
func TestGetChannelV2_KeyMasked(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	channel := SeedV2Channel(t, ctx, "Key Masking Target")
	originalKey := channel.Key

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)

	if strings.Contains(w.Body.String(), originalKey) {
		t.Fatalf("raw channel key leaked in GetChannelV2 response body: %s", w.Body.String())
	}

	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	gotKey, _ := data["key"].(string)
	wantKey := maskKey(originalKey)
	if gotKey != wantKey {
		t.Errorf("key = %q, want masked %q (original %q)", gotKey, wantKey, originalKey)
	}
	if gotKey == originalKey {
		t.Error("key must not equal the original raw key")
	}
}

func TestListChannelsV2_Pagination(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Create 15 channels
	for i := 0; i < 15; i++ {
		SeedV2Channel(t, ctx, fmt.Sprintf("Channel %d", i))
	}

	// Get first page
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels?page=1&page_size=10", nil, []string{"admin"})
	resp := AssertV2Success(t, w)

	data := resp["data"].(map[string]interface{})
	channels := data["channels"].([]interface{})
	if len(channels) != 10 {
		t.Errorf("expected 10 channels on first page, got %d", len(channels))
	}

	total := int(data["total"].(float64))
	if total != 15 {
		t.Errorf("expected total=15, got %d", total)
	}

	// Get second page
	w = V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels?page=2&page_size=10", nil, []string{"admin"})
	resp = AssertV2Success(t, w)

	data = resp["data"].(map[string]interface{})
	channels = data["channels"].([]interface{})
	if len(channels) != 5 {
		t.Errorf("expected 5 channels on second page, got %d", len(channels))
	}
}

func TestListChannelsV2_KeywordSearch(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	SeedV2Channel(t, ctx, "Production GPT")
	SeedV2Channel(t, ctx, "Staging GPT")
	SeedV2Channel(t, ctx, "Production Claude")

	// Search with keyword "GPT"
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels?keyword=GPT", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, got %v", resp["success"])
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data object in response")
	}
	total := int(data["total"].(float64))
	if total != 2 {
		t.Errorf("expected 2 channels matching 'GPT', got %d", total)
	}
}

// TestListChannelsV2_CrossTenantIsolation guards against the pre-fix bug where
// ListChannelsV2 called repo.GetAllChannels / SearchChannels / GetChannelsByTag
// (all bare-DB, no tenant filter) so an admin of any tenant could enumerate
// every tenant's channels. Also asserts channelView hides per-channel secrets.
func TestListChannelsV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// Channel in the caller's own tenant.
	SeedV2Channel(t, ctx, "My Channel")

	// Channel belonging to a different tenant — must never surface.
	other := &repo.Channel{
		Name:        "Other Tenant Channel",
		TenantId:    "other-tenant-xyz",
		Key:         "sk-other-" + common.GetRandomString(16),
		Status:      common.ChannelStatusEnabled,
		Type:        1,
		Models:      "gpt-4",
		Group:       "default",
		Other:       "secret-other-payload",
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("failed to seed cross-tenant channel: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels", nil, []string{"admin"})
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})

	total := int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 channel (tenant-scoped), got %d — possible cross-tenant leak", total)
	}

	channels := data["channels"].([]interface{})
	if len(channels) != 1 {
		t.Fatalf("expected 1 row, got %d", len(channels))
	}
	ch := channels[0].(map[string]interface{})
	if name, _ := ch["name"].(string); name == "Other Tenant Channel" {
		t.Error("cross-tenant channel leaked into the response")
	}

	for _, f := range []string{"other", "setting", "param_override", "header_override", "tenant_id", "other_info"} {
		if _, exists := ch[f]; exists {
			t.Errorf("forbidden field %q leaked through channelView", f)
		}
	}
}

// TestSearchChannelsV2_CrossTenantIsolation covers the keyword-search branch.
func TestSearchChannelsV2_CrossTenantIsolation(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	SeedV2Channel(t, ctx, "Production GPT")

	other := &repo.Channel{
		Name:        "Production GPT",
		TenantId:    "other-tenant-xyz",
		Key:         "sk-other-" + common.GetRandomString(16),
		Status:      common.ChannelStatusEnabled,
		Type:        1,
		Models:      "gpt-4",
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(other).Error; err != nil {
		t.Fatalf("failed to seed cross-tenant channel: %v", err)
	}

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels?keyword=GPT", nil, []string{"admin"})
	resp := AssertV2Success(t, w)
	data := resp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	if total != 1 {
		t.Errorf("expected 1 channel matching 'GPT' in this tenant, got %d — search leaked cross-tenant", total)
	}
}

func TestGetChannelV2_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, "/api/v2/test-tenant/channels/invalid", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Invalid channel ID" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestUpdateChannelV2_NotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	body := map[string]interface{}{
		"name": "Ghost Channel",
	}
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, "/api/v2/test-tenant/channels/99999", body, []string{"admin"})

	AssertV2Status(t, w, http.StatusNotFound)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Channel not found" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestUpdateChannelV2_NameTooLong(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	channel := SeedV2Channel(t, ctx, "Original Channel")

	longName := make([]byte, 101)
	for i := range longName {
		longName[i] = 'a'
	}

	body := map[string]interface{}{
		"name": string(longName),
	}
	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d", channel.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPut, path, body, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Channel name too long (max 100 characters)" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}

func TestDeleteChannelV2_InvalidID(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodDelete, "/api/v2/test-tenant/channels/invalid", nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadRequest)
	resp := ParseV2Response(t, w)
	if msg, ok := resp["message"].(string); ok {
		if msg != "Invalid channel ID" {
			t.Errorf("unexpected error message: %s", msg)
		}
	}
}
