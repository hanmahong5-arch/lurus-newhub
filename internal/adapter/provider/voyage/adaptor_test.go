package voyage

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", nil)
	return c, w
}

func infoFor(mode int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:   mode,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.voyageai.com", ApiKey: "k"},
	}
}

type rc struct{ *strings.Reader }

func (rc) Close() error { return nil }

func body(s string) io.ReadCloser { return rc{strings.NewReader(s)} }

func TestGetRequestURL(t *testing.T) {
	a := &Adaptor{}
	cases := map[int]string{
		relayconstant.RelayModeRerank:     "https://api.voyageai.com/v1/rerank",
		relayconstant.RelayModeEmbeddings: "https://api.voyageai.com/v1/embeddings",
	}
	for mode, want := range cases {
		got, err := a.GetRequestURL(infoFor(mode))
		if err != nil || got != want {
			t.Errorf("mode %d: got %q, %v want %q", mode, got, err, want)
		}
	}
	if _, err := a.GetRequestURL(infoFor(relayconstant.RelayModeChatCompletions)); err == nil {
		t.Error("chat mode must be rejected")
	}
}

func TestSetupRequestHeader_Bearer(t *testing.T) {
	c, _ := newCtx()
	h := http.Header{}
	if err := (&Adaptor{}).SetupRequestHeader(c, &h, infoFor(relayconstant.RelayModeRerank)); err != nil {
		t.Fatal(err)
	}
	if h.Get("Authorization") != "Bearer k" {
		t.Errorf("auth = %q", h.Get("Authorization"))
	}
}

// Voyage names the rerank cut-off top_k, not top_n: sending the OpenAI/Jina
// name would be silently ignored upstream.
func TestConvertRerankRequest_FieldMapping(t *testing.T) {
	c, _ := newCtx()
	ret := true
	out, err := (&Adaptor{}).ConvertRerankRequest(c, relayconstant.RelayModeRerank, dto.RerankRequest{
		Model: "model-a", Query: "q", Documents: []any{"d1", "d2"}, TopN: 1, ReturnDocuments: &ret,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["top_k"] != float64(1) || m["return_documents"] != true || m["query"] != "q" || m["model"] != "model-a" {
		t.Errorf("payload = %s", b)
	}
	if _, has := m["top_n"]; has {
		t.Errorf("top_n must not be forwarded: %s", b)
	}
}

func TestConvertEmbeddingRequest_FieldMapping(t *testing.T) {
	c, _ := newCtx()
	info := infoFor(relayconstant.RelayModeEmbeddings)
	out, err := (&Adaptor{}).ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{
		Model: "model-a", Input: []any{"a"}, Dimensions: 256, InputType: "query", EncodingFormat: "float",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["output_dimension"] != float64(256) || m["input_type"] != "query" {
		t.Errorf("payload = %s", b)
	}
	if _, has := m["dimensions"]; has {
		t.Errorf("dimensions must be renamed: %s", b)
	}
	// Voyage accepts encoding_format only as "base64"; "float" must be omitted.
	if _, has := m["encoding_format"]; has {
		t.Errorf("float encoding_format must be omitted: %s", b)
	}
	out2, _ := (&Adaptor{}).ConvertEmbeddingRequest(c, info, dto.EmbeddingRequest{
		Model: "model-a", Input: "a", EncodingFormat: "base64",
	})
	b2, _ := json.Marshal(out2)
	if !strings.Contains(string(b2), `"encoding_format":"base64"`) {
		t.Errorf("base64 must be forwarded: %s", b2)
	}
}

func TestDoResponse_RerankMapsDataToResultsAndUsage(t *testing.T) {
	raw, err := os.ReadFile("testdata/rerank_response.json")
	if err != nil {
		t.Fatal(err)
	}
	c, w := newCtx()
	usage, apiErr := (&Adaptor{}).DoResponse(c, &http.Response{StatusCode: 200, Body: body(string(raw))}, infoFor(relayconstant.RelayModeRerank))
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if u := usage.(*dto.Usage); u.TotalTokens != 26 {
		t.Errorf("usage = %+v", u)
	}
	var got dto.RerankResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 2 || got.Results[0].Index != 1 || got.Results[0].RelevanceScore != 0.9375 {
		t.Errorf("results = %+v", got.Results)
	}
}

func TestDoResponse_RerankBadBody(t *testing.T) {
	c, _ := newCtx()
	_, apiErr := (&Adaptor{}).DoResponse(c, &http.Response{StatusCode: 200, Body: body("not json")}, infoFor(relayconstant.RelayModeRerank))
	if apiErr == nil {
		t.Fatal("want error")
	}
}

func TestDoResponse_InvalidMode(t *testing.T) {
	c, _ := newCtx()
	_, apiErr := (&Adaptor{}).DoResponse(c, &http.Response{StatusCode: 200, Body: body("{}")}, infoFor(relayconstant.RelayModeChatCompletions))
	if apiErr == nil {
		t.Fatal("want error")
	}
}
