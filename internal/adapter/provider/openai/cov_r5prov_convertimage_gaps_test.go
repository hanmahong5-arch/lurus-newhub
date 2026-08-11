package openai

// Business-acceptance tests for ConvertImageRequest branches not covered by
// the existing cov_multipart suite: multiple files under the plain "image"
// field name (must be re-emitted under "image[]" so the upstream treats them
// as an array, not overwrite a single field), and the generic
// "image[<index>]"-prefixed field-name fallback used by some SDKs/clients
// that don't use the literal "image[]" bracket-only name.

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// r5provMultiFileMultipartCtx builds a multipart request where a single
// field name ("image") carries more than one file part -- something the
// map[string][]byte shape of the existing newMultipartCtx helper can't
// express (one entry per field name).
func r5provMultiFileMultipartCtx(t *testing.T, fieldName string, files [][]byte) *gin.Context {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for i, content := range files {
		fw, err := mw.CreateFormFile(fieldName, "img"+string(rune('0'+i))+".png")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("write file content: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	return c
}

func TestR5OpenAI_ConvertImageRequest_Edits_MultipleImagesUnderPlainField_UsesArrayFieldName(t *testing.T) {
	a := &Adaptor{}
	c := r5provMultiFileMultipartCtx(t, "image", [][]byte{[]byte("first-image-bytes"), []byte("second-image-bytes")})
	req := dto.ImageRequest{Model: "gpt-image-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}

	result, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf, ok := result.(*bytes.Buffer)
	if !ok {
		t.Fatalf("result type = %T, want *bytes.Buffer", result)
	}
	body := buf.String()
	// When there is more than one image file, the rebuilt form must use the
	// array field name "image[]" (not "image") so the upstream doesn't
	// silently drop all but the last file.
	if !strings.Contains(body, `name="image[]"`) {
		t.Errorf("rebuilt body missing image[] field name for multi-image upload: %q", body)
	}
	if strings.Count(body, `name="image[]"`) != 2 {
		t.Errorf("expected 2 image[] parts in rebuilt body, got body=%q", body)
	}
	if !strings.Contains(body, "first-image-bytes") || !strings.Contains(body, "second-image-bytes") {
		t.Errorf("rebuilt body missing one of the image contents: %q", body)
	}
}

func TestR5OpenAI_ConvertImageRequest_Edits_GenericIndexedImageField_Discovered(t *testing.T) {
	a := &Adaptor{}
	c := r5provMultiFileMultipartCtx(t, "image[0]", [][]byte{[]byte("indexed-field-bytes")})
	req := dto.ImageRequest{Model: "gpt-image-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}

	result, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf, ok := result.(*bytes.Buffer)
	if !ok {
		t.Fatalf("result type = %T, want *bytes.Buffer", result)
	}
	body := buf.String()
	// Neither literal "image" nor "image[]" was present -- only the generic
	// "image[0]"-style field. The handler must still discover it via the
	// prefix scan and forward the content (as a single file, under "image").
	if !strings.Contains(body, "indexed-field-bytes") {
		t.Errorf("rebuilt body missing content sourced from the generic image[N] field: %q", body)
	}
	if !strings.Contains(body, `name="image"`) {
		t.Errorf("single discovered image should be re-emitted under name=image: %q", body)
	}
}
