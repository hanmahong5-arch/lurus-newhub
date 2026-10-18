package openai

// usage.cost is the OpenRouter-standard field tools such as Cline, Roo and
// OpenClaw display as the call's cost. It must state what the caller is
// charged, never what an OpenRouter upstream charged us.

import (
	"encoding/json"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// 7 prompt + 3 completion tokens at ratio 1 = 10 quota.
func chargedUSDFor10Quota() float64 { return 10 / common.QuotaPerUnit }

func openRouterInfo() *relaycommon.RelayInfo {
	info := perceptiveRelayInfo()
	info.ChannelType = constant.ChannelTypeOpenRouter
	return info
}

func usageOf(t *testing.T, frame string) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(frame), &body); err != nil {
		t.Fatalf("decode %q: %v", frame, err)
	}
	u, _ := body["usage"].(map[string]interface{})
	if u == nil {
		t.Fatalf("no usage object in %q", frame)
	}
	return u
}

// Mutation: drop the ApplyChargedCost call in OpenaiHandler → the client
// sees 0.5 (OpenRouter's price) and cost_details.
func TestOpenaiHandler_UsageCostIsTheChargeNotTheUpstreamCost(t *testing.T) {
	w := newRecorderCtx(t)
	body := `{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"cost":0.5,"is_byok":false,"cost_details":{"upstream_inference_cost":0.5}}}`
	resp := fakeHTTPResponse(200, body)
	defer func() { _ = resp.Body.Close() }()

	usage, apiErr := OpenaiHandler(w.ctx, openRouterInfo(), resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}

	u := usageOf(t, w.rec.Body.String())
	if got, _ := u["cost"].(float64); got != chargedUSDFor10Quota() {
		t.Errorf("client usage.cost = %v, want the charge %v", u["cost"], chargedUSDFor10Quota())
	}
	for _, k := range []string{"cost_details", "is_byok"} {
		if _, ok := u[k]; ok {
			t.Errorf("client usage still carries upstream %s", k)
		}
	}
	// Billing still sees the upstream cost (margin reporting reads it).
	if got, _ := usage.Cost.(float64); got != 0.5 {
		t.Errorf("returned usage.Cost = %v, want the upstream 0.5 kept for billing", usage.Cost)
	}
}

func TestOaiStreamHandler_InlineUsageCostIsTheCharge(t *testing.T) {
	w := newRecorderCtx(t)
	info := openRouterInfo()
	info.ShouldIncludeUsage = true
	body := `data: {"id":"c1","model":"m","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"c1","model":"m","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"cost":0.5,"cost_details":{"upstream_inference_cost":0.5}}}` + "\n\n" +
		"data: [DONE]\n\n"
	resp := sseResponse(body)
	defer func() { _ = resp.Body.Close() }()

	if _, apiErr := OaiStreamHandler(w.ctx, info, resp); apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr.Error())
	}
	var final string
	for _, line := range strings.Split(w.rec.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: {") && strings.Contains(line, `"usage"`) {
			final = strings.TrimPrefix(line, "data: ")
		}
	}
	u := usageOf(t, final)
	if got, _ := u["cost"].(float64); got != chargedUSDFor10Quota() {
		t.Errorf("final frame usage.cost = %v, want the charge %v", u["cost"], chargedUSDFor10Quota())
	}
	if _, ok := u["cost_details"]; ok {
		t.Errorf("final frame still carries upstream cost_details")
	}
}

// An unbilled frame (no extension) states no cost at all, and the upstream's
// is removed.
func TestSendFinalStreamData_NilExtDropsUpstreamCost(t *testing.T) {
	w := newRecorderCtx(t)
	data := `{"id":"c1","choices":[],"usage":{"total_tokens":10,"cost":0.5}}`
	if err := sendFinalStreamData(w.ctx, &relaycommon.RelayInfo{}, data, false, false, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u := usageOf(t, strings.TrimSpace(strings.TrimPrefix(w.rec.Body.String(), "data: ")))
	if _, ok := u["cost"]; ok {
		t.Errorf("unbilled frame states a cost: %v", u["cost"])
	}
}
