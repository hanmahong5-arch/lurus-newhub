package helper

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func quotedList(n int, item string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = item
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func embedBody(extra string, input string) string {
	return fmt.Sprintf(`{"model":"model-a","input":%s%s}`, input, extra)
}

func TestEmbeddingContract(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		valid bool
	}{
		{"2047 inputs", embedBody("", quotedList(2047, `"x"`)), true},
		{"2048 inputs", embedBody("", quotedList(2048, `"x"`)), true},
		{"2049 inputs", embedBody("", quotedList(2049, `"x"`)), false},
		{"empty array", embedBody("", `[]`), false},
		{"empty string item", embedBody("", `["a",""]`), false},
		{"empty single string", embedBody("", `""`), false},
		{"token array is one input", embedBody("", `[1,2,3]`), true},
		{"dims omitted", embedBody("", `"x"`), true},
		{"dims 0 explicit", embedBody(`,"dimensions":0`, `"x"`), false},
		{"dims 1", embedBody(`,"dimensions":1`, `"x"`), true},
		{"dims 65536", embedBody(`,"dimensions":65536`, `"x"`), true},
		{"dims 65537", embedBody(`,"dimensions":65537`, `"x"`), false},
		{"dims negative", embedBody(`,"dimensions":-4`, `"x"`), false},
		{"format float", embedBody(`,"encoding_format":"float"`, `"x"`), true},
		{"format base64", embedBody(`,"encoding_format":"base64"`, `"x"`), true},
		{"format other", embedBody(`,"encoding_format":"hex"`, `"x"`), false},
		{"input_type only", embedBody(`,"input_type":"query"`, `"x"`), true},
		{"task only", embedBody(`,"task":"retrieval"`, `"x"`), true},
		{"input_type and task", embedBody(`,"input_type":"query","task":"retrieval"`, `"x"`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newJSONCtx("POST", "/v1/embeddings", tc.body)
			_, err := GetAndValidateEmbeddingRequest(c, 3 /*RelayModeEmbeddings*/)
			if tc.valid && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.valid {
				if err == nil {
					t.Fatal("want rejection")
				}
				var apiErr *types.NewAPIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
					t.Fatalf("want 400 NewAPIError, got %T %v", err, err)
				}
			}
		})
	}
}

// Moderations reuse the embedding DTO but have no 2048 batch ceiling.
func TestEmbeddingLimitsDoNotApplyToModerations(t *testing.T) {
	c, _ := newJSONCtx("POST", "/v1/moderations", embedBody("", quotedList(2049, `"x"`)))
	if _, err := GetAndValidateEmbeddingRequest(c, 4 /*RelayModeModerations*/); err != nil {
		t.Fatalf("moderations must not be capped: %v", err)
	}
}

func TestCheckEmbeddingUnsupportedParams(t *testing.T) {
	c, _ := newJSONCtx("POST", "/v1/embeddings", embedBody(`,"input_type":"query"`, `"x"`))
	req, err := GetAndValidateEmbeddingRequest(c, 3)
	if err != nil {
		t.Fatal(err)
	}
	if e := CheckEmbeddingUnsupportedParams(req, constant.ChannelTypeVoyage); e != nil {
		t.Errorf("voyage forwards input_type: %v", e)
	}
	e := CheckEmbeddingUnsupportedParams(req, constant.ChannelTypeOpenAI)
	if e == nil || e.GetErrorCode() != types.ErrorCodeUnsupportedParameter || e.StatusCode != http.StatusBadRequest {
		t.Errorf("openai channel must reject input_type with unsupported_parameter: %v", e)
	}
	req.InputType, req.Task = "", "retrieval"
	if e := CheckEmbeddingUnsupportedParams(req, constant.ChannelTypeJina); e != nil {
		t.Errorf("jina forwards task: %v", e)
	}
	if e := CheckEmbeddingUnsupportedParams(req, constant.ChannelTypeOpenAI); e == nil {
		t.Error("openai channel must reject task")
	}
	req.Task = ""
	if e := CheckEmbeddingUnsupportedParams(req, constant.ChannelTypeOpenAI); e != nil {
		t.Errorf("no hints, nothing to reject: %v", e)
	}
}

func TestRerankContract(t *testing.T) {
	docs := func(n int) string { return quotedList(n, `"d"`) }
	body := func(d string, extra string) string {
		return fmt.Sprintf(`{"model":"model-a","query":"q","documents":%s%s}`, d, extra)
	}
	cases := []struct {
		name  string
		body  string
		valid bool
	}{
		{"2047 docs", body(docs(2047), ""), true},
		{"2048 docs", body(docs(2048), ""), true},
		{"2049 docs", body(docs(2049), ""), false},
		{"no docs", body(`[]`, ""), false},
		{"empty doc", body(`["a",""]`, ""), false},
		{"null doc", body(`["a",null]`, ""), false},
		{"object doc ok", body(`[{"text":"a"}]`, ""), true},
		{"top_n equals len", body(docs(3), `,"top_n":3`), true},
		{"top_n over len", body(docs(3), `,"top_n":4`), false},
		{"top_n negative", body(docs(3), `,"top_n":-1`), false},
		{"top_n omitted", body(docs(3), ""), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newJSONCtx("POST", "/v1/rerank", tc.body)
			_, err := GetAndValidateRerankRequest(c)
			if tc.valid != (err == nil) {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
