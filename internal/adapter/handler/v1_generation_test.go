package handler

// v1_generation_test.go — oracle for GET /v1/generation (L2-REQUEST-IDENTITY
// / C05-ROW-IDENTITY-LOOKUP), driven on relay_success_fixture_test.go's real
// TokenAuth->Distribute->Relay harness so the row it looks up is a genuine
// settlement, not a hand-built struct standing in for one.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

type generationResponse struct {
	Success bool           `json:"success"`
	Data    generationView `json:"data"`
}

// TestGetGeneration_ResolvesOwnRelayByRequestId drives a real relay with
// X-Session-Id/user set, then looks the row up by the X-Request-Id the
// relay response itself carried.
func TestGetGeneration_ResolvesOwnRelayByRequestId(t *testing.T) {
	ctx := setupRelaySuccessRouter(t, openAIChatEchoUpstream)

	relayResp := ctx.postChat(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"user":"alice"}`,
		map[string]string{"X-Session-Id": "conv-42"})
	if relayResp.Code != http.StatusOK {
		t.Fatalf("relay status = %d, body=%s", relayResp.Code, relayResp.Body.String())
	}
	reqId := relayResp.Header().Get(common.RequestIdHeader)
	if reqId == "" {
		t.Fatal("relay response carried no X-Request-Id to look up")
	}

	// Confirm the log row's Quota independently before asserting total_cost
	// against it (never trust a mirrored computation without a real number
	// behind it).
	var row repo.Log
	if err := ctx.db.Where("type = ?", repo.LogTypeConsume).First(&row).Error; err != nil {
		t.Fatalf("query consume log: %v", err)
	}
	if row.Quota <= 0 {
		t.Fatalf("seeded row Quota = %d, want > 0", row.Quota)
	}

	genReq := ctx.newAuthedRequest(t, http.MethodGet, "/v1/generation?id="+reqId, "")
	genRR := ctx.serve(genReq)
	if genRR.Code != http.StatusOK {
		t.Fatalf("GET /v1/generation status = %d, body=%s", genRR.Code, genRR.Body.String())
	}

	var resp generationResponse
	if err := json.Unmarshal(genRR.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, genRR.Body.String())
	}
	if !resp.Success {
		t.Fatalf("success = false, body=%s", genRR.Body.String())
	}

	wantCost := float64(row.Quota) / common.QuotaPerUnit
	if resp.Data.TotalCost != wantCost {
		t.Errorf("total_cost = %v, want %v (row.Quota=%d / QuotaPerUnit)", resp.Data.TotalCost, wantCost, row.Quota)
	}
	if resp.Data.ProviderName != "OpenAI" && resp.Data.ProviderName != "openai" {
		t.Errorf("provider_name = %q, want the fake channel's type name (OpenAI)", resp.Data.ProviderName)
	}
	if resp.Data.SessionId != "conv-42" {
		t.Errorf("session_id = %q, want conv-42", resp.Data.SessionId)
	}

	body := genRR.Body.String()
	if strings.Contains(body, "admin_info") {
		t.Errorf("body = %s, must not leak admin_info (TierInternal)", body)
	}
	if strings.Contains(body, `"channel_id"`) {
		t.Errorf("body = %s, must not leak channel_id", body)
	}
}

// TestGetGeneration_ForeignTokenReturns404: another caller's token must not
// resolve someone else's request id.
func TestGetGeneration_ForeignTokenReturns404(t *testing.T) {
	ctx := setupRelaySuccessRouter(t, openAIChatEchoUpstream)

	relayResp := ctx.postChat(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`, nil)
	if relayResp.Code != http.StatusOK {
		t.Fatalf("relay status = %d, body=%s", relayResp.Code, relayResp.Body.String())
	}
	reqId := relayResp.Header().Get(common.RequestIdHeader)

	intruderKey, err := common.GenerateRandomKey(48)
	if err != nil {
		t.Fatalf("generate intruder key: %v", err)
	}
	intruderUser := &repo.User{
		Username: "generation-intruder", DisplayName: "Intruder", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Email: "genintruder@test.local", Group: "default", TenantId: "default",
	}
	if err := ctx.db.Create(intruderUser).Error; err != nil {
		t.Fatalf("seed intruder user: %v", err)
	}
	intruderToken := &repo.Token{
		UserId: intruderUser.Id, TenantId: "default", Key: intruderKey, Name: "intruder-token",
		Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: -1,
	}
	if err := ctx.db.Create(intruderToken).Error; err != nil {
		t.Fatalf("seed intruder token: %v", err)
	}

	req := ctx.newRequestWithKey(t, http.MethodGet, "/v1/generation?id="+reqId, "", intruderToken.Key)
	rr := ctx.serve(req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a foreign token's request id, body=%s", rr.Code, rr.Body.String())
	}
}

// TestGetGeneration_OversizedIdReturns404 pins the id-format guard: a value
// that could never be a real request id must 404, not 500 on a query error.
func TestGetGeneration_OversizedIdReturns404(t *testing.T) {
	ctx := setupRelaySuccessRouter(t, openAIChatEchoUpstream)

	req := ctx.newAuthedRequest(t, http.MethodGet, "/v1/generation?id="+strings.Repeat("a", 201), "")
	rr := ctx.serve(req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an oversized id, body=%s", rr.Code, rr.Body.String())
	}
}
