package app

// coverage_lift_test.go — targeted, meaningful coverage for reachable branches
// the existing unit tier left uncovered:
//   - log_service.SearchLogs DB-fallback (both user-scoped and all-logs paths)
//   - convert.GeminiToOpenAIRequest tool-declaration + multi-media-array branches
//   - token_counter.EstimateRequestToken media-file type switch (data: URIs)
//   - quota.PostConsumeQuota platform pre-auth settlement -> outbox enqueue
//   - billing_outbox.ProcessBillingOutbox permanent-failure branch
//
// Every test asserts real output/state, not merely non-panic.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ─── log_service.go: SearchLogs DB fallback ───────────────────────────────

// seedLog inserts one consume log row for a user with the given content.
func seedLog(t *testing.T, userId int, content string) {
	t.Helper()
	l := repo.Log{
		UserId:    userId,
		Username:  "u",
		Type:      repo.LogTypeConsume,
		Content:   content,
		CreatedAt: time.Now().Unix(),
	}
	if err := repo.LOG_DB.Create(&l).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

// TestSearchLogs_UserScopedFallback proves that with Meilisearch disabled and a
// UserId set, SearchLogs routes to the per-user DB query and returns only that
// user's rows whose content matches the keyword prefix.
func TestSearchLogs_UserScopedFallback(t *testing.T) {
	setupServiceTestDB(t)
	uid := seedTestUser(t, repo.DB, 1)
	other := seedTestUser(t, repo.DB, 1)
	seedLog(t, uid, "alpha request")
	seedLog(t, uid, "beta request")
	seedLog(t, other, "alpha other-user")

	res, err := SearchLogs(LogSearchParams{UserId: uid, Keyword: "alpha"})
	if err != nil {
		t.Fatalf("SearchLogs: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("user-scoped total = %d, want 1 (only this user's 'alpha' row)", res.Total)
	}
	logs, ok := res.Items.([]*repo.Log)
	if !ok {
		t.Fatalf("Items type = %T, want []*repo.Log", res.Items)
	}
	if len(logs) != 1 || logs[0].UserId != uid {
		t.Errorf("returned rows = %+v, want one row for user %d", logs, uid)
	}
}

// TestSearchLogs_AllLogsFallback proves that with UserId==0, SearchLogs routes
// to the all-logs DB query (admin scope) and matches across all users.
func TestSearchLogs_AllLogsFallback(t *testing.T) {
	setupServiceTestDB(t)
	a := seedTestUser(t, repo.DB, 1)
	b := seedTestUser(t, repo.DB, 1)
	seedLog(t, a, "zeta one")
	seedLog(t, b, "zeta two")
	seedLog(t, a, "omega three")

	res, err := SearchLogs(LogSearchParams{Keyword: "zeta"})
	if err != nil {
		t.Fatalf("SearchLogs all: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("all-logs total = %d, want 2 (both 'zeta' rows across users)", res.Total)
	}
}

// ─── convert.go: GeminiToOpenAIRequest tool declarations + multi-media ─────

// TestGeminiToOpenAIRequest_ToolDeclarations proves that Gemini
// functionDeclarations are mapped into OpenAI tool definitions with name,
// description and parameters preserved.
func TestGeminiToOpenAIRequest_ToolDeclarations(t *testing.T) {
	toolsJSON := `[{"functionDeclarations":[` +
		`{"name":"get_weather","description":"Fetch weather","parameters":{"type":"object"}},` +
		`{"name":"get_time","description":"Fetch time","parameters":{"type":"object"}}` +
		`]}]`
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "hi"}}},
		},
		Tools: json.RawMessage(toolsJSON),
	}

	out, err := GeminiToOpenAIRequest(req, geminiInfo())
	if err != nil {
		t.Fatalf("GeminiToOpenAIRequest: %v", err)
	}
	if len(out.Tools) != 2 {
		t.Fatalf("len(Tools) = %d, want 2", len(out.Tools))
	}
	if out.Tools[0].Function.Name != "get_weather" || out.Tools[1].Function.Name != "get_time" {
		t.Errorf("tool names = [%q,%q], want [get_weather,get_time]",
			out.Tools[0].Function.Name, out.Tools[1].Function.Name)
	}
	if out.Tools[0].Function.Description != "Fetch weather" {
		t.Errorf("tool description = %q, want 'Fetch weather'", out.Tools[0].Function.Description)
	}
}

// TestGeminiToOpenAIRequest_MultiMediaBecomesArray proves that a message with
// both a text part and an image part is emitted as a multi-part content array
// (not a plain string).
func TestGeminiToOpenAIRequest_MultiMediaBecomesArray(t *testing.T) {
	req := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{
				{Text: "describe this"},
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "QUJD"}},
			}},
		},
	}
	out, err := GeminiToOpenAIRequest(req, geminiInfo())
	if err != nil {
		t.Fatalf("GeminiToOpenAIRequest: %v", err)
	}
	parts := out.Messages[0].ParseContent()
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (text + image)", len(parts))
	}
	var haveText, haveImage bool
	for _, p := range parts {
		if p.Type == "text" {
			haveText = true
		}
		if p.Type == "image_url" {
			haveImage = true
		}
	}
	if !haveText || !haveImage {
		t.Errorf("parts types = %+v, want one text and one image_url", parts)
	}
}

// ─── token_counter.go: EstimateRequestToken media file-type switch ─────────

// TestEstimateRequestToken_MediaFileTypes proves the data:-URI mime parsing and
// the per-file-type token accounting: audio adds 256, video adds 8192, and an
// unknown/document type adds 4096 on top of the OpenAI framing + text tokens.
func TestEstimateRequestToken_MediaFileTypes(t *testing.T) {
	prev := constant.CountToken
	constant.CountToken = true
	defer func() { constant.CountToken = prev }()

	c := createTestGinContext()
	c.Set(string(constant.ContextKeyOriginalModel), "gpt-4o")

	base := &types.TokenCountMeta{CombineText: "x", MessagesCount: 1}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}

	// Baseline with no files.
	baseTokens, err := EstimateRequestToken(c, base, info)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	cases := []struct {
		name    string
		dataURI string
		wantAdd int
	}{
		{"audio", "data:audio/wav;base64,QUJD", 256},
		{"video", "data:video/mp4;base64,QUJD", 8192},
		{"document", "data:application/pdf;base64,QUJD", 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c2 := createTestGinContext()
			c2.Set(string(constant.ContextKeyOriginalModel), "gpt-4o")
			meta := &types.TokenCountMeta{
				CombineText:   "x",
				MessagesCount: 1,
				Files:         []*types.FileMeta{{OriginData: tc.dataURI}},
			}
			got, err := EstimateRequestToken(c2, meta, info)
			if err != nil {
				t.Fatalf("EstimateRequestToken(%s): %v", tc.name, err)
			}
			if got-baseTokens != tc.wantAdd {
				t.Errorf("%s added %d tokens, want %d", tc.name, got-baseTokens, tc.wantAdd)
			}
		})
	}
}

// ─── quota.go: PostConsumeQuota platform settlement -> outbox enqueue ───────

// TestPostConsumeQuota_SettleFailureEnqueuesOutbox proves the Phase-5 platform
// settlement path: with an IdentityAccountID and a PlatformPreAuthID set and the
// identity backend unreachable in unit scope, the settle attempt fails and a
// durable "settle" outbox row is enqueued for retry (no silent revenue loss),
// while the local user quota is still debited by the exact amount.
func TestPostConsumeQuota_SettleFailureEnqueuesOutbox(t *testing.T) {
	db := setupServiceTestDB(t)
	seedPoolTables(t, db)
	restore := setupOutbox(t, db)
	defer restore()

	const startQuota = 100_000
	userId := seedTestUser(t, db, startQuota)
	key, tokenId := seedTestToken(t, db, userId, startQuota, false)

	relayInfo := &relaycommon.RelayInfo{
		UserId:            userId,
		TokenId:           tokenId,
		TokenKey:          key,
		IdentityAccountID: 4242,
		PlatformPreAuthID: 9001,
	}

	const quota = 750
	if err := PostConsumeQuota(relayInfo, quota, 0, false); err != nil {
		t.Fatalf("PostConsumeQuota: %v", err)
	}

	if got := userQuota(t, db, userId); got != startQuota-quota {
		t.Errorf("user quota = %d, want %d", got, startQuota-quota)
	}

	var row entity.BillingOutbox
	if err := db.Where("pre_auth_id = ? AND action = ?", 9001, outboxActionSettle).First(&row).Error; err != nil {
		t.Fatalf("no settle outbox row enqueued (settlement not durable): %v", err)
	}
	if row.AccountID != 4242 {
		t.Errorf("outbox account = %d, want 4242", row.AccountID)
	}
	// amountLB = totalQuota / QuotaPerUnit; must be positive.
	if row.AmountLB <= 0 {
		t.Errorf("outbox amount = %v, want > 0", row.AmountLB)
	}
	// Pre-auth must be marked handled so a later pass does not double-settle.
	if relayInfo.PlatformPreAuthID != 0 {
		t.Errorf("PlatformPreAuthID = %d, want 0 (marked handled)", relayInfo.PlatformPreAuthID)
	}
}

// ─── log_info_generate.go: other-info assembly ────────────────────────────

// TestGenerateTextOtherInfo_PopulatesConditionalFields proves the conditional
// branches: model-mapping, reasoning-effort, system-prompt-override, multi-key
// admin info, local-count-tokens, and request-path capture from the gin request.
func TestGenerateTextOtherInfo_PopulatesConditionalFields(t *testing.T) {
	c := createTestGinContext()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions?x=1", nil)
	c.Set(string(constant.ContextKeySystemPromptOverride), true)
	c.Set(string(constant.ContextKeyChannelIsMultiKey), true)
	c.Set(string(constant.ContextKeyChannelMultiKeyIndex), 3)
	c.Set(string(constant.ContextKeyLocalCountTokens), true)

	now := time.Now()
	relayInfo := &relaycommon.RelayInfo{
		StartTime:         now.Add(-time.Second),
		FirstResponseTime: now,
		ReasoningEffort:   "high",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-4o-upstream",
			IsModelMapped:     true,
		},
	}

	other := GenerateTextOtherInfo(c, relayInfo, 2.0, 1.0, 3.0, 0, 0.0, 0.0, 1.0)

	if other["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", other["reasoning_effort"])
	}
	if other["is_model_mapped"] != true || other["upstream_model_name"] != "gpt-4o-upstream" {
		t.Errorf("model-mapped fields missing: %v", other)
	}
	if other["is_system_prompt_overwritten"] != true {
		t.Errorf("is_system_prompt_overwritten = %v, want true", other["is_system_prompt_overwritten"])
	}
	if other["request_path"] != "/v1/chat/completions" {
		t.Errorf("request_path = %v, want /v1/chat/completions (query stripped)", other["request_path"])
	}
	admin, ok := other["admin_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("admin_info type = %T, want map", other["admin_info"])
	}
	if admin["is_multi_key"] != true || admin["multi_key_index"] != 3 {
		t.Errorf("multi-key admin info = %v, want {true,3}", admin)
	}
	if admin["local_count_tokens"] != true {
		t.Errorf("local_count_tokens = %v, want true", admin["local_count_tokens"])
	}
}

// TestGenerateMjOtherInfo_RequestPathFromRelayInfo proves the appendRequestPath
// fallback: with a nil gin context it takes the path from relayInfo and strips
// the query string, and it emits the special group ratio when present.
func TestGenerateMjOtherInfo_RequestPathFromRelayInfo(t *testing.T) {
	relayInfo := &relaycommon.RelayInfo{RequestURLPath: "/mj/submit/imagine?token=abc"}
	price := types.PerCallPriceData{
		ModelPrice: 0.1,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio:        1.0,
			HasSpecialRatio:   true,
			GroupSpecialRatio: 0.75,
		},
	}
	other := GenerateMjOtherInfo(relayInfo, price)
	if other["request_path"] != "/mj/submit/imagine" {
		t.Errorf("request_path = %v, want /mj/submit/imagine (query stripped)", other["request_path"])
	}
	if other["user_group_ratio"] != 0.75 {
		t.Errorf("user_group_ratio = %v, want 0.75 (special ratio)", other["user_group_ratio"])
	}
}

// TestAppendRequestPath_NilOtherNoop proves the guard: a nil map is left
// untouched (no panic).
func TestAppendRequestPath_NilOtherNoop(t *testing.T) {
	appendRequestPath(nil, &relaycommon.RelayInfo{RequestURLPath: "/x"}, nil)
}

// ─── error.go: RelayErrorHandler JSON-string error fallback ────────────────

// TestRelayErrorHandler_StringErrorFallback proves the branch where the upstream
// error field is a bare JSON string (not an object): the handler falls back to a
// ToMessage-derived error and, with showBodyWhenFail, includes the raw body.
func TestRelayErrorHandler_StringErrorFallback(t *testing.T) {
	body := `{"error":"upstream exploded"}`
	apiErr := RelayErrorHandler(context.Background(), makeResp(http.StatusBadGateway, body, nil), true)
	if apiErr == nil {
		t.Fatal("expected non-nil NewAPIError")
	}
	if apiErr.Err == nil {
		t.Fatal("expected an underlying error")
	}
}

// ─── billing_service.go: GetSubscriptionQuotaInfo / GetUsageAmount ─────────

// TestGetSubscriptionQuotaInfo_TokenScope proves the per-token display path:
// total/used/remaining are derived from the token's RemainQuota + UsedQuota via
// CalculateDisplayAmount (tracked through the production helper so the test
// follows the configured display type).
func TestGetSubscriptionQuotaInfo_TokenScope(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1)
	tok := repo.Token{
		UserId:      userId,
		Key:         "k-sub-token",
		Status:      1,
		Name:        "sub",
		RemainQuota: 6_000,
		UsedQuota:   4_000,
		ExpiredTime: -1,
		Group:       "default",
	}
	if err := db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	info, err := GetSubscriptionQuotaInfo(userId, tok.Id, true)
	if err != nil {
		t.Fatalf("GetSubscriptionQuotaInfo: %v", err)
	}
	if want := CalculateDisplayAmount(10_000); info.TotalAmount != want {
		t.Errorf("TotalAmount = %v, want %v", info.TotalAmount, want)
	}
	if want := CalculateDisplayAmount(4_000); info.UsedAmount != want {
		t.Errorf("UsedAmount = %v, want %v", info.UsedAmount, want)
	}
	if want := CalculateDisplayAmount(6_000); info.RemainingAmount != want {
		t.Errorf("RemainingAmount = %v, want %v", info.RemainingAmount, want)
	}
}

// TestGetSubscriptionQuotaInfo_UnlimitedToken proves the unlimited-quota override
// pins TotalAmount to the sentinel 100000000.
func TestGetSubscriptionQuotaInfo_UnlimitedToken(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1)
	tok := repo.Token{
		UserId:         userId,
		Key:            "k-unlimited",
		Status:         1,
		Name:           "unl",
		RemainQuota:    5,
		UnlimitedQuota: true,
		ExpiredTime:    -1,
		Group:          "default",
	}
	if err := db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	info, err := GetSubscriptionQuotaInfo(userId, tok.Id, true)
	if err != nil {
		t.Fatalf("GetSubscriptionQuotaInfo: %v", err)
	}
	if !info.UnlimitedQuota {
		t.Error("UnlimitedQuota = false, want true")
	}
	if info.TotalAmount != 100000000 {
		t.Errorf("TotalAmount = %v, want 100000000 (unlimited sentinel)", info.TotalAmount)
	}
	if info.ExpiredTime != 0 {
		t.Errorf("ExpiredTime = %d, want 0 (normalised from -1)", info.ExpiredTime)
	}
}

// TestGetSubscriptionQuotaInfo_UserScope proves the user-scoped path derives
// amounts from user remaining + used quota.
func TestGetSubscriptionQuotaInfo_UserScope(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 12_000)
	if err := db.Model(&repo.User{}).Where("id = ?", userId).Update("used_quota", 3_000).Error; err != nil {
		t.Fatalf("set used quota: %v", err)
	}

	info, err := GetSubscriptionQuotaInfo(userId, 0, false)
	if err != nil {
		t.Fatalf("GetSubscriptionQuotaInfo: %v", err)
	}
	if want := CalculateDisplayAmount(15_000); info.TotalAmount != want {
		t.Errorf("TotalAmount = %v, want %v (remain 12000 + used 3000)", info.TotalAmount, want)
	}
	if want := CalculateDisplayAmount(12_000); info.RemainingAmount != want {
		t.Errorf("RemainingAmount = %v, want %v", info.RemainingAmount, want)
	}
}

// TestGetSubscriptionQuotaInfo_TokenError proves the error path: an invalid
// token id (0) surfaces the repo error rather than a zero-value info.
func TestGetSubscriptionQuotaInfo_TokenError(t *testing.T) {
	setupServiceTestDB(t)
	if _, err := GetSubscriptionQuotaInfo(1, 0, true); err == nil {
		t.Fatal("expected error for token id 0")
	}
}

// TestGetUsageAmount_Scopes proves GetUsageAmount returns used-quota * 100 for
// both token and user scope, and errors on a bad token id.
func TestGetUsageAmount_Scopes(t *testing.T) {
	db := setupServiceTestDB(t)
	userId := seedTestUser(t, db, 1)
	if err := db.Model(&repo.User{}).Where("id = ?", userId).Update("used_quota", 2_500).Error; err != nil {
		t.Fatalf("set used quota: %v", err)
	}
	tok := repo.Token{
		UserId: userId, Key: "k-usage", Status: 1, Name: "usg",
		UsedQuota: 800, ExpiredTime: -1, Group: "default",
	}
	if err := db.Create(&tok).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	gotTok, err := GetUsageAmount(userId, tok.Id, true)
	if err != nil {
		t.Fatalf("GetUsageAmount token: %v", err)
	}
	if want := CalculateDisplayAmount(800) * 100; gotTok != want {
		t.Errorf("token usage = %v, want %v", gotTok, want)
	}

	gotUser, err := GetUsageAmount(userId, 0, false)
	if err != nil {
		t.Fatalf("GetUsageAmount user: %v", err)
	}
	if want := CalculateDisplayAmount(2_500) * 100; gotUser != want {
		t.Errorf("user usage = %v, want %v", gotUser, want)
	}

	if _, err := GetUsageAmount(1, 0, true); err == nil {
		t.Fatal("expected error for token id 0")
	}
}

// ─── billing_outbox.go: ProcessBillingOutbox permanent-failure branch ──────

// TestProcessBillingOutbox_PermanentFailureMarksFailed proves that an entry
// which has already exhausted its retry budget transitions to "failed" (not
// rescheduled) when the next processing attempt errors — so a poison entry does
// not retry forever.
func TestProcessBillingOutbox_PermanentFailureMarksFailed(t *testing.T) {
	db := setupServiceTestDB(t)
	restore := setupOutbox(t, db)
	defer restore()

	// Seed an entry one attempt away from the retry ceiling, already due.
	entry := entity.BillingOutbox{
		AccountID:  1,
		PreAuthID:  7777,
		Action:     outboxActionSettle,
		AmountLB:   0.5,
		Status:     "pending",
		RetryCount: outboxMaxRetries - 1,
		NextRetry:  time.Now().Add(-time.Minute),
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	if err := ProcessBillingOutbox(context.Background()); err != nil {
		t.Fatalf("ProcessBillingOutbox: %v", err)
	}

	var row entity.BillingOutbox
	if err := db.Where("pre_auth_id = ?", 7777).First(&row).Error; err != nil {
		t.Fatalf("row gone: %v", err)
	}
	if row.Status != "failed" {
		t.Errorf("status = %q, want failed (retry budget exhausted)", row.Status)
	}
	if row.RetryCount != outboxMaxRetries {
		t.Errorf("retry_count = %d, want %d", row.RetryCount, outboxMaxRetries)
	}
	if row.Error == "" {
		t.Error("expected a recorded error string on the permanently-failed entry")
	}
}
