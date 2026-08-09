package gemini

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ---------------------------------------------------------------------------
// mapVoiceToGemini
// ---------------------------------------------------------------------------

func TestMapVoiceToGemini(t *testing.T) {
	tests := []struct {
		voice string
		want  string
	}{
		{"alloy", "Kore"},
		{"echo", "Charon"},
		{"fable", "Fenrir"},
		{"onyx", "Orus"},
		{"nova", "Aoede"},
		{"shimmer", "Zephyr"},
		{"AlreadyGeminiVoiceName", "AlreadyGeminiVoiceName"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.voice, func(t *testing.T) {
			got := mapVoiceToGemini(tt.voice)
			if got != tt.want {
				t.Errorf("mapVoiceToGemini(%q) = %q, want %q", tt.voice, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isMostlyCJK
// ---------------------------------------------------------------------------

func TestIsMostlyCJK(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"pure CJK", "你好世界这是一段中文文本用来测试", true},
		{"pure ASCII", "hello this is english text", false},
		{"empty string", "", false},
		{"whitespace only", "   \t\n", false},
		{"mixed mostly CJK (>60%)", "你好世界hi", true},
		{"mixed mostly ASCII (<60% CJK)", "hello world 你", false},
		{"exactly boundary-ish CJK heavy", "中中中中中abc", true}, // 5/8 = 62.5% > 60%
		{"japanese kana", "こんにちは", true},
		{"fullwidth punctuation counted as CJK", "你好，世界！", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMostlyCJK(tt.text)
			if got != tt.want {
				t.Errorf("isMostlyCJK(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConvertAudioRequestToGemini
// ---------------------------------------------------------------------------

func TestConvertAudioRequestToGemini_BasicRequest(t *testing.T) {
	reader, err := ConvertAudioRequestToGemini(dto.AudioRequest{Input: "Hello world", Voice: "alloy"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("invalid JSON body: %v (%s)", err, body)
	}
	contents := req["contents"].([]interface{})
	part := contents[0].(map[string]interface{})["parts"].([]interface{})[0].(map[string]interface{})
	if part["text"] != "Hello world" {
		t.Errorf("text = %v, want unchanged 'Hello world' (not mostly CJK)", part["text"])
	}
	genConfig := req["generationConfig"].(map[string]interface{})
	modalities := genConfig["responseModalities"].([]interface{})
	if len(modalities) != 1 || modalities[0] != "AUDIO" {
		t.Errorf("responseModalities = %v, want [AUDIO]", modalities)
	}
	speechConfig := genConfig["speechConfig"].(map[string]interface{})
	voiceConfig := speechConfig["voiceConfig"].(map[string]interface{})
	prebuilt := voiceConfig["prebuiltVoiceConfig"].(map[string]interface{})
	if prebuilt["voiceName"] != "Kore" {
		t.Errorf("voiceName = %v, want Kore (mapped from alloy)", prebuilt["voiceName"])
	}
}

func TestConvertAudioRequestToGemini_CJKTextGetsAsciiPrefix(t *testing.T) {
	reader, err := ConvertAudioRequestToGemini(dto.AudioRequest{Input: "你好世界，欢迎使用。", Voice: "nova"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, _ := io.ReadAll(reader)
	var req map[string]interface{}
	_ = json.Unmarshal(body, &req)
	contents := req["contents"].([]interface{})
	part := contents[0].(map[string]interface{})["parts"].([]interface{})[0].(map[string]interface{})
	text := part["text"].(string)
	if !strings.HasPrefix(text, "Hello. ") {
		t.Errorf("text = %q, want 'Hello. ' prefix prepended for mostly-CJK input", text)
	}
	if !strings.Contains(text, "你好世界") {
		t.Errorf("text = %q, want original CJK content preserved", text)
	}
}

func TestConvertAudioRequestToGemini_UnknownVoicePassthrough(t *testing.T) {
	reader, err := ConvertAudioRequestToGemini(dto.AudioRequest{Input: "hi", Voice: "Puck"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, _ := io.ReadAll(reader)
	if !strings.Contains(string(body), `"voiceName":"Puck"`) {
		t.Errorf("body = %s, want voiceName Puck passed through unchanged", body)
	}
}

// ---------------------------------------------------------------------------
// GeminiTTSHandler
// ---------------------------------------------------------------------------

func TestGeminiTTSHandler_Success(t *testing.T) {
	c, w := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	audioB64 := "aGVsbG8=" // base64("hello")
	body := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"` + audioB64 + `"}}]},"finishReason":"STOP"}]}`
	bcResp6 := respFromBody(200, body)
	defer func() {
		if bcResp6 != nil {
			_ = bcResp6.Body.Close()
		}
	}()
	usage, apiErr := GeminiTTSHandler(c, info, bcResp6)
	if apiErr != nil {
		t.Fatalf("unexpected error: %v", apiErr)
	}
	u := usage.(*dto.Usage)
	if u == nil {
		t.Fatal("usage is nil")
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "hello" {
		t.Errorf("body = %q, want decoded audio bytes %q", w.Body.String(), "hello")
	}
	if w.Header().Get("Content-Type") != "audio/wav" {
		t.Errorf("Content-Type = %q, want audio/wav", w.Header().Get("Content-Type"))
	}
}

func TestGeminiTTSHandler_UpstreamNon200_Errors(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	bcResp5 := respFromBody(429, `rate limited`)
	defer func() {
		if bcResp5 != nil {
			_ = bcResp5.Body.Close()
		}
	}()
	_, apiErr := GeminiTTSHandler(c, info, bcResp5)
	if apiErr == nil {
		t.Fatal("expected error for non-200 upstream status")
	}
	if apiErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429 (propagated from upstream)", apiErr.StatusCode)
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseStatusCode {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseStatusCode)
	}
}

func TestGeminiTTSHandler_MalformedJSON_Errors(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	bcResp4 := respFromBody(200, `not json`)
	defer func() {
		if bcResp4 != nil {
			_ = bcResp4.Body.Close()
		}
	}()
	_, apiErr := GeminiTTSHandler(c, info, bcResp4)
	if apiErr == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if apiErr.GetErrorCode() != types.ErrorCodeBadResponseBody {
		t.Errorf("ErrorCode = %q, want %q", apiErr.GetErrorCode(), types.ErrorCodeBadResponseBody)
	}
}

func TestGeminiTTSHandler_UpstreamErrorField_Errors(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"error":{"message":"quota exceeded","code":403}}`
	bcResp3 := respFromBody(200, body)
	defer func() {
		if bcResp3 != nil {
			_ = bcResp3.Body.Close()
		}
	}()
	_, apiErr := GeminiTTSHandler(c, info, bcResp3)
	if apiErr == nil {
		t.Fatal("expected error when response body carries an 'error' field")
	}
	if !strings.Contains(apiErr.Error(), "quota exceeded") {
		t.Errorf("error = %q, want to mention upstream error message", apiErr.Error())
	}
}

func TestGeminiTTSHandler_NoAudioInResponse_Errors(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"candidates":[{"content":{"parts":[{"inlineData":null}]},"finishReason":"OTHER"}]}`
	bcResp2 := respFromBody(200, body)
	defer func() {
		if bcResp2 != nil {
			_ = bcResp2.Body.Close()
		}
	}()
	_, apiErr := GeminiTTSHandler(c, info, bcResp2)
	if apiErr == nil {
		t.Fatal("expected error when no audio data is present")
	}
	if !strings.Contains(apiErr.Error(), "no audio") {
		t.Errorf("error = %q, want 'no audio' message", apiErr.Error())
	}
}

func TestGeminiTTSHandler_MalformedInlineBase64_SkipsAndErrors(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	body := `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"audio/wav","data":"not-valid-base64!!!"}}]},"finishReason":"STOP"}]}`
	bcResp1 := respFromBody(200, body)
	defer func() {
		if bcResp1 != nil {
			_ = bcResp1.Body.Close()
		}
	}()
	_, apiErr := GeminiTTSHandler(c, info, bcResp1)
	if apiErr == nil {
		t.Fatal("expected error: malformed base64 audio data must be skipped, yielding 'no audio'")
	}
	if !strings.Contains(apiErr.Error(), "no audio") {
		t.Errorf("error = %q, want 'no audio' (decode failures are logged+skipped, not fatal by themselves)", apiErr.Error())
	}
}

func TestGeminiTTSHandler_EmptyCandidates_NoAudioError(t *testing.T) {
	c, _ := newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	bcResp0 := respFromBody(200, `{"candidates":[]}`)
	defer func() {
		if bcResp0 != nil {
			_ = bcResp0.Body.Close()
		}
	}()
	_, apiErr := GeminiTTSHandler(c, info, bcResp0)
	if apiErr == nil {
		t.Fatal("expected error for empty candidates")
	}
	if !strings.Contains(apiErr.Error(), "no audio") {
		t.Errorf("error = %q, want 'no audio'", apiErr.Error())
	}
}
