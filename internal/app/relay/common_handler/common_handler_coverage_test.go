package common_handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

func newRerankInfo(channelType int, returnDocuments bool, documents []any) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RerankerInfo: &relaycommon.RerankerInfo{
			ReturnDocuments: returnDocuments,
			Documents:       documents,
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: channelType,
		},
	}
}

func newTestGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/rerank", nil)
	return c, w
}

func newHTTPResponse(body string) *http.Response {
	return &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(body)),
	}
}

// TestRerankHandler_ReadBodyError exercises the io.ReadAll error branch by
// supplying a Body whose Read always fails.
func TestRerankHandler_ReadBodyError(t *testing.T) {
	c, _ := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeOpenAI, false, nil)
	resp := &http.Response{Body: io.NopCloser(errReader{})}

	usage, apiErr := RerankHandler(c, info, resp)

	if usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
	if apiErr == nil {
		t.Fatal("expected non-nil error")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeReadResponseBodyFailed {
		t.Errorf("errorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeReadResponseBodyFailed)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusInternalServerError)
	}
	if apiErr.Err == nil || apiErr.Err.Error() != "boom" {
		t.Errorf("Err = %v, want boom", apiErr.Err)
	}
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) { return 0, errBoom }

var errBoom = errBoomError{}

type errBoomError struct{}

func (errBoomError) Error() string { return "boom" }

// TestRerankHandler_XinferenceBadJSON exercises the xinference branch's
// unmarshal-error path.
func TestRerankHandler_XinferenceBadJSON(t *testing.T) {
	c, _ := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeXinference, false, nil)
	resp := newHTTPResponse("not-json{{{")

	usage, apiErr := RerankHandler(c, info, resp)

	if usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
	if apiErr == nil {
		t.Fatal("expected non-nil error")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("errorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusInternalServerError)
	}
}

// TestRerankHandler_DefaultBadJSON exercises the non-xinference branch's
// unmarshal-error path.
func TestRerankHandler_DefaultBadJSON(t *testing.T) {
	c, _ := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeOpenAI, false, nil)
	resp := newHTTPResponse("not-json{{{")

	usage, apiErr := RerankHandler(c, info, resp)

	if usage != nil {
		t.Fatalf("expected nil usage, got %+v", usage)
	}
	if apiErr == nil {
		t.Fatal("expected non-nil error")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("errorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

// TestRerankHandler_Default_HappyPath verifies the non-xinference branch:
// PromptTokens is overwritten from TotalTokens, and the response is
// forwarded verbatim (minus the overwritten usage field).
func TestRerankHandler_Default_HappyPath(t *testing.T) {
	c, w := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeOpenAI, false, nil)
	body := `{"results":[{"index":0,"relevance_score":0.75}],"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":42}}`
	resp := newHTTPResponse(body)

	usage, apiErr := RerankHandler(c, info, resp)

	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.TotalTokens != 42 {
		t.Errorf("TotalTokens = %d, want 42", usage.TotalTokens)
	}
	// PromptTokens must be overwritten to equal TotalTokens (line: jinaResp.Usage.PromptTokens = jinaResp.Usage.TotalTokens).
	if usage.PromptTokens != 42 {
		t.Errorf("PromptTokens = %d, want 42 (overwritten from TotalTokens)", usage.PromptTokens)
	}
	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"relevance_score":0.75`)) {
		t.Errorf("body missing relevance_score: %s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"prompt_tokens":42`)) {
		t.Errorf("body missing overwritten prompt_tokens: %s", w.Body.String())
	}
}

// TestRerankHandler_Xinference_ReturnDocuments_EmptyStringFallsBackToInput
// exercises: xinference branch, ReturnDocuments=true, document is an empty
// string -> falls back to info.Documents[result.Index].
func TestRerankHandler_Xinference_ReturnDocuments_EmptyStringFallsBackToInput(t *testing.T) {
	c, w := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeXinference, true, []any{"first-doc", "second-doc"})
	body := `{"results":[{"index":0,"relevance_score":0.9,"document":""},{"index":1,"relevance_score":0.5,"document":"actual-doc"}]}`
	resp := newHTTPResponse(body)

	usage, apiErr := RerankHandler(c, info, resp)

	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	// Both PromptTokens and TotalTokens come from GetEstimatePromptTokens(), default 0.
	if usage.PromptTokens != 0 || usage.TotalTokens != 0 {
		t.Errorf("usage = %+v, want zero-valued (no estimate set)", usage)
	}
	if w.Code != http.StatusOK {
		t.Errorf("HTTP status = %d, want %d", w.Code, http.StatusOK)
	}
	bodyStr := w.Body.String()
	if !bytes.Contains([]byte(bodyStr), []byte(`"document":"first-doc"`)) {
		t.Errorf("expected fallback to info.Documents[0]=first-doc, got: %s", bodyStr)
	}
	if !bytes.Contains([]byte(bodyStr), []byte(`"document":"actual-doc"`)) {
		t.Errorf("expected passthrough non-empty document, got: %s", bodyStr)
	}
}

// TestRerankHandler_Xinference_ReturnDocuments_NonStringDocument exercises:
// xinference branch, ReturnDocuments=true, document is a non-string type
// (e.g. a JSON object) -> passthrough unchanged.
func TestRerankHandler_Xinference_ReturnDocuments_NonStringDocument(t *testing.T) {
	c, w := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeXinference, true, nil)
	body := `{"results":[{"index":0,"relevance_score":0.3,"document":{"text":"nested"}}]}`
	resp := newHTTPResponse(body)

	usage, apiErr := RerankHandler(c, info, resp)

	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"document":{"text":"nested"}`)) {
		t.Errorf("expected passthrough of non-string document object, got: %s", w.Body.String())
	}
}

// TestRerankHandler_Xinference_ReturnDocumentsFalse verifies that when
// ReturnDocuments is false, the Document field is omitted entirely
// (json:"document,omitempty").
func TestRerankHandler_Xinference_ReturnDocumentsFalse(t *testing.T) {
	c, w := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeXinference, false, nil)
	body := `{"results":[{"index":0,"relevance_score":0.6,"document":"should-be-dropped"}]}`
	resp := newHTTPResponse(body)

	usage, apiErr := RerankHandler(c, info, resp)

	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("document")) {
		t.Errorf("expected document field to be omitted, got: %s", w.Body.String())
	}
}

// TestRerankHandler_Xinference_NilDocument verifies that when Document is
// nil (JSON null / absent) and ReturnDocuments is true, respResult.Document
// stays nil (still omitted from JSON due to omitempty).
func TestRerankHandler_Xinference_NilDocument(t *testing.T) {
	c, w := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeXinference, true, nil)
	body := `{"results":[{"index":0,"relevance_score":0.1}]}`
	resp := newHTTPResponse(body)

	usage, apiErr := RerankHandler(c, info, resp)

	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("document")) {
		t.Errorf("expected document field to be omitted for nil document, got: %s", w.Body.String())
	}
}

// TestRerankHandler_Xinference_MultipleResultsPreserveOrderAndIndex verifies
// that the []any slice constructed from xinference results preserves index
// alignment and count.
func TestRerankHandler_Xinference_MultipleResultsPreserveOrderAndIndex(t *testing.T) {
	c, _ := newTestGinContext()
	info := newRerankInfo(constant.ChannelTypeXinference, false, nil)
	body := `{"results":[{"index":2,"relevance_score":0.2},{"index":0,"relevance_score":0.9},{"index":1,"relevance_score":0.5}]}`
	resp := newHTTPResponse(body)

	usage, apiErr := RerankHandler(c, info, resp)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
}
