package volcengine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// parseVolcengineAuth: appid|token splitting — the key that unlocks every
// downstream TTS call.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_ParseVolcengineAuth(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		wantAppID string
		wantToken string
		wantErr   bool
	}{
		{"valid two-part key", "app123|token456", "app123", "token456", false},
		{"missing pipe", "app123token456", "", "", true},
		{"empty string", "", "", "", true},
		{"too many pipes", "a|b|c", "", "", true},
		{"empty token half", "app123|", "app123", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appID, token, err := parseVolcengineAuth(tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if appID != tt.wantAppID || token != tt.wantToken {
				t.Errorf("parseVolcengineAuth(%q) = (%q, %q), want (%q, %q)", tt.key, appID, token, tt.wantAppID, tt.wantToken)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mapVoiceType / mapEncoding / getContentTypeByEncoding: vendor-format
// lookup tables. Wrong mapping = wrong voice/audio format silently sent.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_MapVoiceType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"alloy", "zh_male_M392_conversation_wvae_bigtts"},
		{"nova", "zh_female_shuangkuaisisi_mars_bigtts"},
		{"unknown-voice-xyz", "unknown-voice-xyz"}, // unmapped voices pass through verbatim
		{"", ""},
	}
	for _, tt := range tests {
		if got := mapVoiceType(tt.in); got != tt.want {
			t.Errorf("mapVoiceType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestProvOllamaVolc_MapEncoding(t *testing.T) {
	tests := []struct{ in, want string }{
		{"mp3", "mp3"},
		{"opus", "ogg_opus"},
		{"aac", "mp3"},  // aac has no Volcengine equivalent -> falls back to mp3
		{"flac", "mp3"}, // same
		{"wav", "wav"},
		{"pcm", "pcm"},
		{"unknown-format", "mp3"}, // default fallback
		{"", "mp3"},
	}
	for _, tt := range tests {
		if got := mapEncoding(tt.in); got != tt.want {
			t.Errorf("mapEncoding(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestProvOllamaVolc_GetContentTypeByEncoding(t *testing.T) {
	tests := []struct{ in, want string }{
		{"mp3", "audio/mpeg"},
		{"ogg_opus", "audio/ogg"},
		{"wav", "audio/wav"},
		{"pcm", "audio/pcm"},
		{"totally-unknown", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := getContentTypeByEncoding(tt.in); got != tt.want {
			t.Errorf("getContentTypeByEncoding(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestProvOllamaVolc_GenerateRequestID_NonEmptyAndUnique(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()
	if id1 == "" || id2 == "" {
		t.Fatal("generateRequestID() must not return an empty string")
	}
	if id1 == id2 {
		t.Error("generateRequestID() should generate distinct IDs across calls")
	}
}

// ---------------------------------------------------------------------------
// Adaptor.ConvertAudioRequest: OpenAI TTS request -> Volcengine TTS request.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_ConvertAudioRequest_UnsupportedRelayMode(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	info := &relaycommon.RelayInfo{RelayMode: 0} // not RelayModeAudioSpeech
	_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{})
	if err == nil {
		t.Fatal("expected error for a non-speech relay mode")
	}
}

func TestProvOllamaVolc_ConvertAudioRequest_InvalidApiKeyFormat(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeAudioSpeech,
		ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "no-pipe"},
	}
	_, err := a.ConvertAudioRequest(c, info, dto.AudioRequest{Voice: "alloy", Input: "hi"})
	if err == nil {
		t.Fatal("expected error for a malformed appid|token API key")
	}
}

func TestProvOllamaVolc_ConvertAudioRequest_MapsVoiceAndEncodingAndSetsStreamForSubmit(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	info := &relaycommon.RelayInfo{
		RelayMode:       constant.RelayModeAudioSpeech,
		OriginModelName: "doubao-tts",
		ChannelMeta:     &relaycommon.ChannelMeta{ApiKey: "app1|token1"},
	}
	req := dto.AudioRequest{Voice: "nova", Input: "hello world", ResponseFormat: "opus", Speed: 1.5}
	out, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.IsStream {
		t.Error("IsStream should be forced true for operation=submit (the default TTS operation)")
	}
	stored, exists := c.Get(contextKeyTTSRequest)
	if !exists {
		t.Fatal("expected the constructed VolcengineTTSRequest to be stashed in gin context")
	}
	volcReq := stored.(VolcengineTTSRequest)
	if volcReq.Audio.VoiceType != "zh_female_shuangkuaisisi_mars_bigtts" {
		t.Errorf("VoiceType = %q, want the mapped nova voice", volcReq.Audio.VoiceType)
	}
	if volcReq.Audio.Encoding != "ogg_opus" {
		t.Errorf("Encoding = %q, want ogg_opus (mapped from response_format=opus)", volcReq.Audio.Encoding)
	}
	if volcReq.Audio.SpeedRatio != 1.5 {
		t.Errorf("SpeedRatio = %v, want 1.5", volcReq.Audio.SpeedRatio)
	}
	if volcReq.App.AppID != "app1" || volcReq.App.Token != "token1" {
		t.Errorf("App = %+v, want AppID=app1 Token=token1", volcReq.App)
	}
	if volcReq.Request.Text != "hello world" {
		t.Errorf("Request.Text = %q, want %q", volcReq.Request.Text, "hello world")
	}
	if c.GetString(contextKeyResponseFormat) != "ogg_opus" {
		t.Errorf("context response format = %q, want %q", c.GetString(contextKeyResponseFormat), "ogg_opus")
	}
	// the returned reader must contain valid JSON matching the same request.
	buf, _ := io.ReadAll(out)
	if !strings.Contains(string(buf), `"voice_type":"zh_female_shuangkuaisisi_mars_bigtts"`) {
		t.Errorf("returned request body = %s, want it to contain the mapped voice type", buf)
	}
}

func TestProvOllamaVolc_ConvertAudioRequest_MetadataOverridesBaseRequest(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app1|token1"}}
	req := dto.AudioRequest{
		Voice:    "alloy",
		Input:    "hi",
		Metadata: []byte(`{"request":{"operation":"query","reqid":"custom-req-id"}}`),
	}
	out, err := a.ConvertAudioRequest(c, info, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// operation != "submit" -> IsStream must NOT be forced true.
	if info.IsStream {
		t.Error("IsStream should stay false when metadata overrides operation away from 'submit'")
	}
	buf, _ := io.ReadAll(out)
	if !strings.Contains(string(buf), `"reqid":"custom-req-id"`) {
		t.Errorf("metadata should override the generated reqid, got %s", buf)
	}
	if !strings.Contains(string(buf), `"operation":"query"`) {
		t.Errorf("metadata should override the default 'submit' operation, got %s", buf)
	}
}

func TestProvOllamaVolc_ConvertAudioRequest_MalformedMetadataErrors(t *testing.T) {
	a := &Adaptor{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	info := &relaycommon.RelayInfo{RelayMode: constant.RelayModeAudioSpeech, ChannelMeta: &relaycommon.ChannelMeta{ApiKey: "app1|token1"}}
	req := dto.AudioRequest{Voice: "alloy", Input: "hi", Metadata: []byte(`{not-json`)}
	_, err := a.ConvertAudioRequest(c, info, req)
	if err == nil {
		t.Fatal("expected error for malformed metadata JSON")
	}
}

// ---------------------------------------------------------------------------
// handleTTSResponse: non-streaming TTS response handling — usage extraction
// and base64 audio decode are both billing/correctness-critical.
// ---------------------------------------------------------------------------

func TestProvOllamaVolc_HandleTTSResponse_Success(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	// base64("hello-audio-bytes")
	audioB64 := "aGVsbG8tYXVkaW8tYnl0ZXM="
	body := `{"reqid":"r1","code":3000,"message":"OK","sequence":-1,"data":"` + audioB64 + `"}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{}
	info.SetEstimatePromptTokens(9)

	usage, apiErr := handleTTSResponse(c, resp, info, "mp3")
	if apiErr != nil {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
	u := usage.(*dto.Usage)
	if u.PromptTokens != 9 || u.TotalTokens != 9 {
		t.Errorf("usage = %+v, want PromptTokens=TotalTokens=9 (estimated, TTS has no completion tokens)", u)
	}
	if w.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", w.Header().Get("Content-Type"))
	}
	if w.Body.String() != "hello-audio-bytes" {
		t.Errorf("body = %q, want the decoded audio bytes %q", w.Body.String(), "hello-audio-bytes")
	}
}

func TestProvOllamaVolc_HandleTTSResponse_NonSuccessCode_ReturnsUpstreamMessage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	body := `{"reqid":"r1","code":3001,"message":"invalid voice_type"}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{}

	usage, apiErr := handleTTSResponse(c, resp, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected error for a non-3000 upstream code")
	}
	if !strings.Contains(apiErr.Error(), "invalid voice_type") {
		t.Errorf("error = %v, want it to surface the upstream message", apiErr)
	}
	if usage != nil {
		t.Errorf("usage = %v, want nil on error", usage)
	}
}

func TestProvOllamaVolc_HandleTTSResponse_MalformedBase64Data_ReturnsError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	body := `{"reqid":"r1","code":3000,"message":"OK","data":"not-valid-base64!!!"}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	info := &relaycommon.RelayInfo{}

	_, apiErr := handleTTSResponse(c, resp, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected error for base64-undecodable data")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestProvOllamaVolc_HandleTTSResponse_MalformedJSON_ReturnsError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{not-json`))}
	info := &relaycommon.RelayInfo{}

	_, apiErr := handleTTSResponse(c, resp, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestProvOllamaVolc_HandleTTSResponse_BodyReadFailure(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)

	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(errReaderVolc{})}
	info := &relaycommon.RelayInfo{}

	_, apiErr := handleTTSResponse(c, resp, info, "mp3")
	if apiErr == nil {
		t.Fatal("expected error when the response body fails to read")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeReadResponseBodyFailed {
		t.Errorf("error code = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeReadResponseBodyFailed)
	}
}

type errReaderVolc struct{}

func (errReaderVolc) Read(p []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
