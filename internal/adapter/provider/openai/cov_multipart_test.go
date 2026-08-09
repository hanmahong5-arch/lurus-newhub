package openai

// Business-acceptance tests for the multipart request builders: audio
// (transcription/translation/TTS) and image (generations passthrough vs.
// edits multipart rebuild). These functions decide exactly which bytes get
// forwarded to the upstream vendor for file uploads, so silent field drops
// or MIME mis-detection here mean corrupted uploads reaching production.

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

func newMultipartCtx(t *testing.T, fields map[string]string, files map[string][]byte) *gin.Context {
	t.Helper()
	origMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = origMax })
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%s): %v", k, err)
		}
	}
	for fieldName, content := range files {
		fw, err := mw.CreateFormFile(fieldName, fieldName+".png")
		if err != nil {
			t.Fatalf("CreateFormFile(%s): %v", fieldName, err)
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
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	return c
}

// ---------------------------------------------------------------------------
// ConvertAudioRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertAudioRequest_Speech(t *testing.T) {
	a := &Adaptor{}
	c := newCovTestContext()
	req := dto.AudioRequest{Model: "tts-1", Input: "hello", Voice: "alloy", ResponseFormat: "mp3"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioSpeech}

	r, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.ResponseFormat != "mp3" {
		t.Errorf("a.ResponseFormat = %q, want mp3 (side effect for TTS handler)", a.ResponseFormat)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var decoded dto.AudioRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("result is not valid JSON of the request: %v (%s)", err, raw)
	}
	if decoded.Model != "tts-1" || decoded.Voice != "alloy" {
		t.Errorf("decoded = %+v, want model=tts-1 voice=alloy", decoded)
	}
}

func TestAdaptor_ConvertAudioRequest_Transcription(t *testing.T) {
	a := &Adaptor{}
	c := newMultipartCtx(t, map[string]string{"language": "en", "model": "whisper-1-ignored-field"}, map[string][]byte{"file": []byte("fake-audio-bytes")})
	req := dto.AudioRequest{Model: "whisper-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioTranscription}

	r, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	body := string(buf)

	// model must come from the *request* (dto.AudioRequest.Model), not the
	// raw form field of the same name -- proves writer.WriteField("model", request.Model)
	// wins over blindly forwarding formData.Value["model"].
	if strings.Count(body, `name="model"`) != 1 {
		t.Errorf("expected exactly one model field in rebuilt multipart body, got body=%q", body)
	}
	if !strings.Contains(body, "whisper-1") {
		t.Errorf("rebuilt body missing model value whisper-1: %q", body)
	}
	if strings.Contains(body, "whisper-1-ignored-field") {
		t.Errorf("rebuilt body should not carry the original form's model field verbatim: %q", body)
	}
	if !strings.Contains(body, `name="language"`) || !strings.Contains(body, "en") {
		t.Errorf("rebuilt body missing forwarded language field: %q", body)
	}
	if !strings.Contains(body, "fake-audio-bytes") {
		t.Errorf("rebuilt body missing file content: %q", body)
	}
	ct := c.Request.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data") {
		t.Errorf("Content-Type header = %q, want multipart/form-data prefix (mutated for downstream request)", ct)
	}
}

func TestAdaptor_ConvertAudioRequest_MissingFile(t *testing.T) {
	a := &Adaptor{}
	c := newMultipartCtx(t, map[string]string{"language": "en"}, nil)
	req := dto.AudioRequest{Model: "whisper-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioTranslation}

	_, err := a.ConvertAudioRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error when file field missing")
	}
	if !strings.Contains(err.Error(), "file is required") {
		t.Errorf("error = %q, want mention of 'file is required'", err.Error())
	}
}

func TestAdaptor_ConvertAudioRequest_MalformedMultipart(t *testing.T) {
	a := &Adaptor{}
	origMax := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 10
	t.Cleanup(func() { constant.MaxRequestBodyMB = origMax })
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader("not-a-real-multipart-body"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
	req := dto.AudioRequest{Model: "whisper-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeAudioTranscription}

	_, err := a.ConvertAudioRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error parsing malformed multipart body")
	}
	if !strings.Contains(err.Error(), "error parsing multipart form") {
		t.Errorf("error = %q, want wrapped 'error parsing multipart form'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// ConvertImageRequest
// ---------------------------------------------------------------------------

func TestAdaptor_ConvertImageRequest_GenerationsPassthrough(t *testing.T) {
	a := &Adaptor{}
	c := newCovTestContext()
	req := dto.ImageRequest{Model: "dall-e-3", Prompt: "a cat", N: 1}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}

	result, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r, ok := result.(dto.ImageRequest)
	if !ok {
		t.Fatalf("result type = %T, want dto.ImageRequest (passthrough)", result)
	}
	if r.Prompt != "a cat" || r.N != 1 {
		t.Errorf("passthrough mutated request: %+v", r)
	}
}

func TestAdaptor_ConvertImageRequest_Edits_StandardImageField(t *testing.T) {
	a := &Adaptor{}
	c := newMultipartCtx(t, map[string]string{"prompt": "add a hat"}, map[string][]byte{"image": []byte("fake-png-bytes")})
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
	if !strings.Contains(body, `name="image"`) {
		t.Errorf("rebuilt body missing image field: %q", body)
	}
	if !strings.Contains(body, "fake-png-bytes") {
		t.Errorf("rebuilt body missing image content: %q", body)
	}
	if !strings.Contains(body, `name="prompt"`) || !strings.Contains(body, "add a hat") {
		t.Errorf("rebuilt body missing forwarded prompt field: %q", body)
	}
}

func TestAdaptor_ConvertImageRequest_Edits_ImageArrayBracketField(t *testing.T) {
	a := &Adaptor{}
	c := newMultipartCtx(t, nil, map[string][]byte{"image[]": []byte("array-notation-bytes")})
	req := dto.ImageRequest{Model: "gpt-image-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}

	result, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf := result.(*bytes.Buffer)
	if !strings.Contains(buf.String(), "array-notation-bytes") {
		t.Errorf("rebuilt body missing image[] content: %q", buf.String())
	}
}

func TestAdaptor_ConvertImageRequest_Edits_MaskField(t *testing.T) {
	a := &Adaptor{}
	c := newMultipartCtx(t, nil, map[string][]byte{"image": []byte("base-image"), "mask": []byte("mask-bytes")})
	req := dto.ImageRequest{Model: "gpt-image-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}

	result, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf := result.(*bytes.Buffer)
	body := buf.String()
	if !strings.Contains(body, `name="mask"`) {
		t.Errorf("rebuilt body missing mask field: %q", body)
	}
	if !strings.Contains(body, "mask-bytes") {
		t.Errorf("rebuilt body missing mask content: %q", body)
	}
}

func TestAdaptor_ConvertImageRequest_Edits_MissingImage(t *testing.T) {
	a := &Adaptor{}
	c := newMultipartCtx(t, map[string]string{"prompt": "no image here"}, nil)
	req := dto.ImageRequest{Model: "gpt-image-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}

	_, err := a.ConvertImageRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error when no image field present")
	}
	if !strings.Contains(err.Error(), "image is required") {
		t.Errorf("error = %q, want mention of 'image is required'", err.Error())
	}
}

func TestAdaptor_ConvertImageRequest_Edits_ParseFailure(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader("garbage"))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
	req := dto.ImageRequest{Model: "gpt-image-1"}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}

	_, err := a.ConvertImageRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error parsing malformed multipart body")
	}
}
