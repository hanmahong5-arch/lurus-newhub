package handler

// ============================================================================
// Business scope: Ollama channel model-lifecycle operations
// (internal/adapter/handler/channel.go) — pull/pull-stream/delete/version.
// These let a tenant admin manage models on a self-hosted Ollama upstream
// directly from the console. Edge cases covered: malformed/missing body
// fields, channel not found, wrong channel type, cross-tenant IDOR (must be
// rejected BEFORE any call reaches the upstream), and both the success and
// upstream-failure paths against a local mock server standing in for Ollama.
// ============================================================================

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/gin-gonic/gin"
)

func handler_channel_postCtx(body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

// ─── OllamaPullModel ────────────────────────────────────────────────────────

func TestOllamaPullModel_MalformedBody(t *testing.T) {
	c, w := handler_channel_postCtx("{not json")
	OllamaPullModel(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", w.Code)
	}
}

func TestOllamaPullModel_MissingFields(t *testing.T) {
	cases := []string{
		`{"channel_id":0,"model_name":"llama3"}`,
		`{"channel_id":1,"model_name":""}`,
		`{}`,
	}
	for _, body := range cases {
		c, w := handler_channel_postCtx(body)
		OllamaPullModel(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body=%s: expected 400, got %d", body, w.Code)
		}
	}
}

func TestOllamaPullModel_ChannelNotFound(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	c, w := handler_channel_postCtx(`{"channel_id":999999,"model_name":"llama3"}`)
	OllamaPullModel(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing channel, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestOllamaPullModel_WrongChannelType(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, "https://api.openai.com", "sk-x")
	c, w := handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(ch.Id) + `,"model_name":"llama3"}`)
	OllamaPullModel(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-ollama channel, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestOllamaPullModel_CrossTenantIDOR proves the tenant boundary is enforced
// even though it's checked AFTER the type check — a cross-tenant admin must
// not be able to trigger an upstream pull against another tenant's Ollama
// server (which could be used to exfiltrate data or DoS it).
func TestOllamaPullModel_CrossTenantIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	victim := &repo.Channel{
		Name: "victim-ollama", TenantId: "other-tenant-ollama", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOllama,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	c, w := handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(victim.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaPullModel(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestOllamaPullModel_SuccessAndUpstreamFailure(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, okSrv.URL, "ok")
	c, w := handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(ch.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaPullModel(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected pull success, body=%s", w.Body.String())
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	ch2 := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, errSrv.URL, "err")
	c, w = handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(ch2.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaPullModel(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on upstream failure, got %d body=%s", w.Code, w.Body.String())
	}
	resp = handler_channel_mkBody(t, w)
	if resp["success"] != false {
		t.Errorf("expected success=false on upstream failure, body=%s", w.Body.String())
	}
}

// ─── OllamaPullModelStream ──────────────────────────────────────────────────

func TestOllamaPullModelStream_GuardRails(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	// malformed body
	c, w := handler_channel_postCtx("{not json")
	OllamaPullModelStream(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", w.Code)
	}

	// missing fields
	c, w = handler_channel_postCtx(`{"channel_id":0,"model_name":""}`)
	OllamaPullModelStream(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing fields, got %d", w.Code)
	}

	// channel not found
	c, w = handler_channel_postCtx(`{"channel_id":999999,"model_name":"llama3"}`)
	OllamaPullModelStream(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing channel, got %d", w.Code)
	}
}

func TestOllamaPullModelStream_WrongTypeAndIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	notOllama := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, "https://api.openai.com", "sk-x")
	c, w := handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(notOllama.Id) + `,"model_name":"llama3"}`)
	OllamaPullModelStream(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong channel type, got %d", w.Code)
	}

	victim := &repo.Channel{
		Name: "victim-ollama-stream", TenantId: "other-tenant-ollama-2", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOllama,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	c, w = handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(victim.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaPullModelStream(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestOllamaPullModelStream_EmitsSSEProgressAndDone drives the full SSE
// happy path: the handler sets the streaming headers, relays each progress
// line from the mock Ollama server as a "data: ..." SSE frame, and always
// terminates with "data: [DONE]".
func TestOllamaPullModelStream_EmitsSSEProgressAndDone(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"downloading"}` + "\n"))
		_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
	}))
	defer srv.Close()

	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, srv.URL, "k")
	c, w := handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(ch.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaPullModelStream(c)

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected SSE content-type, got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"success"`) {
		t.Errorf("expected success progress frame relayed, body=%q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "data: [DONE]") {
		t.Errorf("expected stream to terminate with [DONE], body=%q", body)
	}

	// count SSE frames via bufio to ensure at least 2 progress events + DONE.
	scanner := bufio.NewScanner(strings.NewReader(body))
	frames := 0
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			frames++
		}
	}
	if frames < 2 {
		t.Errorf("expected at least 2 SSE frames, got %d, body=%q", frames, body)
	}
}

// TestOllamaPullModelStream_UpstreamErrorEmitsErrorFrame covers the
// non-200-from-upstream path: the handler must still emit an "error" SSE
// frame (not crash / hang) followed by [DONE].
func TestOllamaPullModelStream_UpstreamErrorEmitsErrorFrame(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, srv.URL, "k")
	c, w := handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(ch.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaPullModelStream(c)

	body := w.Body.String()
	if !strings.Contains(body, `"error"`) {
		t.Errorf("expected an error SSE frame on upstream failure, body=%q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n"), "data: [DONE]") {
		t.Errorf("expected stream to still terminate with [DONE] on error, body=%q", body)
	}
}

// ─── OllamaDeleteModel ──────────────────────────────────────────────────────

func TestOllamaDeleteModel_GuardRailsAndIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := handler_channel_postCtx("{not json")
	OllamaDeleteModel(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed body, got %d", w.Code)
	}

	c, w = handler_channel_postCtx(`{"channel_id":0,"model_name":""}`)
	OllamaDeleteModel(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing fields, got %d", w.Code)
	}

	c, w = handler_channel_postCtx(`{"channel_id":999999,"model_name":"llama3"}`)
	OllamaDeleteModel(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing channel, got %d", w.Code)
	}

	notOllama := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, "https://api.openai.com", "sk-x")
	c, w = handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(notOllama.Id) + `,"model_name":"llama3"}`)
	OllamaDeleteModel(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong channel type, got %d", w.Code)
	}

	victim := &repo.Channel{
		Name: "victim-ollama-del", TenantId: "other-tenant-ollama-3", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOllama,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	c, w = handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(victim.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaDeleteModel(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestOllamaDeleteModel_SuccessAndFailure(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/delete" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, okSrv.URL, "k")
	c, w := handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(ch.Id) + `,"model_name":"llama3"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaDeleteModel(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected delete success, body=%s", w.Body.String())
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer errSrv.Close()
	ch2 := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, errSrv.URL, "k")
	c, w = handler_channel_postCtx(`{"channel_id":` + strconv.Itoa(ch2.Id) + `,"model_name":"missing-model"}`)
	c.Set("tenant_id", ctx.TenantID)
	c.Set("id", ctx.AdminUser.Id)
	OllamaDeleteModel(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on upstream delete failure, got %d body=%s", w.Code, w.Body.String())
	}
}

// ─── OllamaVersion ──────────────────────────────────────────────────────────

func TestOllamaVersion_GuardRailsAndIDOR(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	c, w := handler_channel_getCtx2("not-a-number")
	OllamaVersion(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric id, got %d", w.Code)
	}

	c, w = handler_channel_getCtx2("999999")
	OllamaVersion(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing channel, got %d", w.Code)
	}

	notOllama := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOpenAI, "https://api.openai.com", "sk-x")
	c, w = handler_channel_getCtx(notOllama.Id, ctx.TenantID, ctx.AdminUser.Id)
	OllamaVersion(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong channel type, got %d", w.Code)
	}

	victim := &repo.Channel{
		Name: "victim-ollama-ver", TenantId: "other-tenant-ollama-4", Key: "k",
		Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOllama,
		CreatedTime: common.GetTimestamp(),
	}
	if err := ctx.DB.Create(victim).Error; err != nil {
		t.Fatalf("seed victim: %v", err)
	}
	c, w = handler_channel_getCtx(victim.Id, ctx.TenantID, ctx.AdminUser.Id)
	OllamaVersion(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestOllamaVersion_SuccessAndFailure(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.5.1"}`))
	}))
	defer okSrv.Close()
	ch := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, okSrv.URL, "k")
	c, w := handler_channel_getCtx(ch.Id, ctx.TenantID, ctx.AdminUser.Id)
	OllamaVersion(c)
	resp := handler_channel_mkBody(t, w)
	if resp["success"] != true {
		t.Fatalf("expected version fetch success, body=%s", w.Body.String())
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok || data["version"] != "0.5.1" {
		t.Fatalf("expected version=0.5.1, body=%s", w.Body.String())
	}

	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer errSrv.Close()
	ch2 := handler_channel_seedTypedChannel(t, ctx, constant.ChannelTypeOllama, errSrv.URL, "k")
	c, w = handler_channel_getCtx(ch2.Id, ctx.TenantID, ctx.AdminUser.Id)
	OllamaVersion(c)
	resp = handler_channel_mkBody(t, w)
	if resp["success"] != false {
		t.Fatalf("expected version fetch failure surfaced as success=false, body=%s", w.Body.String())
	}
}

// handler_channel_getCtx2 builds a GET context with only the id param set (no
// tenant/session identity) — used for the pre-tenant-check guard-rail cases.
func handler_channel_getCtx2(idParam string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Params = gin.Params{{Key: "id", Value: idParam}}
	return c, w
}
