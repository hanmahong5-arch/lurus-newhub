package constant

// relay_mode_test.go pins two properties of the RelayMode iota block that
// wire-formats-03 (POST /v1/responses/compact) must not break:
//   - every existing RelayMode* ordinal stays exactly where it was, because
//     RelayModeResponsesCompact is appended at the END of the block, never
//     inserted in the middle;
//   - Path2RelayMode resolves the /v1/responses/compact prefix to the new
//     mode BEFORE the plain /v1/responses prefix check would otherwise
//     shadow it (strings.HasPrefix("/v1/responses/compact", "/v1/responses")
//     is also true, so branch order is the only thing preventing the two
//     paths from colliding on the same mode).

import "testing"

func TestRelayMode_ExistingOrdinalsPinned(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"RelayModeUnknown", RelayModeUnknown, 0},
		{"RelayModeChatCompletions", RelayModeChatCompletions, 1},
		{"RelayModeCompletions", RelayModeCompletions, 2},
		{"RelayModeEmbeddings", RelayModeEmbeddings, 3},
		{"RelayModeModerations", RelayModeModerations, 4},
		{"RelayModeImagesGenerations", RelayModeImagesGenerations, 5},
		{"RelayModeImagesEdits", RelayModeImagesEdits, 6},
		{"RelayModeEdits", RelayModeEdits, 7},
		{"RelayModeMidjourneyImagine", RelayModeMidjourneyImagine, 8},
		{"RelayModeMidjourneyDescribe", RelayModeMidjourneyDescribe, 9},
		{"RelayModeMidjourneyBlend", RelayModeMidjourneyBlend, 10},
		{"RelayModeMidjourneyChange", RelayModeMidjourneyChange, 11},
		{"RelayModeMidjourneySimpleChange", RelayModeMidjourneySimpleChange, 12},
		{"RelayModeMidjourneyNotify", RelayModeMidjourneyNotify, 13},
		{"RelayModeMidjourneyTaskFetch", RelayModeMidjourneyTaskFetch, 14},
		{"RelayModeMidjourneyTaskImageSeed", RelayModeMidjourneyTaskImageSeed, 15},
		{"RelayModeMidjourneyTaskFetchByCondition", RelayModeMidjourneyTaskFetchByCondition, 16},
		{"RelayModeMidjourneyAction", RelayModeMidjourneyAction, 17},
		{"RelayModeMidjourneyModal", RelayModeMidjourneyModal, 18},
		{"RelayModeMidjourneyShorten", RelayModeMidjourneyShorten, 19},
		{"RelayModeSwapFace", RelayModeSwapFace, 20},
		{"RelayModeMidjourneyUpload", RelayModeMidjourneyUpload, 21},
		{"RelayModeMidjourneyVideo", RelayModeMidjourneyVideo, 22},
		{"RelayModeMidjourneyEdits", RelayModeMidjourneyEdits, 23},
		{"RelayModeAudioSpeech", RelayModeAudioSpeech, 24},
		{"RelayModeAudioTranscription", RelayModeAudioTranscription, 25},
		{"RelayModeAudioTranslation", RelayModeAudioTranslation, 26},
		{"RelayModeSunoFetch", RelayModeSunoFetch, 27},
		{"RelayModeSunoFetchByID", RelayModeSunoFetchByID, 28},
		{"RelayModeSunoSubmit", RelayModeSunoSubmit, 29},
		{"RelayModeVideoFetchByID", RelayModeVideoFetchByID, 30},
		{"RelayModeVideoSubmit", RelayModeVideoSubmit, 31},
		{"RelayModeMusicSubmit", RelayModeMusicSubmit, 32},
		{"RelayModeMusicFetchByID", RelayModeMusicFetchByID, 33},
		{"RelayModeRerank", RelayModeRerank, 34},
		{"RelayModeResponses", RelayModeResponses, 35},
		{"RelayModeRealtime", RelayModeRealtime, 36},
		{"RelayModeGemini", RelayModeGemini, 37},
		{"RelayModeResponsesCompact", RelayModeResponsesCompact, 38},
		{"RelayModeResponsesRetrieve", RelayModeResponsesRetrieve, 39},
		{"RelayModeResponsesDelete", RelayModeResponsesDelete, 40},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d (an existing RelayMode ordinal moved, or RelayModeResponsesCompact was not appended at the end)", tc.name, tc.got, tc.want)
		}
	}
}

// TestPath2RelayMode_ResponsesRegistryPathsNeverAutoResolved pins the claim
// in RelayModeResponsesRetrieve/Delete's doc comment: Path2RelayMode has no
// case for GET/DELETE /v1/responses/:response_id, so those two constants
// are set directly by handler/relay_responses_registry.go, not derived from
// the request path the way every other mode above is.
func TestPath2RelayMode_ResponsesRegistryPathsNeverAutoResolved(t *testing.T) {
	for _, path := range []string{"/v1/responses/resp_abc123", "/v1/responses/resp_abc123/"} {
		if got := Path2RelayMode(path); got == RelayModeResponsesRetrieve || got == RelayModeResponsesDelete {
			t.Errorf("Path2RelayMode(%q) = %d, want it NOT to auto-resolve to the registry-only modes", path, got)
		}
	}
}

func TestPath2RelayMode_ResponsesCompactBeforePrefix(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{"/v1/responses/compact", RelayModeResponsesCompact},
		{"/v1/responses", RelayModeResponses},
		{"/v1/responses/", RelayModeResponses},
	}
	for _, tc := range cases {
		if got := Path2RelayMode(tc.path); got != tc.want {
			t.Errorf("Path2RelayMode(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
}
