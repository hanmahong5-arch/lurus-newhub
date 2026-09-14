package relay

// responses_handler_test.go — business-acceptance tests for POST
// /v1/responses/compact's subset gate inside ResponsesHelper (cycle-8 L6,
// wire-formats-03): an unsupported channel is refused with zero upstream
// calls, and the documented subset — never the caller's raw body — is what
// reaches the vendor, in BOTH the converted and pass-through code paths.

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// responsesHelperRegistryDB wraps setupRelayDB with the response_registry
// table — setupRelayDB's own table list (relay_task_test.go) predates this
// lane and does not include entity.ResponseRegistry, so tests that need it
// migrate it in on top rather than editing that shared helper's table list.
func responsesHelperRegistryDB(t *testing.T) func() {
	t.Helper()
	cleanup := setupRelayDB(t)
	if err := repo.DB.AutoMigrate(&entity.ResponseRegistry{}); err != nil {
		t.Fatalf("auto migrate ResponseRegistry: %v", err)
	}
	return cleanup
}

// TestResponsesHelper_InsertsRegistryOnSuccess is the oracle for cycle-8
// L7's (tasks-plugins-12) insert hook: a successful, non-compact POST
// /v1/responses on an OpenAI-type channel writes a response_registry row
// keyed by the vendor id, carrying the tenant/user/token/channel/model that
// produced it. Drives the real ResponsesHelper end-to-end (convert ->
// dispatch -> DoResponse -> postConsumeQuota -> insert hook), not a
// hand-built entity.ResponseRegistry row.
func TestResponsesHelper_InsertsRegistryOnSuccess(t *testing.T) {
	cleanup := responsesHelperRegistryDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "registry-insert", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	respReq := &dto.OpenAIResponsesRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses", relayconstant.RelayModeResponses, "gpt-4o-mini", respReq)
	c.Set("token_name", "tkn")
	c.Set("tenant_id", "registry-tenant")
	info.UserId = u.Id
	info.TokenId = 55

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error: %v", err.Error())
	}

	// responsesBody (relay_task_test.go) has id "r1".
	row, err := repo.GetResponseRegistry("r1")
	if err != nil {
		t.Fatalf("GetResponseRegistry(r1): %v", err)
	}
	if row.TenantId != "registry-tenant" {
		t.Errorf("TenantId = %q, want %q", row.TenantId, "registry-tenant")
	}
	if row.UserId != u.Id {
		t.Errorf("UserId = %d, want %d", row.UserId, u.Id)
	}
	if row.TokenId != 55 {
		t.Errorf("TokenId = %d, want 55", row.TokenId)
	}
	if row.ChannelId != 1 {
		t.Errorf("ChannelId = %d, want 1 (wireOpenAIUpstream's ContextKeyChannelId)", row.ChannelId)
	}
	if row.UpstreamModel != "gpt-4o-mini" {
		t.Errorf("UpstreamModel = %q, want %q", row.UpstreamModel, "gpt-4o-mini")
	}
	if row.ExpiresAt <= row.CreatedAt {
		t.Errorf("ExpiresAt (%d) must be after CreatedAt (%d)", row.ExpiresAt, row.CreatedAt)
	}
}

// TestResponsesHelper_StoreFalseSkipsInsert is the oracle for the
// store:false opt-out: no response_registry row is written even though the
// vendor still returns an id and the response is billed normally.
func TestResponsesHelper_StoreFalseSkipsInsert(t *testing.T) {
	cleanup := responsesHelperRegistryDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "registry-store-false", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	respReq := &dto.OpenAIResponsesRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`), Store: []byte("false")}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses", relayconstant.RelayModeResponses, "gpt-4o-mini", respReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error: %v", err.Error())
	}

	if _, err := repo.GetResponseRegistry("r1"); !errors.Is(err, repo.ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry(r1) = %v, want ErrResponseRegistryNotFound (store:false must opt out)", err)
	}
}

// TestResponsesHelper_NonOpenAIChannelSkipsInsert is the oracle for the
// channel-type gate: a channel type outside common.SupportsResponsesStateful's
// allow-list writes no registry row even though the response itself
// succeeds (unlike /v1/responses/compact, plain /v1/responses does not
// refuse the request for an unsupported channel — only the registry insert
// is gated).
func TestResponsesHelper_NonOpenAIChannelSkipsInsert(t *testing.T) {
	cleanup := responsesHelperRegistryDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "registry-non-openai", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	respReq := &dto.OpenAIResponsesRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses", relayconstant.RelayModeResponses, "gpt-4o-mini", respReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id
	// A synthetic channel type not in the ChannelType2APIType switch (falls
	// back to the OpenAI adaptor for dispatch, so the response still
	// succeeds) AND not in responsesCompactSupportedChannels's allow-list
	// (so the insert hook is skipped) — proves the two gates are genuinely
	// independent checks, not one masking the other.
	common.SetContextKey(c, constant.ContextKeyChannelType, 999)

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error: %v", err.Error())
	}

	if _, err := repo.GetResponseRegistry("r1"); !errors.Is(err, repo.ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry(r1) = %v, want ErrResponseRegistryNotFound (unsupported channel type must skip insert)", err)
	}
}

// TestResponsesHelper_MultiKeyChannelSkipsInsert is the oracle for cycle-8
// L7 repair round finding B-F3: a multi-key channel writes no registry row.
// The row pins channel_id but not the key index GetNextEnabledKey selected
// for THIS request, and a multi-key channel picks a key per request
// (random or polling) — a later GET/DELETE re-selecting through
// SetupContextForSelectedChannel could run with a DIFFERENT key than the
// one that minted the id, so the vendor would answer its own 404 for an
// account that never created the response. Migration 036 (frozen this
// cycle) has no key-index column, so the interim fix is skipping the
// insert entirely for a multi-key channel — see
// maybeUpsertResponseRegistry's doc comment.
func TestResponsesHelper_MultiKeyChannelSkipsInsert(t *testing.T) {
	cleanup := responsesHelperRegistryDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "registry-multikey", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	respReq := &dto.OpenAIResponsesRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses", relayconstant.RelayModeResponses, "gpt-4o-mini", respReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id
	// An OpenAI-type channel (would otherwise pass SupportsResponsesStateful)
	// that distributor.go marked multi-key on this request — set through the
	// SAME context key SetupContextForSelectedChannel/distributor.go writes
	// (ContextKeyChannelIsMultiKey), which ResponsesHelper's own
	// info.InitChannelMeta(c) call reads into info.ChannelIsMultiKey; setting
	// the RelayInfo field directly here would panic (info.ChannelMeta is nil
	// until InitChannelMeta runs).
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error: %v", err.Error())
	}

	if _, err := repo.GetResponseRegistry("r1"); !errors.Is(err, repo.ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry(r1) = %v, want ErrResponseRegistryNotFound (multi-key channel must skip insert)", err)
	}
}

// TestResponsesHelper_RegistryInsertFailureDoesNotFailResponse is the
// oracle for the hard requirement in maybeUpsertResponseRegistry's doc
// comment: a registry write failure must never fail the billed response.
// Forces a real DB error (dropped table) rather than a mock, and asserts
// both that ResponsesHelper still returns nil AND that the failure was
// actually counted, not silently swallowed twice over.
func TestResponsesHelper_RegistryInsertFailureDoesNotFailResponse(t *testing.T) {
	cleanup := responsesHelperRegistryDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "registry-insert-fail", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := repo.DB.Migrator().DropTable(&entity.ResponseRegistry{}); err != nil {
		t.Fatalf("drop response_registry table: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	before := testutil.ToFloat64(metrics.ResponseRegistryErrorsTotal)

	respReq := &dto.OpenAIResponsesRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses", relayconstant.RelayModeResponses, "gpt-4o-mini", respReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error despite the registry-insert-failure contract: %v", err.Error())
	}

	if got := testutil.ToFloat64(metrics.ResponseRegistryErrorsTotal) - before; got != 1 {
		t.Errorf("ResponseRegistryErrorsTotal delta = %v, want 1", got)
	}

	var refreshed repo.User
	if dbErr := repo.DB.First(&refreshed, u.Id).Error; dbErr != nil {
		t.Fatalf("reload user: %v", dbErr)
	}
	if refreshed.RequestCount != 1 {
		t.Errorf("RequestCount = %d, want 1 (billing must still have run)", refreshed.RequestCount)
	}
}

// TestResponsesHelper_CompactGate400_ZeroUpstreamHits is the oracle for the
// channel-type gate: a channel type outside
// common.SupportsResponsesCompact's allow-list this cycle must be refused
// with the typed 400 BEFORE any HTTP request reaches the upstream server.
// Uses channel type 999 (the same technique
// TestResponsesHelper_NonOpenAIChannelSkipsInsert uses), NOT
// constant.ChannelTypeAnthropic — an Anthropic channel type resolves to the
// claude adaptor, whose ConvertOpenAIResponsesRequest fails before
// DoRequest is ever reached, so the mutation "remove the channel-type gate"
// would NOT actually turn the hit counter from 0 into 1 for that channel
// type (the request would still die, just later and for a different
// reason). Channel type 999 falls back to the OpenAI adaptor via
// common.ChannelType2APIType's default branch, so with the gate removed the
// request genuinely reaches the upstream server.
func TestResponsesHelper_CompactGate400_ZeroUpstreamHits(t *testing.T) {
	// A database is wired even though the gate rejects before any billing:
	// without one, removing the gate makes this test die on a nil database in
	// postConsumeQuota instead of on the upstream-hit assertion below, and a
	// red for the wrong reason proves nothing about the gate.
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "compact-gate-zero-hits", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	compactReq := &dto.OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses/compact", relayconstant.RelayModeResponsesCompact, "gpt-4o-mini", compactReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id
	// Channel type 999 resolves to the OpenAI adaptor (common.ChannelType2APIType's
	// default arm) but is not in the compact allow-list, so the request would
	// otherwise travel all the way to the upstream.
	common.SetContextKey(c, constant.ContextKeyChannelType, 999)

	apiErr := ResponsesHelper(c, info)
	if apiErr == nil {
		t.Fatal("expected a typed 400 for an unsupported channel type, got nil")
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeResponsesCompactUnsupported {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeResponsesCompactUnsupported)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("upstream hit count = %d, want 0 (the gate must reject before any upstream call)", got)
	}
}

// TestResponsesHelper_CompactSubsetGateBeforePassThrough is the oracle for
// wire-formats-03's core safety property: even with pass-through enabled
// (ChannelSetting.PassThroughBodyEnabled=true), a caller who smuggled
// background/stream/conversation into the raw request body must NOT have
// those fields reach the vendor. The mutation "forward the raw body instead
// of the subset" (reverting the pass-through branch back to
// common.GetRequestBody(c)) makes this red.
func TestResponsesHelper_CompactSubsetGateBeforePassThrough(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "compact-passthrough", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var sawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	rawBody := `{"model":"gpt-4o-mini","input":"pass-through-marker","background":true,"stream":true,"conversation":"conv_smuggled"}`
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader([]byte(rawBody)))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test-key")
	common.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o-mini")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
	c.Set("token_name", "tkn")

	compactReq := &dto.OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini", Input: []byte(`"pass-through-marker"`)}
	info := &relaycommon.RelayInfo{
		Request:         compactReq,
		OriginModelName: "gpt-4o-mini",
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		RequestURLPath:  "/v1/responses/compact",
		StartTime:       time.Now(),
		UserId:          u.Id,
	}

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper compact pass-through returned error: %v", err.Error())
	}
	if sawBody == nil {
		t.Fatal("upstream never received a request body")
	}
	if !bytes.Contains(sawBody, []byte("pass-through-marker")) {
		t.Errorf("documented subset field (input) not forwarded: %s", sawBody)
	}
	for _, smuggled := range []string{"background", "\"stream\"", "conversation", "conv_smuggled"} {
		if bytes.Contains(sawBody, []byte(smuggled)) {
			t.Errorf("smuggled field %q reached the vendor in pass-through mode: %s", smuggled, sawBody)
		}
	}
}

// TestResponsesHelper_CompactForwardsSubset is the oracle for the
// non-pass-through (converted) path: the documented subset fields reach the
// vendor at <base>/v1/responses/compact.
func TestResponsesHelper_CompactForwardsSubset(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "compact-forward", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var sawBody []byte
	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	compactReq := &dto.OpenAIResponsesCompactionRequest{
		Model:              "gpt-4o-mini",
		Input:              []byte(`"forward-marker"`),
		Instructions:       []byte(`"be terse"`),
		PreviousResponseID: "resp_prev",
	}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses/compact", relayconstant.RelayModeResponsesCompact, "gpt-4o-mini", compactReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper compact forward returned error: %v", err.Error())
	}
	if !strings.HasSuffix(sawPath, "/v1/responses/compact") {
		t.Errorf("upstream path = %q, want it to end in /v1/responses/compact", sawPath)
	}
	for _, want := range []string{"forward-marker", "be terse", "resp_prev"} {
		if !bytes.Contains(sawBody, []byte(want)) {
			t.Errorf("documented subset field %q not forwarded: %s", want, sawBody)
		}
	}
}

// TestResponsesHelper_CompactBillsEstimateWhenUpstreamOmitsUsage is the
// oracle for repair-round finding A-F3: OaiResponsesCompactHandler's
// estimate-fallback (info.GetEstimatePromptTokens() when the vendor sends no
// usage object) is only exercised on the real path if DoResponse actually
// dispatches RelayModeResponsesCompact to that handler — deleting
// adaptor.go's `case relayconstant.RelayModeResponsesCompact:` in DoResponse
// left every package in the lane's test_packages green before this test
// existed. Drives the full converted (non-pass-through) path and asserts
// the billed prompt_tokens on the resulting consume-log row equals the
// pre-request estimate. This alone does NOT prove DoResponse routed to
// OaiResponsesCompactHandler: the default chat-completions handler falls
// back to the same estimate when it finds prompt_tokens==0 (relay-openai.go),
// so this no-usage case is one where the two handlers agree — see
// TestResponsesHelper_CompactBillsResponsesShapedUsage below for the oracle
// that actually distinguishes them.
func TestResponsesHelper_CompactBillsEstimateWhenUpstreamOmitsUsage(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()
	// setupRelayDB turns off consume-log writes for its other callers; this
	// test's oracle IS the consume-log row, so it needs them on.
	prevLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	defer func() { common.LogConsumeEnabled = prevLogConsume }()

	u := &repo.User{Username: "compact-bill-estimate", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// no usage object at all
		_, _ = w.Write([]byte(`{"id":"resp_no_usage","status":"completed","output":[]}`))
	}))
	defer srv.Close()

	compactReq := &dto.OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses/compact", relayconstant.RelayModeResponsesCompact, "gpt-4o-mini", compactReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id
	const estimatedPromptTokens = 4242
	info.SetEstimatePromptTokens(estimatedPromptTokens)

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error: %v", err.Error())
	}

	var logs []repo.Log
	if err := repo.DB.Where("user_id = ?", u.Id).Find(&logs).Error; err != nil {
		t.Fatalf("query consume log: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("got %d consume log rows, want 1", len(logs))
	}
	if logs[0].PromptTokens != estimatedPromptTokens {
		t.Errorf("billed PromptTokens = %d, want %d (the pre-request estimate)", logs[0].PromptTokens, estimatedPromptTokens)
	}
}

// TestResponsesHelper_SameRelayInfoRetriesAfterMutation is the oracle for
// repair-round finding A-F1: ResponsesHelper must not mutate the shared
// info.Request field, because handler.Relay builds ONE *RelayInfo and reuses
// the same pointer for every failover attempt. Drives ResponsesHelper twice
// with the SAME *relaycommon.RelayInfo — first against a 500 upstream (the
// class of failure a real retry loop would fail over on), then against a 200
// — and asserts the second call succeeds. Before the fix, the first call
// swapped info.Request from *dto.OpenAIResponsesCompactionRequest to
// *dto.OpenAIResponsesRequest, so the second call's type assertion at the
// top of the compact branch would fail and return 400 invalid_request
// instead of reaching the (now-healthy) upstream.
func TestResponsesHelper_SameRelayInfoRetriesAfterMutation(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "compact-retry-same-info", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var attempt atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempt.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(upstreamErrBody))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	compactReq := &dto.OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses/compact", relayconstant.RelayModeResponsesCompact, "gpt-4o-mini", compactReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id

	firstErr := ResponsesHelper(c, info)
	if firstErr == nil {
		t.Fatal("expected the first (500-upstream) attempt to return an error")
	}

	secondErr := ResponsesHelper(c, info)
	if secondErr != nil {
		t.Fatalf("second attempt (same *RelayInfo, healthy upstream) returned error: %v — info.Request must not have been mutated by the first attempt", secondErr.Error())
	}
}

// TestResponsesHelper_CompactPassThroughAppliesDisabledFieldPolicy is the
// oracle for repair-round finding B-F2: the compact pass-through branch must
// apply the same channel disabled-field policy the converted branch applies
// (relaycommon.RemoveDisabledFields). Without it, a channel with
// AllowServiceTier=false (the zero value / documented default) would still
// let service_tier reach the vendor when PassThroughBodyEnabled is set,
// letting a caller reach a premium vendor tier newhub prices at the
// standard ratio.
func TestResponsesHelper_CompactPassThroughAppliesDisabledFieldPolicy(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "compact-passthrough-disabled-field", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var sawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader([]byte(`{}`)))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, srv.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test-key")
	common.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o-mini")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
	// AllowServiceTier is the zero value (false) here — the documented
	// default that RemoveDisabledFields strips service_tier under.
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
	c.Set("token_name", "tkn")

	compactReq := &dto.OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`), ServiceTier: "priority"}
	info := &relaycommon.RelayInfo{
		Request:         compactReq,
		OriginModelName: "gpt-4o-mini",
		RelayMode:       relayconstant.RelayModeResponsesCompact,
		RequestURLPath:  "/v1/responses/compact",
		StartTime:       time.Now(),
		UserId:          u.Id,
	}

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper compact pass-through returned error: %v", err.Error())
	}
	if sawBody == nil {
		t.Fatal("upstream never received a request body")
	}
	if bytes.Contains(sawBody, []byte("service_tier")) {
		t.Errorf("service_tier reached the vendor despite AllowServiceTier=false: %s", sawBody)
	}
}

// TestResponsesHelper_CompactBillsResponsesShapedUsage pins the DoResponse
// dispatch itself. The Responses API reports usage as input_tokens /
// output_tokens; the chat-completions handler the default switch arm falls
// through to reads prompt_tokens / completion_tokens and finds neither, so a
// consume row carrying 5/3 can only come from the compact handler. The
// sibling estimate test cannot tell the two apart, because both bill the
// estimate when no usage is parsed.
func TestResponsesHelper_CompactBillsResponsesShapedUsage(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()
	app.InitHttpClient()
	prevLogConsume := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	defer func() { common.LogConsumeEnabled = prevLogConsume }()

	u := &repo.User{Username: "compact-bill-usage", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_usage","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`))
	}))
	defer srv.Close()

	compactReq := &dto.OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses/compact", relayconstant.RelayModeResponsesCompact, "gpt-4o-mini", compactReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id
	info.SetEstimatePromptTokens(4242)

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error: %v", err.Error())
	}

	var logs []repo.Log
	if err := repo.DB.Where("user_id = ?", u.Id).Find(&logs).Error; err != nil {
		t.Fatalf("query consume log: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("got %d consume log rows, want 1", len(logs))
	}
	if logs[0].PromptTokens != 5 || logs[0].CompletionTokens != 3 {
		t.Errorf("billed prompt/completion = %d/%d, want 5/3 — the vendor's Responses-shaped usage, which only OaiResponsesCompactHandler parses",
			logs[0].PromptTokens, logs[0].CompletionTokens)
	}
}

// TestResponsesHelper_CompactSkipsRegistryInsert pins the interaction between
// the two response lanes: the compact endpoint is stateless, so a compact call
// must not write a response-registry row even against a channel type that
// otherwise would. Today the compact handler never stashes a response id, so
// the insert would be skipped anyway; this asserts the explicit branch stays,
// because the day the compact handler starts stashing an id, a silently
// retained row would hand out a retrieve id the vendor never stored.
func TestResponsesHelper_CompactSkipsRegistryInsert(t *testing.T) {
	cleanup := responsesHelperRegistryDB(t)
	defer cleanup()
	app.InitHttpClient()

	u := &repo.User{Username: "registry-compact-skip", Quota: 10_000_000}
	if err := repo.DB.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responsesBody))
	}))
	defer srv.Close()

	compactReq := &dto.OpenAIResponsesCompactionRequest{Model: "gpt-4o-mini", Input: []byte(`"hi"`)}
	c, info := wireOpenAIUpstream(t, srv.URL, "/v1/responses/compact", relayconstant.RelayModeResponsesCompact, "gpt-4o-mini", compactReq)
	c.Set("token_name", "tkn")
	info.UserId = u.Id
	// The compact handler forwards the vendor's bytes verbatim, so stash the
	// id the way the non-compact handler would: if the registry branch ran,
	// it would now have everything it needs to write a row.
	c.Set("responses_id", "r1")

	if err := ResponsesHelper(c, info); err != nil {
		t.Fatalf("ResponsesHelper returned error: %v", err.Error())
	}

	if _, err := repo.GetResponseRegistry("r1"); !errors.Is(err, repo.ErrResponseRegistryNotFound) {
		t.Errorf("GetResponseRegistry(r1) = %v, want ErrResponseRegistryNotFound — a compact call must not register a retrievable response id", err)
	}
}
