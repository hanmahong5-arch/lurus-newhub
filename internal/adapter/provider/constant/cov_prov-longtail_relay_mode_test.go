package constant

import (
	"net/http"
	"testing"
)

// Business context: Path2RelayMode is the front door that decides which
// billing/response-shape code path a given incoming HTTP request takes.
// A misrouted path means either a request gets rejected outright or, worse,
// gets billed/parsed under the wrong contract. We exercise every branch plus
// the ordering-sensitive overlaps (e.g. "/pg/chat/completions" vs generic
// "embeddings" suffix match, "/v1/models" also matching Gemini).

func TestProvLongtailPath2RelayMode_Table(t *testing.T) {
	cases := []struct {
		name string
		path string
		want int
	}{
		{"chat completions v1 prefix", "/v1/chat/completions", RelayModeChatCompletions},
		{"chat completions pg prefix", "/pg/chat/completions", RelayModeChatCompletions},
		{"chat completions with trailing segment", "/v1/chat/completions/extra", RelayModeChatCompletions},
		{"completions", "/v1/completions", RelayModeCompletions},
		{"embeddings v1 prefix", "/v1/embeddings", RelayModeEmbeddings},
		{"embeddings by suffix only", "/custom/text-embeddings", RelayModeEmbeddings},
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
		{"gemini models v1beta", "/v1beta/models/gemini-pro:generateContent", RelayModeGemini},
		{"gemini models v1", "/v1/models/gpt-4", RelayModeGemini},
		{"midjourney dispatch", "/mj/submit/imagine", RelayModeMidjourneyImagine},
		{"unknown path", "/v1/totally/unrelated", RelayModeUnknown},
		{"empty path", "", RelayModeUnknown},
		{"root path", "/", RelayModeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Path2RelayMode(tc.path)
			if got != tc.want {
				t.Errorf("Path2RelayMode(%q) = %d, want %d", tc.path, got, tc.want)
			}
		})
	}
}

// The v1/embeddings prefix branch is checked before the generic suffix
// branch; a path satisfying both must resolve via the prefix branch (same
// result here, but this locks the precedence so future branch reordering
// doesn't silently change behavior for prefix-and-suffix-both paths).
func TestProvLongtailPath2RelayMode_EmbeddingsPrefixWinsOverSuffix(t *testing.T) {
	got := Path2RelayMode("/v1/embeddings")
	if got != RelayModeEmbeddings {
		t.Fatalf("got %d, want RelayModeEmbeddings", got)
	}
}

func TestProvLongtailPath2RelayModeMidjourney_Table(t *testing.T) {
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
		{"simple-change maps to same mode as change", "/mj/submit/simple-change", RelayModeMidjourneyChange},
		{"fetch (generic suffix)", "/mj/task/fetch", RelayModeMidjourneyTaskFetch},
		{"image-seed", "/mj/task/image-seed", RelayModeMidjourneyTaskImageSeed},
		{"list-by-condition", "/mj/task/list-by-condition", RelayModeMidjourneyTaskFetchByCondition},
		{"unrecognized mj suffix", "/mj/something/else", RelayModeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Path2RelayModeMidjourney(tc.path)
			if got != tc.want {
				t.Errorf("Path2RelayModeMidjourney(%q) = %d, want %d", tc.path, got, tc.want)
			}
		})
	}
}

// Path2RelayMode dispatches to the midjourney sub-router only for /mj paths;
// verify the umbrella function actually reaches the sub-mode, not just
// stopping at "some midjourney mode".
func TestProvLongtailPath2RelayMode_DelegatesToMidjourneySubRouter(t *testing.T) {
	got := Path2RelayMode("/mj/submit/describe")
	if got != RelayModeMidjourneyDescribe {
		t.Errorf("Path2RelayMode(/mj/submit/describe) = %d, want RelayModeMidjourneyDescribe (%d)", got, RelayModeMidjourneyDescribe)
	}
}

func TestProvLongtailPath2RelayMusic_Table(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"POST submit music", http.MethodPost, "/v1/audio/music", RelayModeMusicSubmit},
		{"GET fetch music by id", http.MethodGet, "/v1/audio/music/abc123", RelayModeMusicFetchByID},
		{"GET without music id path unknown", http.MethodGet, "/v1/audio/music", RelayModeUnknown},
		{"wrong method for submit", http.MethodGet, "/v1/audio/music", RelayModeUnknown},
		{"wrong method for fetch", http.MethodPost, "/v1/audio/music/abc123", RelayModeUnknown},
		{"unrelated path", http.MethodPost, "/v1/chat/completions", RelayModeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Path2RelayMusic(tc.method, tc.path)
			if got != tc.want {
				t.Errorf("Path2RelayMusic(%q, %q) = %d, want %d", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

func TestProvLongtailPath2RelaySuno_Table(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"POST fetch (list)", http.MethodPost, "/suno/fetch", RelayModeSunoFetch},
		{"GET fetch by id", http.MethodGet, "/suno/fetch/task-1", RelayModeSunoFetchByID},
		{"submit path via Contains", http.MethodPost, "/suno/submit/music", RelayModeSunoSubmit},
		{"GET plain fetch prefix mismatch (not POST) falls to fetch-by-id check", http.MethodGet, "/suno/fetch", RelayModeUnknown},
		{"unrelated", http.MethodGet, "/v1/chat/completions", RelayModeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Path2RelaySuno(tc.method, tc.path)
			if got != tc.want {
				t.Errorf("Path2RelaySuno(%q, %q) = %d, want %d", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// Enum ordering matters for anything doing numeric comparisons/serialization
// (e.g. persisted relay-mode integers in logs/DB). Lock the known-stable
// early values so a future insertion in the middle of the block doesn't
// silently renumber persisted values.
func TestProvLongtailRelayModeConstants_StableValues(t *testing.T) {
	if RelayModeUnknown != 0 {
		t.Errorf("RelayModeUnknown = %d, want 0 (zero value must mean unknown/unset)", RelayModeUnknown)
	}
	if RelayModeChatCompletions != 1 {
		t.Errorf("RelayModeChatCompletions = %d, want 1", RelayModeChatCompletions)
	}
}
