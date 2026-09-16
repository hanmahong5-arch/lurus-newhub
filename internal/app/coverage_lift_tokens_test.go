package app

// coverage_lift_tokens_test.go — reachable image/media token-accounting branches
// the existing tier left uncovered: per-model tile base-token table, the
// patch-based 1536-cap scaling path, the EstimateRequestToken image data-URI and
// remote-file branches. All assertions are on real computed token counts.

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// TestGetImageToken_TileBaseTokenTable proves the per-model base-token
// classification for tile-based models. With Detail="low" getImageToken returns
// exactly the model's base token count without decoding, so this pins the table.
func TestGetImageToken_TileBaseTokenTable(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"gpt-4o-mini", 2833},
		{"gpt-5", 70},
		{"gpt-5-chat-latest", 70},
		{"o1", 75},
		{"o3-mini", 75},
		{"computer-use-preview", 65},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got, err := getImageToken(&types.FileMeta{Detail: "low"}, tc.model, false)
			if err != nil {
				t.Fatalf("getImageToken(%s): %v", tc.model, err)
			}
			if got != tc.want {
				t.Errorf("base tokens for %s = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

// TestGetImageToken_PatchCapLargeImage proves the patch-based 1536-cap path: a
// large image drives rawPatches beyond 1536, so the result is the capped patch
// count scaled by the model multiplier (never exceeds ceil(1536*multiplier)),
// and is strictly larger than the below-cap result for a tiny image.
func TestGetImageToken_PatchCapLargeImage(t *testing.T) {
	prev := constant.GetMediaToken
	prevNS := constant.GetMediaTokenNotStream
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	defer func() { constant.GetMediaToken = prev; constant.GetMediaTokenNotStream = prevNS }()

	// 1600x1600 => ceil(1600/32)^2 = 50*50 = 2500 raw patches, above the 1536 cap.
	large := pngDataURL(t, 1600, 1600)
	small := pngDataURL(t, 96, 96) // 3*3 = 9 patches, below cap

	cases := []struct {
		model      string
		multiplier float64
	}{
		{"gpt-4.1-mini", 1.62},
		{"gpt-4.1-nano", 2.46},
		{"o4-mini", 1.72},
		{"gpt-5-mini", 1.62},
		{"gpt-5-nano", 2.46},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			capped, err := getImageToken(&types.FileMeta{OriginData: large, Detail: "high"}, tc.model, false)
			if err != nil {
				t.Fatalf("getImageToken large %s: %v", tc.model, err)
			}
			below, err := getImageToken(&types.FileMeta{OriginData: small, Detail: "high"}, tc.model, false)
			if err != nil {
				t.Fatalf("getImageToken small %s: %v", tc.model, err)
			}
			ceiling := int(math.Round(1536 * tc.multiplier))
			if capped <= 0 || capped > ceiling {
				t.Errorf("%s capped tokens = %d, want in (0, %d]", tc.model, capped, ceiling)
			}
			if capped <= below {
				t.Errorf("%s: capped(%d) should exceed below-cap(%d)", tc.model, capped, below)
			}
		})
	}
}

// TestEstimateRequestToken_ImageDataURI proves the data:image URI branch of
// EstimateRequestToken: the file is classified as an image and routed through
// getImageToken (OpenAI text model), adding a positive token count.
func TestEstimateRequestToken_ImageDataURI(t *testing.T) {
	prev := constant.CountToken
	prevMT := constant.GetMediaToken
	prevNS := constant.GetMediaTokenNotStream
	constant.CountToken = true
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	defer func() {
		constant.CountToken = prev
		constant.GetMediaToken = prevMT
		constant.GetMediaTokenNotStream = prevNS
	}()

	c := createTestGinContext()
	c.Set(string(constant.ContextKeyOriginalModel), "gpt-4o")
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}

	baseMeta := &types.TokenCountMeta{CombineText: "x", MessagesCount: 1}
	baseline, err := EstimateRequestToken(c, baseMeta, info)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	c2 := createTestGinContext()
	c2.Set(string(constant.ContextKeyOriginalModel), "gpt-4o")
	meta := &types.TokenCountMeta{
		CombineText:   "x",
		MessagesCount: 1,
		Files:         []*types.FileMeta{{OriginData: pngDataURL(t, 64, 64)}},
	}
	withImg, err := EstimateRequestToken(c2, meta, info)
	if err != nil {
		t.Fatalf("EstimateRequestToken image: %v", err)
	}
	if withImg <= baseline {
		t.Errorf("image data-URI added %d tokens, want > 0", withImg-baseline)
	}
}

// TestEstimateRequestToken_RemoteFileMimeDetected proves the http-file branch:
// a remote URL whose Content-Type is audio is fetched, classified as audio, and
// contributes the fixed 256-token audio allowance.
func TestEstimateRequestToken_RemoteFileMimeDetected(t *testing.T) {
	allowLocalFetch(t)
	raiseDownloadCap(t)

	prev := constant.CountToken
	prevMT := constant.GetMediaToken
	prevNS := constant.GetMediaTokenNotStream
	constant.CountToken = true
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	defer func() {
		constant.CountToken = prev
		constant.GetMediaToken = prevMT
		constant.GetMediaTokenNotStream = prevNS
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFFxxxxWAVE"))
	}))
	defer srv.Close()

	c := createTestGinContext()
	c.Set(string(constant.ContextKeyOriginalModel), "gpt-4o")
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, IsStream: true}

	baseMeta := &types.TokenCountMeta{CombineText: "x", MessagesCount: 1}
	baseline, err := EstimateRequestToken(c, baseMeta, info)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	c2 := createTestGinContext()
	c2.Set(string(constant.ContextKeyOriginalModel), "gpt-4o")
	meta := &types.TokenCountMeta{
		CombineText:   "x",
		MessagesCount: 1,
		Files:         []*types.FileMeta{{OriginData: srv.URL + "/clip"}},
	}
	got, err := EstimateRequestToken(c2, meta, info)
	if err != nil {
		t.Fatalf("EstimateRequestToken remote: %v", err)
	}
	if got-baseline != 256 {
		t.Errorf("remote audio added %d tokens, want 256", got-baseline)
	}
}
