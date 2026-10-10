package common_handler

// Cycle 22 L2: response-side rerank contract (usage sanity, ordering,
// return_documents) and the usage-source stamp consumed by settlement.

import (
	"encoding/json"
	"net/http"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func jinaInfo(returnDocs bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RerankerInfo: &relaycommon.RerankerInfo{ReturnDocuments: returnDocs},
	}
}

func TestRerankHandler_ContradictoryUsage_Is502AndNotSettled(t *testing.T) {
	for name, usage := range map[string]string{
		"total zero, prompt positive": `{"total_tokens":0,"prompt_tokens":7}`,
		"negative total":              `{"total_tokens":-3}`,
		"negative prompt":             `{"total_tokens":5,"prompt_tokens":-1}`,
		"negative completion":         `{"total_tokens":5,"completion_tokens":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, rec := routerRelayNewGinCtx()
			body := `{"results":[{"index":0,"relevance_score":0.9}],"usage":` + usage + `}`
			upstream := routerRelayUpstreamResp(body)
			defer func() { _ = upstream.Body.Close() }()
			u, err := RerankHandler(c, jinaInfo(false), upstream)
			if err == nil || u != nil {
				t.Fatalf("want rejection, got usage=%v err=%v", u, err)
			}
			if err.GetErrorCode() != types.ErrorCodeInvalidProviderUsage || err.StatusCode != http.StatusBadGateway {
				t.Errorf("code/status = %s/%d, want invalid_provider_usage/502", err.GetErrorCode(), err.StatusCode)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("nothing may be written to the caller on rejection, got %q", rec.Body.String())
			}
		})
	}
}

func TestRerankHandler_AbsentUsage_IsEstimated(t *testing.T) {
	c, _ := routerRelayNewGinCtx()
	info := jinaInfo(false)
	info.SetEstimatePromptTokens(21)
	upstream := routerRelayUpstreamResp(`{"results":[{"index":0,"relevance_score":0.5}]}`)
	defer func() { _ = upstream.Body.Close() }()
	u, err := RerankHandler(c, info, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if info.UsageSource != "estimated" || u.TotalTokens != 21 {
		t.Errorf("source=%q total=%d, want estimated/21", info.UsageSource, u.TotalTokens)
	}
}

func TestRerankHandler_TotalOnlyUsageKeptAsSent(t *testing.T) {
	c, _ := routerRelayNewGinCtx()
	info := jinaInfo(false)
	upstream := routerRelayUpstreamResp(`{"results":[],"usage":{"total_tokens":9}}`)
	defer func() { _ = upstream.Body.Close() }()
	u, err := RerankHandler(c, info, upstream)
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 0 || u.TotalTokens != 9 || info.UsageSource != "upstream" {
		t.Errorf("prompt=%d total=%d source=%q, want 0/9/upstream", u.PromptTokens, u.TotalTokens, info.UsageSource)
	}
}

func TestRerankHandler_OrderValidation(t *testing.T) {
	cases := []struct {
		name   string
		scores string
		ok     bool
	}{
		{"descending", `[0.9,0.5,0.1]`, true},
		{"equal scores are fine", `[0.7,0.7,0.7]`, true},
		{"single", `[0.3]`, true},
		{"ascending", `[0.1,0.5,0.9]`, false},
		{"one out of place", `[0.9,0.2,0.5]`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var scores []float64
			_ = json.Unmarshal([]byte(tc.scores), &scores)
			results := make([]map[string]any, len(scores))
			for i, s := range scores {
				results[i] = map[string]any{"index": i, "relevance_score": s}
			}
			raw, _ := json.Marshal(map[string]any{"results": results, "usage": map[string]int{"total_tokens": 4}})
			c, _ := routerRelayNewGinCtx()
			upstream := routerRelayUpstreamResp(string(raw))
			defer func() { _ = upstream.Body.Close() }()
			_, err := RerankHandler(c, jinaInfo(false), upstream)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("unsorted results must be rejected")
				}
				if err.GetErrorCode() != types.ErrorCodeInvalidProviderResponse || err.StatusCode != http.StatusBadGateway {
					t.Errorf("code/status = %s/%d, want invalid_provider_response/502", err.GetErrorCode(), err.StatusCode)
				}
			}
		})
	}
}

func TestRerankHandler_ReturnDocumentsFalse_StripsDocument(t *testing.T) {
	body := `{"results":[{"index":0,"relevance_score":0.9,"document":{"text":"secret-a"}}],"usage":{"total_tokens":3}}`

	c, rec := routerRelayNewGinCtx()
	upstream := routerRelayUpstreamResp(body)
	defer func() { _ = upstream.Body.Close() }()
	if _, err := RerankHandler(c, jinaInfo(false), upstream); err != nil {
		t.Fatal(err)
	}
	var out map[string][]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if _, has := out["results"][0]["document"]; has {
		t.Errorf("return_documents=false must not echo documents: %s", rec.Body.String())
	}

	c2, rec2 := routerRelayNewGinCtx()
	upstream2 := routerRelayUpstreamResp(body)
	defer func() { _ = upstream2.Body.Close() }()
	if _, err := RerankHandler(c2, jinaInfo(true), upstream2); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &out)
	if _, has := out["results"][0]["document"]; !has {
		t.Errorf("return_documents=true must keep documents: %s", rec2.Body.String())
	}
}

func TestUsageContradictory(t *testing.T) {
	for u, want := range map[dto.Usage]bool{
		{}:                                     false,
		{TotalTokens: 5}:                       false,
		{PromptTokens: 3, TotalTokens: 3}:      false,
		{PromptTokens: 3}:                      true,
		{TotalTokens: -1}:                      true,
		{CompletionTokens: -1, TotalTokens: 2}: true,
	} {
		if got := UsageContradictory(u); got != want {
			t.Errorf("UsageContradictory(%+v) = %v, want %v", u, got, want)
		}
	}
}
