package mokaai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"

	"github.com/gin-gonic/gin"
)

// r6a_brokenReader simulates a network failure mid-read of the upstream
// embedding response body, distinct from a malformed-JSON body (which fails
// at json.Unmarshal, not io.ReadAll).
type r6a_brokenReader struct{}

func (r6a_brokenReader) Read(p []byte) (int, error) {
	return 0, errors.New("r6a: simulated connection reset")
}

func (r6a_brokenReader) Close() error { return nil }

func r6a_newMokaCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)
	return c, w
}

// mokaEmbeddingHandler feeds dto.Usage used for billing; a body read failure
// must be classified as an error and must not panic or fabricate usage.
func TestR6AMoka_EmbeddingHandler_BodyReadFailure_ReturnsErrorNotPanic(t *testing.T) {
	c, _ := r6a_newMokaCtx()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	resp := &http.Response{StatusCode: 200, Body: r6a_brokenReader{}, Header: make(http.Header)}

	usage, apiErr := mokaEmbeddingHandler(c, info, resp)

	if apiErr == nil {
		t.Fatal("expected a classified error when the upstream body read fails")
	}
	if usage != nil {
		t.Errorf("usage should be nil on a read failure, got %+v", usage)
	}
}
