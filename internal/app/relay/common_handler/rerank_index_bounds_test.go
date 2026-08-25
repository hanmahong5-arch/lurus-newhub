package common_handler

// Regression tests for the document backfill on the Xinference rerank path:
// the index used to address the caller's own request documents is supplied by
// the upstream response, so an out-of-range value must be rejected as a bad
// upstream body instead of panicking the handler (a panic turns into a 500 and
// the pre-consumed quota is never refunded).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func rerankBoundsCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	return c, rec
}

func rerankBoundsResp(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{},
	}
}

func rerankBoundsInfo(docs ...any) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXinference},
		RerankerInfo: &relaycommon.RerankerInfo{
			ReturnDocuments: true,
			Documents:       docs,
		},
	}
}

func TestRerankHandler_XinferencePath_IndexEqualToDocumentCount_RejectedNotPanic(t *testing.T) {
	// index 2 with only 2 documents (valid indices are 0 and 1).
	upstreamBody := `{"results": [{"index": 2, "relevance_score": 0.9, "document": ""}]}`

	c, rec := rerankBoundsCtx()
	info := rerankBoundsInfo("original-doc-0", "original-doc-1")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked on out-of-range upstream index instead of returning an error: %v", r)
		}
	}()

	usage, apiErr := RerankHandler(c, info, rerankBoundsResp(upstreamBody))
	if apiErr == nil {
		t.Fatal("expected a bad-response error for an upstream index past the end of the request documents, got nil")
	}
	if usage != nil {
		t.Fatalf("expected nil usage when the upstream response is rejected, got %+v", usage)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Fatalf("expected error code %q (bad upstream body, not a client error), got %q",
			types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 status, got %d", apiErr.StatusCode)
	}
	// The message must name the offending index and the document count so the
	// misbehaving upstream is identifiable from the log alone.
	msg := apiErr.Error()
	if !strings.Contains(msg, "2") {
		t.Fatalf("expected error message to name the offending index and document count, got %q", msg)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("must not write a success body when the upstream response is rejected, got %q", rec.Body.String())
	}
}

func TestRerankHandler_XinferencePath_NegativeIndex_RejectedNotPanic(t *testing.T) {
	upstreamBody := `{"results": [{"index": -1, "relevance_score": 0.9, "document": ""}]}`

	c, rec := rerankBoundsCtx()
	info := rerankBoundsInfo("original-doc-0", "original-doc-1")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked on negative upstream index instead of returning an error: %v", r)
		}
	}()

	usage, apiErr := RerankHandler(c, info, rerankBoundsResp(upstreamBody))
	if apiErr == nil {
		t.Fatal("expected a bad-response error for a negative upstream index, got nil")
	}
	if usage != nil {
		t.Fatalf("expected nil usage when the upstream response is rejected, got %+v", usage)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Fatalf("expected error code %q, got %q", types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
	}
	if !strings.Contains(apiErr.Error(), "-1") {
		t.Fatalf("expected error message to name the offending index -1, got %q", apiErr.Error())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("must not write a success body when the upstream response is rejected, got %q", rec.Body.String())
	}
}

// Empty request documents is the degenerate case of the same bug: every index
// is out of range, including 0.
func TestRerankHandler_XinferencePath_NoRequestDocuments_RejectedNotPanic(t *testing.T) {
	upstreamBody := `{"results": [{"index": 0, "relevance_score": 0.9, "document": ""}]}`

	c, _ := rerankBoundsCtx()
	info := rerankBoundsInfo()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handler panicked when the request carried no documents: %v", r)
		}
	}()

	usage, apiErr := RerankHandler(c, info, rerankBoundsResp(upstreamBody))
	if apiErr == nil {
		t.Fatal("expected a bad-response error when there is no document to backfill from, got nil")
	}
	if usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
}

// Happy path guard: valid indices must still backfill, including an
// out-of-order index that legitimately addresses an earlier document.
func TestRerankHandler_XinferencePath_ValidIndicesStillBackfill(t *testing.T) {
	upstreamBody := `{
		"results": [
			{"index": 1, "relevance_score": 0.9, "document": ""},
			{"index": 0, "relevance_score": 0.4, "document": ""}
		]
	}`

	c, rec := rerankBoundsCtx()
	info := rerankBoundsInfo("original-doc-0", "original-doc-1")
	info.SetEstimatePromptTokens(11)

	usage, apiErr := RerankHandler(c, info, rerankBoundsResp(upstreamBody))
	if apiErr != nil {
		t.Fatalf("unexpected error for in-range indices: %v", apiErr)
	}
	if usage == nil || usage.PromptTokens != 11 {
		t.Fatalf("expected usage estimated from the request (11), got %+v", usage)
	}

	var out dto.RerankResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body is not valid RerankResponse JSON: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out.Results))
	}
	if out.Results[0].Document != "original-doc-1" {
		t.Fatalf("expected index 1 backfilled from info.Documents[1], got %q", out.Results[0].Document)
	}
	if out.Results[1].Document != "original-doc-0" {
		t.Fatalf("expected index 0 backfilled from info.Documents[0], got %q", out.Results[1].Document)
	}
}
