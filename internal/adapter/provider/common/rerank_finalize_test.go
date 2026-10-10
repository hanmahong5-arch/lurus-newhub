package common

import (
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestValidateRerankResponse(t *testing.T) {
	withDocs := &RelayInfo{RerankerInfo: &RerankerInfo{ReturnDocuments: true}}
	noDocs := &RelayInfo{RerankerInfo: &RerankerInfo{ReturnDocuments: false}}
	mk := func(u dto.Usage, scores ...float64) *dto.RerankResponse {
		r := &dto.RerankResponse{Usage: u}
		for i, s := range scores {
			r.Results = append(r.Results, dto.RerankResponseResult{Index: i, RelevanceScore: s, Document: "doc"})
		}
		return r
	}
	ok := dto.Usage{PromptTokens: 5, TotalTokens: 5}
	cases := []struct {
		name     string
		info     *RelayInfo
		resp     *dto.RerankResponse
		wantCode types.ErrorCode
		wantDoc  bool
		skipRetr bool
	}{
		{"normal keeps docs when asked", withDocs, mk(ok, 0.9, 0.1), "", true, false},
		{"docs stripped when not asked", noDocs, mk(ok, 0.9, 0.1), "", false, false},
		{"docs stripped when info nil", nil, mk(ok, 0.9, 0.1), "", false, false},
		{"contradictory usage", withDocs, mk(dto.Usage{PromptTokens: 7}, 0.9), types.ErrorCodeInvalidProviderUsage, true, false},
		{"negative usage", withDocs, mk(dto.Usage{TotalTokens: -1}, 0.9), types.ErrorCodeInvalidProviderUsage, true, false},
		{"misordered", withDocs, mk(ok, 0.1, 0.9), types.ErrorCodeInvalidProviderResponse, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRerankResponse(tc.info, tc.resp)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got := tc.resp.Results[0].Document != nil; got != tc.wantDoc {
					t.Errorf("document present = %v, want %v", got, tc.wantDoc)
				}
				return
			}
			if err == nil {
				t.Fatal("want rejection")
			}
			if err.GetErrorCode() != tc.wantCode || err.StatusCode != http.StatusBadGateway {
				t.Errorf("code/status = %s/%d", err.GetErrorCode(), err.StatusCode)
			}
			if tc.skipRetr && !types.IsSkipRetryError(err) {
				t.Error("misordered response must skip retry")
			}
		})
	}
}
