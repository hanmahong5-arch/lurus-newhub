package handler

// faultsim_retrieval_test.go — oracle for the rerank / embeddings wires of the
// UAT fault simulator. Handler-level tests pin the wire shapes; the end-to-end
// test proves the claim that matters: newhub's OWN relay (Jina rerank adaptor,
// OpenAI embeddings adaptor, cycle-22 validation and metering) accepts or
// rejects the simulator's answers exactly as designed.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// faultSimRetrievalUpstream mounts the simulator under the paths the Jina and
// OpenAI adaptors append to a base URL.
func faultSimRetrievalUpstream() http.HandlerFunc {
	e := gin.New()
	e.POST("/v1/rerank", FaultSimRerank)
	e.POST("/v1/embeddings", FaultSimEmbeddings)
	return e.ServeHTTP
}

type faultSimRerankResp struct {
	Model   string `json:"model"`
	Results []struct {
		Document       map[string]string `json:"document"`
		Index          int               `json:"index"`
		RelevanceScore float64           `json:"relevance_score"`
	} `json:"results"`
	Usage map[string]int `json:"usage"`
}

func faultSimRerank(t *testing.T, body string) (*httptest.ResponseRecorder, faultSimRerankResp) {
	t.Helper()
	w := faultSimDoPost(faultSimRetrievalUpstream(), "/v1/rerank", body, map[string]string{"X-Faultsim-Token": "tok"})
	var resp faultSimRerankResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, w.Body.String())
		}
	}
	return w, resp
}

func TestFaultSimRerank_RequiresToken(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "tok")
	gin.SetMode(gin.TestMode)
	h := faultSimRetrievalUpstream()
	for _, path := range []string{"/v1/rerank", "/v1/embeddings"} {
		body := `{"model":"ok","query":"q","documents":["a"],"input":"a"}`
		if w := faultSimDoPost(h, path, body, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: code=%d, want 401", path, w.Code)
		}
		if w := faultSimDoPost(h, path, body, map[string]string{"X-Faultsim-Token": "wrong"}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s with wrong token: code=%d, want 401", path, w.Code)
		}
		if w := faultSimDoPost(h, path, body, map[string]string{"Authorization": "Bearer tok"}); w.Code != http.StatusOK {
			t.Errorf("%s with bearer token: code=%d, want 200; body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestFaultSimRerank_OrderingTopNAndDocuments(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "tok")
	gin.SetMode(gin.TestMode)

	t.Run("sorted by score descending and not in input order", func(t *testing.T) {
		w, resp := faultSimRerank(t, `{"model":"ok","query":"q","documents":["a","b","c","d","e"]}`)
		if w.Code != http.StatusOK || len(resp.Results) != 5 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		inInputOrder := true
		for i, r := range resp.Results {
			if i > 0 && r.RelevanceScore > resp.Results[i-1].RelevanceScore {
				t.Fatalf("results not descending at %d: %+v", i, resp.Results)
			}
			if r.Index != i {
				inInputOrder = false
			}
		}
		if inInputOrder {
			t.Fatalf("ranked order equals input order, so a missing sort would go unnoticed: %+v", resp.Results)
		}
		// The score belongs to the document, not to its rank.
		for _, r := range resp.Results {
			if want := faultSimRerankScore(r.Index); r.RelevanceScore != want {
				t.Errorf("index %d score=%v, want %v", r.Index, r.RelevanceScore, want)
			}
		}
	})
	t.Run("top_n cuts after sorting", func(t *testing.T) {
		_, all := faultSimRerank(t, `{"model":"ok","query":"q","documents":["a","b","c","d","e"]}`)
		w, resp := faultSimRerank(t, `{"model":"ok","query":"q","documents":["a","b","c","d","e"],"top_n":2}`)
		if w.Code != http.StatusOK || len(resp.Results) != 2 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		for i := range resp.Results {
			if resp.Results[i].Index != all.Results[i].Index {
				t.Fatalf("top_n kept %+v, want the head of %+v", resp.Results, all.Results)
			}
		}
	})
	t.Run("top_n above the document count returns all", func(t *testing.T) {
		_, resp := faultSimRerank(t, `{"model":"ok","query":"q","documents":["a","b"],"top_n":50}`)
		if len(resp.Results) != 2 {
			t.Fatalf("results=%d, want 2", len(resp.Results))
		}
	})
	t.Run("documents only when return_documents is true", func(t *testing.T) {
		for _, body := range []string{
			`{"model":"ok","query":"q","documents":["a","b"]}`,
			`{"model":"ok","query":"q","documents":["a","b"],"return_documents":false}`,
		} {
			w, _ := faultSimRerank(t, body)
			if strings.Contains(w.Body.String(), `"document"`) {
				t.Errorf("document present without return_documents: %s", w.Body.String())
			}
		}
		_, resp := faultSimRerank(t, `{"model":"ok","query":"q","documents":["alpha one",{"text":"beta two"}],"return_documents":true}`)
		want := map[int]string{0: "alpha one", 1: "beta two"}
		for _, r := range resp.Results {
			if r.Document["text"] != want[r.Index] {
				t.Errorf("index %d document=%v, want text %q", r.Index, r.Document, want[r.Index])
			}
		}
	})
	t.Run("usage is the word count of query plus documents", func(t *testing.T) {
		_, resp := faultSimRerank(t, `{"model":"ok","query":"two words","documents":["one","three whole words"]}`)
		if got := resp.Usage["total_tokens"]; got != 6 {
			t.Fatalf("total_tokens=%d, want 6 (usage=%v)", got, resp.Usage)
		}
		if _, ok := resp.Usage["prompt_tokens"]; ok {
			t.Errorf("Jina reports only total_tokens, got %v", resp.Usage)
		}
	})
	t.Run("empty documents is 400", func(t *testing.T) {
		if w, _ := faultSimRerank(t, `{"model":"ok","query":"q","documents":[]}`); w.Code != http.StatusBadRequest {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

func TestFaultSimRerank_UsageModesAndFaults(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "tok")
	gin.SetMode(gin.TestMode)

	t.Run("ok-nousage omits usage", func(t *testing.T) {
		w, _ := faultSimRerank(t, `{"model":"ok-nousage","query":"q","documents":["a"]}`)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"usage"`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("ok-badusage is self-contradictory", func(t *testing.T) {
		_, resp := faultSimRerank(t, `{"model":"ok-badusage-1","query":"q","documents":["a"]}`)
		if resp.Usage["prompt_tokens"] <= 0 || resp.Usage["total_tokens"] != 0 {
			t.Fatalf("usage=%v, want prompt_tokens>0 with total_tokens=0", resp.Usage)
		}
	})
	t.Run("fault modes reuse the shared faults", func(t *testing.T) {
		if w, _ := faultSimRerank(t, `{"model":"http_500","query":"q","documents":["a"]}`); w.Code != http.StatusInternalServerError {
			t.Fatalf("http_500: code=%d", w.Code)
		}
		if w, _ := faultSimRerank(t, `{"model":"rate_limit_429","query":"q","documents":["a"]}`); w.Code != http.StatusTooManyRequests {
			t.Fatalf("rate_limit_429: code=%d", w.Code)
		}
	})
	t.Run("unknown model is 400", func(t *testing.T) {
		if w, _ := faultSimRerank(t, `{"model":"okay","query":"q","documents":["a"]}`); w.Code != http.StatusBadRequest {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

func faultSimEmbed(t *testing.T, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := faultSimDoPost(faultSimRetrievalUpstream(), "/v1/embeddings", body, map[string]string{"X-Faultsim-Token": "tok"})
	var resp map[string]any
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v body=%s", err, w.Body.String())
		}
	}
	return w, resp
}

func TestFaultSimEmbeddings_CountDimsAndBase64(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", "tok")
	gin.SetMode(gin.TestMode)

	t.Run("string input gives one default-length vector", func(t *testing.T) {
		w, resp := faultSimEmbed(t, `{"model":"ok","input":"hello there"}`)
		data, _ := resp["data"].([]any)
		if w.Code != http.StatusOK || len(data) != 1 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		vec, _ := data[0].(map[string]any)["embedding"].([]any)
		if len(vec) != faultSimDefaultDims {
			t.Fatalf("dims=%d, want %d", len(vec), faultSimDefaultDims)
		}
	})
	t.Run("array input gives one item per input at the requested dimensions", func(t *testing.T) {
		_, resp := faultSimEmbed(t, `{"model":"ok","input":["a b","c","d e f"],"dimensions":5}`)
		data, _ := resp["data"].([]any)
		if len(data) != 3 {
			t.Fatalf("items=%d, want 3", len(data))
		}
		for i, it := range data {
			m := it.(map[string]any)
			if int(m["index"].(float64)) != i || len(m["embedding"].([]any)) != 5 {
				t.Errorf("item %d = %v", i, m)
			}
		}
		usage, _ := resp["usage"].(map[string]any)
		if usage["prompt_tokens"] != float64(6) || usage["total_tokens"] != float64(6) {
			t.Errorf("usage=%v, want 6/6 (word count)", usage)
		}
	})
	t.Run("vectors are deterministic and input-dependent", func(t *testing.T) {
		_, a1 := faultSimEmbed(t, `{"model":"ok","input":"same"}`)
		_, a2 := faultSimEmbed(t, `{"model":"ok","input":"same"}`)
		_, b := faultSimEmbed(t, `{"model":"ok","input":"different"}`)
		j := func(v map[string]any) string { raw, _ := json.Marshal(v["data"]); return string(raw) }
		if j(a1) != j(a2) || j(a1) == j(b) {
			t.Fatalf("a1=%s a2=%s b=%s", j(a1), j(a2), j(b))
		}
	})
	t.Run("base64 decodes to the same little-endian float32 vector", func(t *testing.T) {
		_, plain := faultSimEmbed(t, `{"model":"ok","input":"x y","dimensions":6}`)
		_, enc := faultSimEmbed(t, `{"model":"ok","input":"x y","dimensions":6,"encoding_format":"base64"}`)
		want := plain["data"].([]any)[0].(map[string]any)["embedding"].([]any)
		s, ok := enc["data"].([]any)[0].(map[string]any)["embedding"].(string)
		if !ok {
			t.Fatalf("base64 embedding is not a string: %v", enc["data"])
		}
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(raw) != 6*4 {
			t.Fatalf("decode: err=%v len=%d", err, len(raw))
		}
		for i := range want {
			got := math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
			if got != float32(want[i].(float64)) {
				t.Errorf("component %d: base64=%v float=%v", i, got, want[i])
			}
		}
	})
	t.Run("ok-nousage omits usage", func(t *testing.T) {
		w, _ := faultSimEmbed(t, `{"model":"ok-nousage","input":"a"}`)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"usage"`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("bad requests and faults", func(t *testing.T) {
		for _, body := range []string{
			`{"model":"ok"}`,
			`{"model":"ok","input":[]}`,
			`{"model":"ok","input":"a","dimensions":100000}`,
			`{"model":"ok","input":"a","dimensions":-3}`,
		} {
			if w, _ := faultSimEmbed(t, body); w.Code != http.StatusBadRequest {
				t.Errorf("%s: code=%d, want 400", body, w.Code)
			}
		}
		if w, _ := faultSimEmbed(t, `{"model":"http_500","input":"a"}`); w.Code != http.StatusInternalServerError {
			t.Errorf("http_500: code=%d", w.Code)
		}
	})
}

// TestFaultSimRetrieval_RelayAcceptsAndRejects drives the real relay (TokenAuth
// -> Distribute -> Relay) with a Jina channel and an OpenAI channel whose
// base_url is the in-process simulator.
func TestFaultSimRetrieval_RelayAcceptsAndRejects(t *testing.T) {
	t.Setenv("FAULTSIM_TOKEN", faultSimTestKey)
	pinRetryTimes(t, 0)
	ctx := setupRelaySuccessRouter(t, faultSimRetrievalUpstream())

	prevRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(prevRatio) })
	if err := ratio_setting.UpdateModelRatioByJSONString(
		`{"ok-rerank":1.0,"ok-badusage-rerank":1.0,"ok-nousage-rerank":1.0,"ok-embed":1.0,"ok-nousage-embed":1.0}`); err != nil {
		t.Fatal(err)
	}

	// The fixture channel becomes the Jina rerank channel.
	ctx.channel.Type = constant.ChannelTypeJina
	ctx.channel.Models = "ok-rerank,ok-badusage-rerank,ok-nousage-rerank"
	if err := ctx.db.Save(ctx.channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := ctx.db.Where("channel_id = ?", ctx.channel.Id).Delete(&repo.Ability{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ctx.channel.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	base := ctx.upstream.URL
	weight, prio := uint(10), int64(0)
	embCh := &repo.Channel{
		Id: ctx.channel.Id + 1, TenantId: "default", Type: constant.ChannelTypeOpenAI, Key: faultSimTestKey,
		Status: common.ChannelStatusEnabled, Name: "faultsim-embeddings", BaseURL: &base,
		Models: "ok-embed,ok-nousage-embed", Group: "default", Weight: &weight, Priority: &prio,
	}
	if err := ctx.db.Create(embCh).Error; err != nil {
		t.Fatal(err)
	}
	if err := embCh.AddAbilities(nil); err != nil {
		t.Fatal(err)
	}
	g := ctx.router.Group("/v1")
	g.Use(middleware.StampRelayFormat(), middleware.TokenAuth(), middleware.Distribute())
	g.POST("/rerank", func(c *gin.Context) { Relay(c, types.RelayFormatRerank) })
	g.POST("/embeddings", func(c *gin.Context) { Relay(c, types.RelayFormatEmbedding) })

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ctx.token.Key)
		w := httptest.NewRecorder()
		ctx.router.ServeHTTP(w, req)
		return w
	}
	consumeLogs := func() []repo.Log {
		var logs []repo.Log
		if err := ctx.db.Where("type = ?", repo.LogTypeConsume).Order("id").Find(&logs).Error; err != nil {
			t.Fatal(err)
		}
		return logs
	}

	docs := `"documents":["alpha beta","gamma","delta epsilon zeta","eta","theta iota"]`

	t.Run("rerank succeeds sorted with upstream-reported usage", func(t *testing.T) {
		w := post("/v1/rerank", `{"model":"ok-rerank","query":"find me",`+docs+`,"top_n":3}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		// The gateway re-renders usage as its own nested struct, so only the
		// results are decoded here.
		var resp struct {
			Results []struct {
				RelevanceScore float64 `json:"relevance_score"`
			} `json:"results"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != 3 {
			t.Fatalf("results=%d, want top_n=3: %s", len(resp.Results), w.Body.String())
		}
		for i := 1; i < len(resp.Results); i++ {
			if resp.Results[i].RelevanceScore > resp.Results[i-1].RelevanceScore {
				t.Fatalf("not descending: %s", w.Body.String())
			}
		}
		if strings.Contains(w.Body.String(), `"document"`) {
			t.Errorf("document leaked without return_documents: %s", w.Body.String())
		}
		logs := consumeLogs()
		if len(logs) != 1 {
			t.Fatalf("consume rows=%d, want 1; resp=%s", len(logs), w.Body.String())
		}
		if logs[0].UsageSource != "upstream" {
			t.Errorf("usage_source=%q, want upstream", logs[0].UsageSource)
		}
	})
	t.Run("rerank returns documents on request", func(t *testing.T) {
		w := post("/v1/rerank", `{"model":"ok-rerank","query":"find me",`+docs+`,"return_documents":true}`)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"document"`) {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("rerank without usage is metered as estimated", func(t *testing.T) {
		n := len(consumeLogs())
		w := post("/v1/rerank", `{"model":"ok-nousage-rerank","query":"find me",`+docs+`}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		logs := consumeLogs()
		if len(logs) != n+1 || logs[len(logs)-1].UsageSource != "estimated" {
			t.Fatalf("logs=%d (before %d), last source=%q", len(logs), n, logs[len(logs)-1].UsageSource)
		}
	})
	t.Run("rerank with contradictory usage is rejected and unbilled", func(t *testing.T) {
		n := len(consumeLogs())
		w := post("/v1/rerank", `{"model":"ok-badusage-rerank","query":"find me",`+docs+`}`)
		if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "invalid_provider_usage") {
			t.Fatalf("status=%d, want 502 invalid_provider_usage; body=%s", w.Code, w.Body.String())
		}
		if got := len(consumeLogs()); got != n {
			t.Errorf("consume rows %d -> %d: a rejected response was billed", n, got)
		}
	})
	t.Run("embeddings succeed with upstream usage, float and base64", func(t *testing.T) {
		n := len(consumeLogs())
		w := post("/v1/embeddings", `{"model":"ok-embed","input":["one two","three"],"dimensions":4}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Data  []struct{ Embedding []float64 } `json:"data"`
			Usage map[string]int                  `json:"usage"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Data) != 2 || len(resp.Data[0].Embedding) != 4 || resp.Usage["total_tokens"] != 3 {
			t.Fatalf("resp=%s", w.Body.String())
		}
		w = post("/v1/embeddings", `{"model":"ok-embed","input":"one","dimensions":4,"encoding_format":"base64"}`)
		var b64 struct {
			Data []struct{ Embedding string } `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &b64); err != nil || w.Code != http.StatusOK || len(b64.Data) != 1 {
			t.Fatalf("base64 status=%d err=%v body=%s", w.Code, err, w.Body.String())
		}
		if raw, err := base64.StdEncoding.DecodeString(b64.Data[0].Embedding); err != nil || len(raw) != 16 {
			t.Fatalf("base64 payload: err=%v len=%d", err, len(raw))
		}
		if got := len(consumeLogs()); got != n+2 {
			t.Errorf("consume rows %d -> %d, want +2", n, got)
		}
	})
	t.Run("embeddings without usage still settle", func(t *testing.T) {
		n := len(consumeLogs())
		w := post("/v1/embeddings", `{"model":"ok-nousage-embed","input":"one two three"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if got := len(consumeLogs()); got != n+1 {
			t.Errorf("consume rows %d -> %d, want +1", n, got)
		}
	})
}
