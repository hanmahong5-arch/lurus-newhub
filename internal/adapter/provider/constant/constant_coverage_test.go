package constant

import (
	"net/http"
	"testing"
)

func TestPath2RelayMode(t *testing.T) {
	cases := []struct {
		name string
		path string
		want int
	}{
		{"chat completions v1", "/v1/chat/completions", RelayModeChatCompletions},
		{"chat completions pg", "/pg/chat/completions", RelayModeChatCompletions},
		{"completions", "/v1/completions", RelayModeCompletions},
		{"embeddings v1 prefix", "/v1/embeddings", RelayModeEmbeddings},
		{"embeddings suffix", "/some/custom/embeddings", RelayModeEmbeddings},
		{"moderations", "/v1/moderations", RelayModeModerations},
		{"images generations", "/v1/images/generations", RelayModeImagesGenerations},
		{"images edits", "/v1/images/edits", RelayModeImagesEdits},
		{"edits", "/v1/edits", RelayModeEdits},
		{"responses", "/v1/responses", RelayModeResponses},
		{"audio speech", "/v1/audio/speech", RelayModeAudioSpeech},
		{"audio transcriptions", "/v1/audio/transcriptions", RelayModeAudioTranscription},
		{"audio translations", "/v1/audio/translations", RelayModeAudioTranslation},
		{"rerank", "/v1/rerank", RelayModeRerank},
		{"realtime", "/v1/realtime", RelayModeRealtime},
		{"gemini v1beta models", "/v1beta/models/gemini-pro", RelayModeGemini},
		{"gemini v1 models", "/v1/models", RelayModeGemini},
		{"midjourney delegated", "/mj/submit/imagine", RelayModeMidjourneyImagine},
		{"midjourney unknown suffix", "/mj/unknown-thing", RelayModeUnknown},
		{"unknown path", "/some/random/path", RelayModeUnknown},
		{"empty path", "", RelayModeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Path2RelayMode(c.path)
			if got != c.want {
				t.Errorf("Path2RelayMode(%q) = %d, want %d", c.path, got, c.want)
			}
		})
	}
}

func TestPath2RelayModeMidjourney(t *testing.T) {
	cases := []struct {
		name string
		path string
		want int
	}{
		{"action", "/mj/submit/action", RelayModeMidjourneyAction},
		{"modal", "/mj/submit/modal", RelayModeMidjourneyModal},
		{"shorten", "/mj/submit/shorten", RelayModeMidjourneyShorten},
		{"swap face", "/mj/insight-face/swap", RelayModeSwapFace},
		{"upload discord images", "/mj/submit/upload-discord-images", RelayModeMidjourneyUpload},
		{"imagine", "/mj/submit/imagine", RelayModeMidjourneyImagine},
		{"video", "/mj/submit/video", RelayModeMidjourneyVideo},
		{"edits", "/mj/submit/edits", RelayModeMidjourneyEdits},
		{"blend", "/mj/submit/blend", RelayModeMidjourneyBlend},
		{"describe", "/mj/submit/describe", RelayModeMidjourneyDescribe},
		{"notify", "/mj/notify", RelayModeMidjourneyNotify},
		{"change", "/mj/submit/change", RelayModeMidjourneyChange},
		{"simple-change", "/mj/submit/simple-change", RelayModeMidjourneyChange},
		{"fetch", "/mj/task/fetch", RelayModeMidjourneyTaskFetch},
		{"image-seed", "/mj/task/image-seed", RelayModeMidjourneyTaskImageSeed},
		{"list-by-condition", "/mj/task/list-by-condition", RelayModeMidjourneyTaskFetchByCondition},
		{"unknown", "/mj/whatever", RelayModeUnknown},
		{"empty", "", RelayModeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Path2RelayModeMidjourney(c.path)
			if got != c.want {
				t.Errorf("Path2RelayModeMidjourney(%q) = %d, want %d", c.path, got, c.want)
			}
		})
	}
}

func TestPath2RelayMusic(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"post audio music submit", http.MethodPost, "/v1/audio/music", RelayModeMusicSubmit},
		{"get audio music fetch by id", http.MethodGet, "/v1/audio/music/12345", RelayModeMusicFetchByID},
		{"post wrong suffix", http.MethodPost, "/v1/audio/music/12345", RelayModeUnknown},
		{"get wrong contains", http.MethodGet, "/v1/audio/other", RelayModeUnknown},
		{"put method unknown", http.MethodPut, "/v1/audio/music", RelayModeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Path2RelayMusic(c.method, c.path)
			if got != c.want {
				t.Errorf("Path2RelayMusic(%q, %q) = %d, want %d", c.method, c.path, got, c.want)
			}
		})
	}
}

func TestPath2RelaySuno(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"post fetch", http.MethodPost, "/suno/fetch", RelayModeSunoFetch},
		{"get fetch by id", http.MethodGet, "/suno/fetch/12345", RelayModeSunoFetchByID},
		{"submit contains", http.MethodPost, "/suno/submit/music", RelayModeSunoSubmit},
		{"get plain fetch no slash", http.MethodGet, "/suno/fetch", RelayModeUnknown},
		{"put unknown", http.MethodPut, "/suno/fetch", RelayModeUnknown},
		{"unknown path", http.MethodGet, "/suno/other", RelayModeUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Path2RelaySuno(c.method, c.path)
			if got != c.want {
				t.Errorf("Path2RelaySuno(%q, %q) = %d, want %d", c.method, c.path, got, c.want)
			}
		})
	}
}

// TestRelayModeConstantsAreDistinct guards against accidental duplicate iota
// values being introduced when the const block is edited.
func TestRelayModeConstantsAreDistinct(t *testing.T) {
	values := map[string]int{
		"RelayModeUnknown":                        RelayModeUnknown,
		"RelayModeChatCompletions":                RelayModeChatCompletions,
		"RelayModeCompletions":                    RelayModeCompletions,
		"RelayModeEmbeddings":                     RelayModeEmbeddings,
		"RelayModeModerations":                    RelayModeModerations,
		"RelayModeImagesGenerations":              RelayModeImagesGenerations,
		"RelayModeImagesEdits":                    RelayModeImagesEdits,
		"RelayModeEdits":                          RelayModeEdits,
		"RelayModeMidjourneyImagine":              RelayModeMidjourneyImagine,
		"RelayModeMidjourneyDescribe":             RelayModeMidjourneyDescribe,
		"RelayModeMidjourneyBlend":                RelayModeMidjourneyBlend,
		"RelayModeMidjourneyChange":               RelayModeMidjourneyChange,
		"RelayModeMidjourneySimpleChange":         RelayModeMidjourneySimpleChange,
		"RelayModeMidjourneyNotify":               RelayModeMidjourneyNotify,
		"RelayModeMidjourneyTaskFetch":            RelayModeMidjourneyTaskFetch,
		"RelayModeMidjourneyTaskImageSeed":        RelayModeMidjourneyTaskImageSeed,
		"RelayModeMidjourneyTaskFetchByCondition": RelayModeMidjourneyTaskFetchByCondition,
		"RelayModeMidjourneyAction":               RelayModeMidjourneyAction,
		"RelayModeMidjourneyModal":                RelayModeMidjourneyModal,
		"RelayModeMidjourneyShorten":              RelayModeMidjourneyShorten,
		"RelayModeSwapFace":                       RelayModeSwapFace,
		"RelayModeMidjourneyUpload":               RelayModeMidjourneyUpload,
		"RelayModeMidjourneyVideo":                RelayModeMidjourneyVideo,
		"RelayModeMidjourneyEdits":                RelayModeMidjourneyEdits,
		"RelayModeAudioSpeech":                    RelayModeAudioSpeech,
		"RelayModeAudioTranscription":             RelayModeAudioTranscription,
		"RelayModeAudioTranslation":               RelayModeAudioTranslation,
		"RelayModeSunoFetch":                      RelayModeSunoFetch,
		"RelayModeSunoFetchByID":                  RelayModeSunoFetchByID,
		"RelayModeSunoSubmit":                     RelayModeSunoSubmit,
		"RelayModeVideoFetchByID":                 RelayModeVideoFetchByID,
		"RelayModeVideoSubmit":                    RelayModeVideoSubmit,
		"RelayModeMusicSubmit":                    RelayModeMusicSubmit,
		"RelayModeMusicFetchByID":                 RelayModeMusicFetchByID,
		"RelayModeRerank":                         RelayModeRerank,
		"RelayModeResponses":                      RelayModeResponses,
		"RelayModeRealtime":                       RelayModeRealtime,
		"RelayModeGemini":                         RelayModeGemini,
	}
	seen := make(map[int]string, len(values))
	for name, v := range values {
		if other, ok := seen[v]; ok {
			t.Errorf("constant collision: %s and %s both equal %d", name, other, v)
		}
		seen[v] = name
	}
	if RelayModeUnknown != 0 {
		t.Errorf("RelayModeUnknown should be the zero value, got %d", RelayModeUnknown)
	}
}
