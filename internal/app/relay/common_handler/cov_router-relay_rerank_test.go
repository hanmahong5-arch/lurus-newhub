package common_handler

// Business-acceptance tests for RerankHandler: the function that turns an
// upstream rerank response (either a Xinference-flavored payload or a
// Jina-compatible payload) into the caller-facing dto.RerankResponse and
// the *dto.Usage returned to the billing/settlement layer.
//
// Self-check performed while writing these tests: stubbing RerankHandler's
// body to `return nil, nil` (skipping decode/Usage population) makes every
// assertion below fail, because they inspect fields decoded from the fake
// upstream body (Usage.PromptTokens/TotalTokens, Results[i].Document,
// Results[i].RelevanceScore, HTTP status/body written to the recorder) —
// none of them merely assert err==nil or reflect back a constant the test
// itself constructed independent of the handler's decode logic.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func routerRelayNewGinCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	return c, rec
}

func routerRelayUpstreamResp(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{},
	}
}

// Jina-compatible upstream (default / non-Xinference channel path): the
// handler must copy the response's own TotalTokens into PromptTokens (the
// business quirk at line 69 of rerank.go — Jina responses don't split
// prompt vs total, so the settlement layer bills using TotalTokens for
// both fields) and pass the results through untouched.
func TestRerankHandler_JinaPath_UsageAndResultsPassThrough(t *testing.T) {
	upstreamBody := `{
		"results": [
			{"index": 0, "relevance_score": 0.91, "document": "doc-a"},
			{"index": 1, "relevance_score": 0.42, "document": "doc-b"}
		],
		"usage": {"total_tokens": 57}
	}`

	c, rec := routerRelayNewGinCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RerankerInfo: &relaycommon.RerankerInfo{ReturnDocuments: true},
	}

	bcResp5 := routerRelayUpstreamResp(upstreamBody)
	defer func() {
		if bcResp5 != nil {
			_ = bcResp5.Body.Close()
		}
	}()
	usage, apiErr := RerankHandler(c, info, bcResp5)
	if apiErr != nil {
		t.Fatalf("unexpected error from Jina-compatible rerank response: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage for successful Jina rerank")
	}
	// Business rule: Jina responses only report TotalTokens; the handler
	// must mirror it into PromptTokens so downstream billing sees a
	// consistent (non-zero) prompt token count instead of silently
	// under-billing prompt usage to 0.
	if usage.TotalTokens != 57 || usage.PromptTokens != 57 {
		t.Fatalf("expected PromptTokens mirrored from TotalTokens (57/57), got prompt=%d total=%d",
			usage.PromptTokens, usage.TotalTokens)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 written to caller, got %d", rec.Code)
	}
	var out dto.RerankResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body is not valid RerankResponse JSON: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("expected 2 results echoed to caller, got %d", len(out.Results))
	}
	if out.Results[0].Document != "doc-a" || out.Results[1].Document != "doc-b" {
		t.Fatalf("expected documents passed through unmodified, got %q / %q",
			out.Results[0].Document, out.Results[1].Document)
	}
	if out.Results[0].RelevanceScore != 0.91 {
		t.Fatalf("expected relevance score 0.91 preserved, got %v", out.Results[0].RelevanceScore)
	}
}

// Xinference upstream, ReturnDocuments=true, upstream omitted the document
// text (empty string): the handler must backfill the document from the
// caller's own original request documents (info.Documents[index]) rather
// than leaving it blank — this is the business behavior that lets clients
// who didn't ask Xinference to echo documents still get them in the
// unified response shape.
func TestRerankHandler_XinferencePath_BackfillsMissingDocumentFromRequest(t *testing.T) {
	upstreamBody := `{
		"results": [
			{"index": 0, "relevance_score": 0.75, "document": ""},
			{"index": 1, "relevance_score": 0.30, "document": "explicit-doc"}
		]
	}`

	c, rec := routerRelayNewGinCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXinference},
		RerankerInfo: &relaycommon.RerankerInfo{
			ReturnDocuments: true,
			Documents:       []any{"original-doc-0", "original-doc-1"},
		},
	}
	info.SetEstimatePromptTokens(13)

	bcResp4 := routerRelayUpstreamResp(upstreamBody)
	defer func() {
		if bcResp4 != nil {
			_ = bcResp4.Body.Close()
		}
	}()
	usage, apiErr := RerankHandler(c, info, bcResp4)
	if apiErr != nil {
		t.Fatalf("unexpected error from Xinference rerank response: %v", apiErr)
	}
	// Xinference path prices off the caller's own estimated prompt tokens,
	// not anything upstream returns (Xinference doesn't report usage).
	if usage.PromptTokens != 13 || usage.TotalTokens != 13 {
		t.Fatalf("expected usage mirrored from RelayInfo estimate (13/13), got prompt=%d total=%d",
			usage.PromptTokens, usage.TotalTokens)
	}

	var out dto.RerankResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body is not valid RerankResponse JSON: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out.Results))
	}
	if out.Results[0].Document != "original-doc-0" {
		t.Fatalf("expected empty upstream document backfilled from info.Documents[0], got %q", out.Results[0].Document)
	}
	if out.Results[1].Document != "explicit-doc" {
		t.Fatalf("expected non-empty upstream document preserved as-is, got %q", out.Results[1].Document)
	}
}

// Xinference upstream but ReturnDocuments=false: the handler must NOT leak
// any document text back to the caller, even though the upstream body
// carried one. This is a privacy/cost boundary — clients who didn't ask
// for documents back shouldn't get them (and shouldn't pay bandwidth for
// them being echoed).
func TestRerankHandler_XinferencePath_ReturnDocumentsFalse_OmitsDocument(t *testing.T) {
	upstreamBody := `{
		"results": [
			{"index": 0, "relevance_score": 0.5, "document": "should-not-leak"}
		]
	}`

	c, rec := routerRelayNewGinCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXinference},
		RerankerInfo: &relaycommon.RerankerInfo{
			ReturnDocuments: false,
			Documents:       []any{"secret"},
		},
	}

	bcResp3 := routerRelayUpstreamResp(upstreamBody)
	defer func() {
		if bcResp3 != nil {
			_ = bcResp3.Body.Close()
		}
	}()
	usage, apiErr := RerankHandler(c, info, bcResp3)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected usage to still be populated even when documents are withheld")
	}

	var out dto.RerankResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body is not valid RerankResponse JSON: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out.Results))
	}
	if out.Results[0].Document != nil {
		t.Fatalf("expected document withheld when ReturnDocuments=false, got %v", out.Results[0].Document)
	}
}

// Malformed upstream JSON on the Jina-compatible path must surface as a
// caller-facing bad-response-body error rather than a 200 with garbage
// data, and must NOT write a success body to the response writer.
func TestRerankHandler_JinaPath_MalformedJSON_ReturnsBadResponseError(t *testing.T) {
	c, rec := routerRelayNewGinCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RerankerInfo: &relaycommon.RerankerInfo{},
	}

	bcResp2 := routerRelayUpstreamResp(`{not-json`)
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	usage, apiErr := RerankHandler(c, info, bcResp2)
	if apiErr == nil {
		t.Fatal("expected error for malformed upstream JSON, got nil")
	}
	if usage != nil {
		t.Fatalf("expected nil usage on decode failure, got %+v", usage)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 status on bad response body, got %d", apiErr.StatusCode)
	}
	// httptest.ResponseRecorder.Code defaults to 200 even with nothing
	// written, so assert on the body instead: the handler must bail out
	// before calling c.JSON, leaving nothing written to the writer.
	if rec.Body.Len() != 0 {
		t.Fatalf("must not write a success body when upstream JSON failed to decode, got %q", rec.Body.String())
	}
}

// Malformed upstream JSON on the Xinference path must fail the same way
// (separate decode branch in the handler — must be covered independently
// of the Jina-path decode failure above).
func TestRerankHandler_XinferencePath_MalformedJSON_ReturnsBadResponseError(t *testing.T) {
	c, _ := routerRelayNewGinCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXinference},
		RerankerInfo: &relaycommon.RerankerInfo{},
	}

	bcResp1 := routerRelayUpstreamResp(`not even close to json`)
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	usage, apiErr := RerankHandler(c, info, bcResp1)
	if apiErr == nil {
		t.Fatal("expected error for malformed Xinference JSON, got nil")
	}
	if usage != nil {
		t.Fatalf("expected nil usage on Xinference decode failure, got %+v", usage)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 status on bad Xinference response body, got %d", apiErr.StatusCode)
	}
}

// A response body that errors on Read (rather than being malformed JSON)
// must surface as ErrorCodeReadResponseBodyFailed, exercising the
// earliest failure branch in the handler.
type routerRelayBrokenReadCloser struct{}

func (routerRelayBrokenReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (routerRelayBrokenReadCloser) Close() error              { return nil }

func TestRerankHandler_BodyReadFailure_ReturnsReadBodyError(t *testing.T) {
	c, _ := routerRelayNewGinCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
		RerankerInfo: &relaycommon.RerankerInfo{},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       routerRelayBrokenReadCloser{},
		Header:     http.Header{},
	}

	usage, apiErr := RerankHandler(c, info, resp)
	if apiErr == nil {
		t.Fatal("expected error when upstream response body read fails")
	}
	if usage != nil {
		t.Fatalf("expected nil usage on body-read failure, got %+v", usage)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 status on body-read failure, got %d", apiErr.StatusCode)
	}
}

// Empty results set on the Xinference path (upstream found nothing to
// rerank) must not crash and must produce a zero-length, non-nil results
// slice rather than being silently dropped.
func TestRerankHandler_XinferencePath_EmptyResults(t *testing.T) {
	c, rec := routerRelayNewGinCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta:  &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXinference},
		RerankerInfo: &relaycommon.RerankerInfo{ReturnDocuments: true, Documents: []any{}},
	}

	bcResp0 := routerRelayUpstreamResp(`{"results": []}`)
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	usage, apiErr := RerankHandler(c, info, bcResp0)
	if apiErr != nil {
		t.Fatalf("unexpected error on empty results: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage even when there are no results")
	}
	var out dto.RerankResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body is not valid RerankResponse JSON: %v", err)
	}
	if len(out.Results) != 0 {
		t.Fatalf("expected zero results echoed, got %d", len(out.Results))
	}
}
