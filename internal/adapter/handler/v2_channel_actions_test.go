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

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func seedChannelWithBase(t *testing.T, ctx *V2TestContext, name, baseURL string) *repo.Channel {
	t.Helper()
	u := baseURL
	ch := &repo.Channel{
		Name:        name,
		TenantId:    ctx.TenantID,
		Key:         "sk-test-actions-key",
		Status:      1,
		Type:        1,
		Models:      "gpt-4,gpt-3.5-turbo",
		Group:       "default",
		BaseURL:     &u,
		CreatedTime: 1000000,
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return ch
}

// ─── TestChannelV2 ────────────────────────────────────────────────────────────

func TestV2ChannelTest_Happy(t *testing.T) {
	// Mock upstream that returns a valid chat completion.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	prev := channelTestHTTPClient
	channelTestHTTPClient = upstream.Client()
	defer func() { channelTestHTTPClient = prev }()

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedChannelWithBase(t, ctx, "Test Channel", upstream.URL)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/test", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, got: %v — body: %s", resp["success"], w.Body.String())
	}
	if _, ok := resp["latency_ms"]; !ok {
		t.Error("expected latency_ms in response")
	}
}

func TestV2ChannelTest_ChannelNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, "/api/v2/test-tenant/channels/99999/test", nil, []string{"admin"})
	AssertV2Status(t, w, http.StatusNotFound)
	resp := ParseV2Response(t, w)
	if resp["success"] != false {
		t.Error("expected success=false")
	}
}

func TestV2ChannelTest_TenantMismatch_NoAdmin(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	ch := seedChannelWithBase(t, ctx, "Admin Only Channel", upstream.URL)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/test", ch.Id)
	// Normal user (no admin role) — should receive 403.
	w := V2RequestAsUser(ctx, ctx.NormalUser, http.MethodPost, path, nil, nil)
	AssertV2Status(t, w, http.StatusForbidden)
}

// ─── FetchUpstreamModelsV2 ────────────────────────────────────────────────────

func TestV2UpstreamModels_Happy(t *testing.T) {
	// Mock /v1/models that returns a list with one new model and one missing.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// upstream has: gpt-4 (already in channel), gpt-4o (new), gpt-3.5-turbo (already in channel)
		// channel has: gpt-4, gpt-3.5-turbo, claude-3 (missing from upstream)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4"},{"id":"gpt-4o"},{"id":"gpt-3.5-turbo"}]}`))
	}))
	defer upstream.Close()

	prev := channelTestHTTPClient
	channelTestHTTPClient = upstream.Client()
	defer func() { channelTestHTTPClient = prev }()

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	baseURL := upstream.URL
	ch := &repo.Channel{
		Name:        "Sync Channel",
		TenantId:    ctx.TenantID,
		Key:         "sk-sync-key",
		Status:      1,
		Type:        1,
		Models:      "gpt-4,gpt-3.5-turbo,claude-3",
		Group:       "default",
		BaseURL:     &baseURL,
		CreatedTime: 1000000,
	}
	if err := ctx.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/upstream-models", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)
	resp := ParseV2Response(t, w)
	if resp["success"] != true {
		t.Fatalf("expected success=true, body: %s", w.Body.String())
	}

	dataRaw, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got: %T", resp["data"])
	}

	upstreamList := dataRaw["upstream"].([]interface{})
	if len(upstreamList) != 3 {
		t.Errorf("expected 3 upstream models, got %d", len(upstreamList))
	}

	newList := dataRaw["new"].([]interface{})
	if len(newList) != 1 || newList[0] != "gpt-4o" {
		t.Errorf("expected new=[gpt-4o], got %v", newList)
	}

	missingList := dataRaw["missing"].([]interface{})
	if len(missingList) != 1 || missingList[0] != "claude-3" {
		t.Errorf("expected missing=[claude-3], got %v", missingList)
	}
}

func TestV2UpstreamModels_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer upstream.Close()

	prev := channelTestHTTPClient
	channelTestHTTPClient = upstream.Client()
	defer func() { channelTestHTTPClient = prev }()

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedChannelWithBase(t, ctx, "Error Channel", upstream.URL)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/upstream-models", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodGet, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusBadGateway)
	resp := ParseV2Response(t, w)
	if resp["success"] != false {
		t.Error("expected success=false on upstream 401")
	}
}

// ─── extractJSONError unit test ───────────────────────────────────────────────

func TestExtractJSONError(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"no error field", `{"choices":[]}`, ""},
		{"with error message", `{"error":{"message":"invalid key"}}`, "invalid key"},
		{"non-json", "not json", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSONError([]byte(tc.input))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ─── response shape regression ────────────────────────────────────────────────

func TestV2ChannelTest_ResponseShape(t *testing.T) {
	// Upstream returns an error payload (HTTP 200 but error in body).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer upstream.Close()

	prev := channelTestHTTPClient
	channelTestHTTPClient = upstream.Client()
	defer func() { channelTestHTTPClient = prev }()

	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	ch := seedChannelWithBase(t, ctx, "Quota Channel", upstream.URL)

	path := fmt.Sprintf("/api/v2/test-tenant/channels/%d/test", ch.Id)
	w := V2RequestAsUser(ctx, ctx.AdminUser, http.MethodPost, path, nil, []string{"admin"})

	AssertV2Status(t, w, http.StatusOK)

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["success"] != false {
		t.Error("expected success=false when upstream returns error payload")
	}
	errMsg, _ := body["error"].(string)
	if errMsg != "quota exceeded" {
		t.Errorf("expected error='quota exceeded', got %q", errMsg)
	}
}
