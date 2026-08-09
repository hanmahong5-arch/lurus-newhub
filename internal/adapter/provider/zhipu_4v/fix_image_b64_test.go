package zhipu_4v

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/gin-gonic/gin"
)

// fixImageB64Response builds an upstream image response with the given raw JSON body.
func fixImageB64Response(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// fixImageB64Payload is the client-facing OpenAI-shaped payload.
type fixImageB64Payload struct {
	Created int64 `json:"created"`
	Data    []struct {
		B64Json string `json:"b64_json"`
	} `json:"data"`
}

func fixImageB64Run(t *testing.T, body string) fixImageB64Payload {
	t.Helper()
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	info := &relaycommon.RelayInfo{StartTime: time.Now()}

	upstream := fixImageB64Response(body)
	defer func() { _ = upstream.Body.Close() }()

	usage, apiErr := zhipu4vImageHandler(c, upstream, info)
	if apiErr != nil {
		t.Fatalf("zhipu4vImageHandler returned an error: %v", apiErr)
	}
	if usage == nil {
		t.Fatal("zhipu4vImageHandler returned nil usage")
	}

	var payload fixImageB64Payload
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode client payload: %v (body=%s)", err, w.Body.String())
	}
	return payload
}

// TestFixZhipuImage_InlineB64WithoutUrlIsForwarded covers a response whose data
// entries carry the image bytes inline and have no url at all. Those bytes are
// usable as-is; dropping them would bill the tenant for a generation and hand
// back an empty data array.
func TestFixZhipuImage_InlineB64WithoutUrlIsForwarded(t *testing.T) {
	payload := fixImageB64Run(t, `{"created":1700000000,"data":[{"b64_json":"QUJD"},{"b64_image":"REVG"}]}`)

	if len(payload.Data) != 2 {
		t.Fatalf("client data length = %d, want 2 (entries with inline base64 must not be dropped for having no url)", len(payload.Data))
	}
	if payload.Data[0].B64Json != "QUJD" {
		t.Errorf("data[0].b64_json = %q, want %q", payload.Data[0].B64Json, "QUJD")
	}
	if payload.Data[1].B64Json != "REVG" {
		t.Errorf("data[1].b64_json = %q, want %q", payload.Data[1].B64Json, "REVG")
	}
	if payload.Created != 1700000000 {
		t.Errorf("created = %d, want 1700000000", payload.Created)
	}
}

// TestFixZhipuImage_EntryWithNeitherUrlNorB64IsSkipped keeps the skip path
// intact: an entry with no url and no inline bytes is still unusable.
func TestFixZhipuImage_EntryWithNeitherUrlNorB64IsSkipped(t *testing.T) {
	payload := fixImageB64Run(t, `{"created":1700000000,"data":[{},{"b64_json":"QUJD"}]}`)

	if len(payload.Data) != 1 {
		t.Fatalf("client data length = %d, want 1 (the empty entry must still be skipped)", len(payload.Data))
	}
	if payload.Data[0].B64Json != "QUJD" {
		t.Errorf("data[0].b64_json = %q, want %q", payload.Data[0].B64Json, "QUJD")
	}
}
