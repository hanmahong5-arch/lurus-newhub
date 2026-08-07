package vertex

import (
	"net/http/httptest"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/gin-gonic/gin"
)

// TestFixVertexRerank_ConvertRerankRequestFailsFast pins ConvertRerankRequest to
// the same contract as the other unsupported converters in this adaptor: an
// explicit error. A (nil, nil) result is marshalled by the rerank relay path
// into the literal body "null" and posted upstream, turning an unsupported
// request into a late, opaque upstream failure.
func TestFixVertexRerank_ConvertRerankRequestFailsFast(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	a := &Adaptor{}
	got, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{
		Model:     "rerank-model",
		Query:     "query",
		Documents: []any{"doc"},
	})

	if err == nil {
		t.Fatal("ConvertRerankRequest returned a nil error; rerank is not implemented for this adaptor and must be rejected here")
	}
	if got != nil {
		t.Errorf("converted request = %v, want nil", got)
	}
}

// TestFixVertexRerank_UnsupportedConvertersAgree keeps the rerank stub aligned
// with its siblings so the fix cannot silently drift back.
func TestFixVertexRerank_UnsupportedConvertersAgree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	a := &Adaptor{}
	if _, err := a.ConvertEmbeddingRequest(c, nil, dto.EmbeddingRequest{}); err == nil {
		t.Fatal("ConvertEmbeddingRequest: expected an error")
	}
	if _, err := a.ConvertRerankRequest(c, 0, dto.RerankRequest{}); err == nil {
		t.Fatal("ConvertRerankRequest: expected an error")
	}
}
