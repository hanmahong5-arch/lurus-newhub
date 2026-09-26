package helper

import (
	"context"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"

	"github.com/gin-gonic/gin"
)

// --- common.go remaining branches ---

// Renamed from TestSetEventStreamHeaders_ModelProviderHeader: cycle-14 L6
// replaced X-Model-Provider with X-Relay-Adapter, which is what the value
// (the channel's adapter family) has always been. Same branch.
func TestSetEventStreamHeaders_RelayAdapterHeader(t *testing.T) {
	c, w := writeCtx()
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
	}
	SetEventStreamHeaders(c, info)
	if got := w.Header().Get(RelayAdapterHeader); got == "" {
		t.Errorf("%s header not set when ChannelMeta present, got %q", RelayAdapterHeader, got)
	}
}

func TestPingData_CancelledContext(t *testing.T) {
	c, _ := writeCtx()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	err := PingData(c)
	if err == nil || !strings.Contains(err.Error(), "request context done") {
		t.Errorf("expected request-context-done error, got %v", err)
	}
}

func TestObjectData_MarshalError(t *testing.T) {
	c, _ := writeCtx()
	// a channel value cannot be JSON-marshalled -> marshal error path.
	err := ObjectData(c, make(chan int))
	if err == nil || !strings.Contains(err.Error(), "marshalling object") {
		t.Errorf("expected marshal error, got %v", err)
	}
}

func TestWssObject_MarshalError(t *testing.T) {
	c, _ := writeCtx()
	// marshal fails before the nil-conn check is reached.
	err := WssObject(c, nil, make(chan int))
	if err == nil || !strings.Contains(err.Error(), "marshalling object") {
		t.Errorf("expected marshal error, got %v", err)
	}
}

// --- perception.go remaining branch ---

func TestComputeLurusExtension_NilInfo(t *testing.T) {
	if ext := ComputeLurusExtension(nil, &dto.Usage{}, 100); ext != nil {
		t.Errorf("ComputeLurusExtension(nil) = %v, want nil", ext)
	}
}

// --- price.go remaining branches ---

func TestModelPriceHelper_FreeByZeroRatio(t *testing.T) {
	qs := operation_setting.GetQuotaSetting()
	prev := qs.EnableFreeModelPreConsume
	qs.EnableFreeModelPreConsume = false
	t.Cleanup(func() { qs.EnableFreeModelPreConsume = prev })

	// ratio-based model with a configured ratio of 0 and a non-zero group ratio:
	// exercises the else-branch (modelRatio == 0) free-model zeroing.
	seedRatios(t, `{"zero-ratio-model":0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "zero-ratio-model", UsingGroup: "default"}
	pd, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !pd.FreeModel {
		t.Error("FreeModel should be true when model ratio is 0")
	}
	if pd.QuotaToPreConsume != 0 {
		t.Errorf("QuotaToPreConsume = %d, want 0", pd.QuotaToPreConsume)
	}
}

func TestModelPriceHelper_DebugEnabled(t *testing.T) {
	prev := common.DebugEnabled
	common.DebugEnabled = true
	t.Cleanup(func() { common.DebugEnabled = prev })

	seedRatios(t, `{"ratio-model":2.0}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "ratio-model", UsingGroup: "default"}
	if _, err := ModelPriceHelper(priceCtx(), info, 100, &types.TokenCountMeta{}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestModelPriceHelperPerCall_DefaultMapFallback(t *testing.T) {
	// "swap_face" is absent from the (empty) configured price map but present in the
	// default per-call price map -> exercises the default-map lookup branch.
	seedRatios(t, `{}`, `{}`, `{"default":1.0}`, map[string]map[string]float64{})
	info := &relaycommon.RelayInfo{OriginModelName: "swap_face", UsingGroup: "default"}
	pd := ModelPriceHelperPerCall(priceCtx(), info)
	if pd.ModelPrice != 0.05 {
		t.Errorf("ModelPrice = %v, want 0.05 (default map)", pd.ModelPrice)
	}
	// 0.05 * QuotaPerUnit(500000) * 1.0 = 25000
	if pd.Quota != 25000 {
		t.Errorf("Quota = %d, want 25000", pd.Quota)
	}
}

// --- valid_request.go remaining branches ---

func TestGetAndValidAudioRequest_SpeechAndErrors(t *testing.T) {
	t.Run("speech mode missing model rejected", func(t *testing.T) {
		// RelayModeAudioSpeech == 24; the previous suite passed 25 (transcription),
		// so the speech case block was never exercised.
		c, _ := newJSONCtx("POST", "/v1/audio/speech", `{"input":"hi"}`)
		_, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech)
		if err == nil || !strings.Contains(err.Error(), "model is required") {
			t.Fatalf("expected model-required error, got %v", err)
		}
	})

	t.Run("bad json rejected", func(t *testing.T) {
		c, _ := newJSONCtx("POST", "/v1/audio/speech", `not-json`)
		if _, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech); err == nil {
			t.Fatal("expected unmarshal error")
		}
	})
}

func TestGetAndValidateEmbeddingRequest_BadJSON(t *testing.T) {
	c, _ := newJSONCtx("POST", "/v1/embeddings", `not-json`)
	_, err := GetAndValidateEmbeddingRequest(c, 3)
	if err == nil {
		t.Fatal("expected unmarshal error for bad json")
	}
}

func TestGetAndValidateTextRequest_EmbeddingsModelFromParam(t *testing.T) {
	// RelayModeEmbeddings with empty model pulls the model from the path param and
	// takes the (empty) embeddings switch case.
	c, _ := newJSONCtx("POST", "/v1/embeddings", `{"input":"x"}`)
	c.Params = gin.Params{{Key: "model", Value: "text-embed-3"}}
	req, err := GetAndValidateTextRequest(c, 3 /*RelayModeEmbeddings*/)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Model != "text-embed-3" {
		t.Errorf("model = %q, want text-embed-3 (from param)", req.Model)
	}
}

func TestGetAndValidateGeminiEmbedding_BadJSON(t *testing.T) {
	t.Run("embedding bad json", func(t *testing.T) {
		c, _ := newJSONCtx("POST", "/v1beta/models/x:embedContent", `not-json`)
		if _, err := GetAndValidateGeminiEmbeddingRequest(c); err == nil {
			t.Fatal("expected unmarshal error")
		}
	})
	t.Run("batch embedding bad json", func(t *testing.T) {
		c, _ := newJSONCtx("POST", "/v1beta/models/x:batchEmbedContents", `not-json`)
		if _, err := GetAndValidateGeminiBatchEmbeddingRequest(c); err == nil {
			t.Fatal("expected unmarshal error")
		}
	})
}
