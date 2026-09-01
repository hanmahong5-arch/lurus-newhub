package helper

// image_n_bound_test.go — `n` on an image request is a direct, client-supplied
// multiplier on the charge (dto/openai_image.go: ImagePriceRatio =
// sizeRatio * qualityRatio * float64(N)), so it needs an upper bound.
//
// N is `uint`, so the negative-n-becomes-a-credit failure upstream New API hit
// (d0bd8aac7) cannot happen here — but nothing capped the magnitude, and a
// large enough n drives the float64 product past what an int64 quota can
// represent. Both intake shapes must be bounded: the multipart form path and
// the JSON body path.
//
// Mutation oracle: delete the dto.MaxImageN check in GetAndValidOpenAIImageRequest
// and both rejection cases below go red while the accepted case stays green.

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func imageCtxJSON(body string) *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c
}

func TestGetAndValidOpenAIImageRequest_RejectsUnboundedN_JSON(t *testing.T) {
	body := fmt.Sprintf(`{"model":"dall-e-3","prompt":"x","n":%d}`, dto.MaxImageN+1)
	_, err := GetAndValidOpenAIImageRequest(imageCtxJSON(body), 0 /*RelayModeImagesGenerations*/)
	if err == nil {
		t.Fatalf("n=%d was accepted; an unbounded client-supplied n multiplies the charge without limit",
			dto.MaxImageN+1)
	}
	if !strings.Contains(err.Error(), "n must be an integer between 1 and") {
		t.Errorf("error = %q, want the explicit bound message so the caller can fix the request", err)
	}
}

func TestGetAndValidOpenAIImageRequest_RejectsUnboundedN_Multipart(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "gpt-image-1")
	_ = mw.WriteField("prompt", "x")
	_ = mw.WriteField("n", fmt.Sprintf("%d", dto.MaxImageN+1))
	_ = mw.WriteField("image", "base64data")
	_ = mw.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/v1/images/edits", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.Request = req

	if _, err := GetAndValidOpenAIImageRequest(c, 6 /*RelayModeImagesEdits*/); err == nil {
		t.Fatal("multipart n above the bound was accepted — the form path bypassed the JSON-side check")
	}
}

// TestGetAndValidOpenAIImageRequest_AcceptsNAtBound is the positive control: the
// bound must not reject legitimate batch sizes.
func TestGetAndValidOpenAIImageRequest_AcceptsNAtBound(t *testing.T) {
	body := fmt.Sprintf(`{"model":"dall-e-3","prompt":"x","n":%d}`, dto.MaxImageN)
	got, err := GetAndValidOpenAIImageRequest(imageCtxJSON(body), 0)
	if err != nil {
		t.Fatalf("n=%d (exactly the bound) was rejected: %v", dto.MaxImageN, err)
	}
	if got.N != dto.MaxImageN {
		t.Errorf("N = %d, want %d", got.N, dto.MaxImageN)
	}
}
