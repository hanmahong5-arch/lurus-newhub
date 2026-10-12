package handler

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/testkit/fakeupstream"

	"github.com/gin-gonic/gin"
)

// Retrieval wires of the fault simulator: rerank (Jina) and embeddings
// (OpenAI). They exist so retrieval metering — usage_source, the
// invalid_provider_usage rejection, ordering and top_n bounds — can be driven
// end to end on UAT through a Jina / OpenAI channel whose base_url is
// <host>/api/v2/faultsim, with no vendor key. Same FAULTSIM_TOKEN gate and
// auth as the other wires (see faultsim.go for the safety properties).
//
// Model modes (success mode is "ok" / "ok-*", as everywhere else):
//
//   - ok, ok-*:        a well-formed answer with a deterministic usage figure.
//   - ok-nousage*:     usage omitted entirely, so the gateway must estimate
//     (usage_source=estimated).
//   - ok-badusage*:    rerank only — a self-contradictory usage block
//     (prompt_tokens>0 with total_tokens=0) the gateway must reject with
//     invalid_provider_usage (502, unbilled). On embeddings it behaves as
//     plain "ok".
//   - any fakeupstream fault mode (http_500, rate_limit_429, ...): that fault.
const (
	faultSimModeNoUsage  = "ok-nousage"
	faultSimModeBadUsage = "ok-badusage"

	// faultSimDefaultDims is the embedding vector length when the request
	// names none; faultSimMaxDims bounds an explicit request.
	faultSimDefaultDims = 8
	faultSimMaxDims     = 4096
)

// faultSimRetrievalPrelude authenticates, decodes the body and resolves the
// mode. handled=true means a response (401, 400 or a fault) was already
// written and the caller must return.
func faultSimRetrievalPrelude(c *gin.Context) (body map[string]any, mode string, handled bool) {
	if !faultSimAuthorized(c) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"message": "fault simulator token required", "type": "faultsim"},
		})
		return nil, "", true
	}
	body = map[string]any{}
	_ = c.ShouldBindJSON(&body)
	model, _ := body["model"].(string)
	mode = model
	if q := c.Query("mode"); q != "" {
		mode = q
	}
	if IsFaultSimOKModel(mode) {
		return body, mode, false
	}
	if !fakeupstream.IsFaultMode(mode) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("unknown fault mode %q; supported: %v, or ok / ok-* for success", mode, FaultSimModes),
				"type":    "faultsim",
			},
		})
		return nil, "", true
	}
	// Faults are wire-agnostic HTTP-level failures; the chat wire's rendering
	// is as good as any for a rerank / embeddings call.
	fakeupstream.WriteFault(c.Writer, c.Request, fakeupstream.WireOpenAIChat, mode, fakeupstream.FaultParamsFromQuery(c.Request))
	return nil, "", true
}

func faultSimBadRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": msg, "type": "faultsim"}})
}

// faultSimDocText renders one rerank document (a string, or an object with a
// "text" field as Jina accepts) to the text that is scored and echoed back.
func faultSimDocText(doc any) string {
	switch d := doc.(type) {
	case string:
		return d
	case map[string]any:
		if s, ok := d["text"].(string); ok {
			return s
		}
	}
	raw, _ := json.Marshal(doc)
	return string(raw)
}

// faultSimRerankScore is the relevance score of the document at index i. It is
// a pure function of the index and deliberately NOT monotonic in it, so the
// natural document order is never already the ranked order: a handler that
// forgot to sort would be caught. 7 is coprime with 11, so up to 11 documents
// get distinct scores in (0,1].
func faultSimRerankScore(i int) float64 {
	return float64((i*7+3)%11+1) / 12
}

// FaultSimRerank serves POST /faultsim/v1/rerank (Jina wire; the Jina adaptor
// appends /v1/rerank to the channel base_url).
//
// Results are ordered by relevance_score descending (index ascending on ties),
// cut to top_n when it is positive, and carry {"document":{"text":...}} only
// when return_documents is true. usage.total_tokens is the word count of the
// query plus all documents — a pure function of the input, so a test can
// compute the expected charge.
func FaultSimRerank(c *gin.Context) {
	body, mode, handled := faultSimRetrievalPrelude(c)
	if handled {
		return
	}
	docs, _ := body["documents"].([]any)
	if len(docs) == 0 {
		faultSimBadRequest(c, "documents must be a non-empty array")
		return
	}
	query, _ := body["query"].(string)
	topN := 0
	if v, ok := body["top_n"].(float64); ok && v > 0 {
		topN = int(v)
	}
	returnDocs, _ := body["return_documents"].(bool)

	type result struct {
		Document       map[string]string `json:"document,omitempty"`
		Index          int               `json:"index"`
		RelevanceScore float64           `json:"relevance_score"`
	}
	words := len(strings.Fields(query))
	results := make([]result, len(docs))
	for i, d := range docs {
		text := faultSimDocText(d)
		words += len(strings.Fields(text))
		results[i] = result{Index: i, RelevanceScore: faultSimRerankScore(i)}
		if returnDocs {
			results[i].Document = map[string]string{"text": text}
		}
	}
	sort.SliceStable(results, func(a, b int) bool {
		return results[a].RelevanceScore > results[b].RelevanceScore
	})
	if topN > 0 && topN < len(results) {
		results = results[:topN]
	}

	resp := gin.H{"model": body["model"], "results": results}
	switch {
	case strings.HasPrefix(mode, faultSimModeNoUsage):
		// usage omitted on purpose
	case strings.HasPrefix(mode, faultSimModeBadUsage):
		resp["usage"] = gin.H{"prompt_tokens": words + 1, "total_tokens": 0}
	default:
		resp["usage"] = gin.H{"total_tokens": words}
	}
	c.JSON(http.StatusOK, resp)
}

// faultSimEmbeddingInputs flattens the OpenAI `input` field (a string, or an
// array of strings / token arrays) into one text per embedded item.
func faultSimEmbeddingInputs(raw any) []string {
	switch v := raw.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
				continue
			}
			b, _ := json.Marshal(item)
			out = append(out, string(b))
		}
		return out
	}
	return nil
}

// faultSimVector is the deterministic embedding of text: every component is a
// pure function of the text and its position, in [0,1).
func faultSimVector(text string, dims int) []float32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(text))
	seed := h.Sum32()
	v := make([]float32, dims)
	for j := range v {
		v[j] = float32((seed+uint32(j)*2654435761)%1000) / 1000
	}
	return v
}

// faultSimEncodeBase64 packs a vector as little-endian float32, the layout
// OpenAI uses for encoding_format=base64.
func faultSimEncodeBase64(v []float32) string {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[4*i:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// FaultSimEmbeddings serves POST /faultsim/v1/embeddings (OpenAI wire; the
// OpenAI and Jina adaptors append /v1/embeddings to the channel base_url).
//
// One item per input, each a deterministic vector of `dimensions` floats
// (default 8), or a base64 string of little-endian float32 when
// encoding_format is "base64". usage prompt_tokens == total_tokens == the word
// count of all inputs; "ok-nousage*" omits usage.
func FaultSimEmbeddings(c *gin.Context) {
	body, mode, handled := faultSimRetrievalPrelude(c)
	if handled {
		return
	}
	inputs := faultSimEmbeddingInputs(body["input"])
	if len(inputs) == 0 {
		faultSimBadRequest(c, "input must be a non-empty string or array")
		return
	}
	dims := faultSimDefaultDims
	if v, ok := body["dimensions"].(float64); ok && v != 0 {
		if v < 1 || v > faultSimMaxDims {
			faultSimBadRequest(c, fmt.Sprintf("dimensions must be between 1 and %d", faultSimMaxDims))
			return
		}
		dims = int(v)
	}
	asBase64 := body["encoding_format"] == "base64"

	words := 0
	data := make([]gin.H, len(inputs))
	for i, text := range inputs {
		words += len(strings.Fields(text))
		vec := faultSimVector(text, dims)
		var emb any = vec
		if asBase64 {
			emb = faultSimEncodeBase64(vec)
		}
		data[i] = gin.H{"object": "embedding", "index": i, "embedding": emb}
	}

	resp := gin.H{"object": "list", "data": data, "model": body["model"]}
	if !strings.HasPrefix(mode, faultSimModeNoUsage) {
		resp["usage"] = gin.H{"prompt_tokens": words, "total_tokens": words}
	}
	c.JSON(http.StatusOK, resp)
}
