package siliconflow

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func rerankContractRun(t *testing.T, returnDocs bool, body string) (*types.NewAPIError, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	info := &relaycommon.RelayInfo{RerankerInfo: &relaycommon.RerankerInfo{ReturnDocuments: returnDocs}}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	_, e := siliconflowRerankHandler(c, info, resp)
	return e, w.Body.String()
}

func TestRerankContract(t *testing.T) {
	cases := []struct {
		name       string
		returnDocs bool
		body       string
		wantCode   types.ErrorCode
		wantDoc    bool
	}{
		{"normal returns documents", true, `{"results":[{"index":0,"relevance_score":0.9,"document":{"text":"a"}},{"index":1,"relevance_score":0.2,"document":{"text":"b"}}],"meta":{"tokens":{"input_tokens":5}}}`, "", true},
		{"return_documents false strips documents", false, `{"results":[{"index":0,"relevance_score":0.9,"document":{"text":"a"}},{"index":1,"relevance_score":0.2,"document":{"text":"b"}}],"meta":{"tokens":{"input_tokens":5}}}`, "", false},
		{"misordered", true, `{"results":[{"index":0,"relevance_score":0.2},{"index":1,"relevance_score":0.9}],"meta":{"tokens":{"input_tokens":5}}}`, types.ErrorCodeInvalidProviderResponse, false},
		{"contradictory usage", true, `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"tokens":{"input_tokens":5,"output_tokens":-5}}}`, types.ErrorCodeInvalidProviderUsage, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr, written := rerankContractRun(t, tc.returnDocs, tc.body)
			if tc.wantCode != "" {
				if apiErr == nil || apiErr.GetErrorCode() != tc.wantCode || apiErr.StatusCode != http.StatusBadGateway {
					t.Fatalf("want 502 %s, got %v", tc.wantCode, apiErr)
				}
				if written != "" {
					t.Errorf("response body must not be written on rejection, got %q", written)
				}
				return
			}
			if apiErr != nil {
				t.Fatalf("unexpected error: %v", apiErr)
			}
			if !strings.Contains(written, "relevance_score") {
				t.Fatalf("normal response must be written, got %q", written)
			}
			if got := strings.Contains(written, "\"document\""); got != tc.wantDoc {
				t.Errorf("document present = %v, want %v (body %s)", got, tc.wantDoc, written)
			}
		})
	}
}
