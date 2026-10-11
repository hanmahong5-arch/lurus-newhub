package app

// channel_modality_test.go - the modality route filter in channel selection.
// ROUTING_MODALITY_FILTER=off|observe|enforce decides whether a wrong-kind
// route (a decision channel offered to an embeddings call) is ignored, counted
// or removed. Runs on both selection paths (SQL and the in-memory cache): they
// share one predicate and must agree.

import (
	"errors"
	"testing"

	relayconstant "github.com/LurusTech/lurus-hub/internal/adapter/provider/constant"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const (
	modOKChannel       = 9461 // OpenAI-compatible channel
	modDecisionChannel = 9462 // TypeSafe: a decision channel
)

// seedModalityChannels creates one channel per (id -> type) all serving model.
func seedModalityChannels(t *testing.T, model string, cacheOn bool, chans map[int]int) {
	t.Helper()
	db := setupServiceTestDB(t)
	if err := db.AutoMigrate(&repo.Ability{}); err != nil {
		t.Fatalf("automigrate abilities: %v", err)
	}
	resetAffinityMemForTest()
	repo.SetModalityOverridesForTest(nil)
	t.Cleanup(func() { repo.SetModalityOverridesForTest(nil) })
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = prev })

	for id, ctype := range chans {
		w := uint(1)
		ch := &repo.Channel{
			Id: id, Type: ctype, Status: common.ChannelStatusEnabled,
			Name: "modality-test", Models: model, Group: "default", TenantId: "default", Weight: &w,
		}
		if err := db.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", id, err)
		}
		if err := db.Create(&repo.Ability{Group: "default", Model: model, ChannelId: id, Enabled: true, Weight: w}).Error; err != nil {
			t.Fatalf("seed ability %d: %v", id, err)
		}
	}
	if cacheOn {
		common.MemoryCacheEnabled = true
		repo.InitChannelCache()
	}
}

func modalitySelect(t *testing.T, model string, relayMode int) (*repo.Channel, error) {
	t.Helper()
	c := affinitySelectCtx(t, "")
	c.Set(string(constant.ContextKeyRelayMode), relayMode)
	retry := 0
	ch, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "default", ModelName: model, Retry: &retry})
	return ch, err
}

func forEachPath(t *testing.T, fn func(t *testing.T, cacheOn bool)) {
	for _, cacheOn := range []bool{false, true} {
		name := "sql"
		if cacheOn {
			name = "cache"
		}
		t.Run(name, func(t *testing.T) { fn(t, cacheOn) })
	}
}

func TestModalityFilter_EnforceDropsWrongKindRoutes(t *testing.T) {
	forEachPath(t, func(t *testing.T, cacheOn bool) {
		t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
		seedModalityChannels(t, "text-embedding-model-a", cacheOn, map[int]int{modOKChannel: 1, modDecisionChannel: constant.ChannelTypeTypeSafe})
		for i := 0; i < 60; i++ {
			ch, err := modalitySelect(t, "text-embedding-model-a", relayconstant.RelayModeEmbeddings)
			if err != nil || ch == nil || ch.Id != modOKChannel {
				t.Fatalf("iteration %d: got %v err=%v, want the embedding-capable channel only", i, ch, err)
			}
		}
	})
}

func TestModalityFilter_EnforceNoCandidateIsDedicatedSentinel(t *testing.T) {
	forEachPath(t, func(t *testing.T, cacheOn bool) {
		t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
		seedModalityChannels(t, "text-embedding-model-a", cacheOn, map[int]int{modDecisionChannel: constant.ChannelTypeTypeSafe})
		ch, err := modalitySelect(t, "text-embedding-model-a", relayconstant.RelayModeEmbeddings)
		if ch != nil {
			t.Fatalf("selected #%d, want none", ch.Id)
		}
		if !errors.Is(err, repo.ErrNoChannelSupportsModality) {
			t.Fatalf("err = %v, want ErrNoChannelSupportsModality", err)
		}
		if errors.Is(err, repo.ErrNoChannelSatisfiesPredicate) {
			t.Fatal("modality miss must not read as a provider-filter miss")
		}
		var miss *ModalityMissError
		if !errors.As(err, &miss) || miss.Mismatched != 1 || miss.RelayMode != "embeddings" {
			t.Fatalf("miss = %+v, want 1 mismatched embeddings route", miss)
		}
		reasons := miss.Reasons()
		if len(reasons) != 1 || reasons[0].Code != "modality_mismatch" || reasons[0].RouteCount != 1 {
			t.Fatalf("reasons = %+v", reasons)
		}
	})
}

func TestModalityFilter_ObserveCountsButKeepsRoutes(t *testing.T) {
	forEachPath(t, func(t *testing.T, cacheOn bool) {
		t.Setenv("ROUTING_MODALITY_FILTER", "observe")
		seedModalityChannels(t, "text-embedding-model-a", cacheOn, map[int]int{modDecisionChannel: constant.ChannelTypeTypeSafe})
		before := testutil.ToFloat64(metrics.RoutingModalityMismatchTotal.WithLabelValues("embeddings"))
		ch, err := modalitySelect(t, "text-embedding-model-a", relayconstant.RelayModeEmbeddings)
		if err != nil || ch == nil || ch.Id != modDecisionChannel {
			t.Fatalf("observe must not change the result: ch=%v err=%v", ch, err)
		}
		after := testutil.ToFloat64(metrics.RoutingModalityMismatchTotal.WithLabelValues("embeddings"))
		if after-before != 1 {
			t.Fatalf("mismatch counter moved by %v, want 1", after-before)
		}
	})
}

func TestModalityFilter_DefaultModeIsObserve(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "")
	seedModalityChannels(t, "text-embedding-model-a", false, map[int]int{modDecisionChannel: constant.ChannelTypeTypeSafe})
	ch, err := modalitySelect(t, "text-embedding-model-a", relayconstant.RelayModeEmbeddings)
	if err != nil || ch == nil {
		t.Fatalf("default mode must not refuse: ch=%v err=%v", ch, err)
	}
}

func TestModalityFilter_OffIsSilent(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "off")
	seedModalityChannels(t, "text-embedding-model-a", false, map[int]int{modDecisionChannel: constant.ChannelTypeTypeSafe})
	before := testutil.ToFloat64(metrics.RoutingModalityMismatchTotal.WithLabelValues("embeddings"))
	ch, err := modalitySelect(t, "text-embedding-model-a", relayconstant.RelayModeEmbeddings)
	if err != nil || ch == nil {
		t.Fatalf("off must not refuse: ch=%v err=%v", ch, err)
	}
	if testutil.ToFloat64(metrics.RoutingModalityMismatchTotal.WithLabelValues("embeddings")) != before {
		t.Fatal("off must not count mismatches")
	}
}

func TestModalityFilter_NoRequirementModeIsNeverFiltered(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
	seedModalityChannels(t, "model-a", false, map[int]int{modDecisionChannel: constant.ChannelTypeTypeSafe})
	// RelayModeUnknown (also: no relay mode on the context at all) carries no requirement.
	for _, mode := range []int{relayconstant.RelayModeUnknown, relayconstant.RelayModeModerations} {
		if ch, err := modalitySelect(t, "model-a", mode); err != nil || ch == nil {
			t.Fatalf("mode %d: ch=%v err=%v, want unfiltered", mode, ch, err)
		}
	}
}

func TestModalityFilter_ChatRequestRefusesDecisionRoute(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
	seedModalityChannels(t, "model-a", false, map[int]int{modDecisionChannel: constant.ChannelTypeTypeSafe})
	_, err := modalitySelect(t, "model-a", relayconstant.RelayModeChatCompletions)
	if !errors.Is(err, repo.ErrNoChannelSupportsModality) {
		t.Fatalf("err = %v, want ErrNoChannelSupportsModality", err)
	}
}

func TestModalityFilter_AdapterUnsupportedReason(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
	// A channel of a chat-only vendor type has no rerank wire even though nothing in the model
	// name says so (the inferred modality is only a guess).
	seedModalityChannels(t, "model-a", false, map[int]int{modOKChannel: constant.ChannelTypeAnthropic})
	_, err := modalitySelect(t, "model-a", relayconstant.RelayModeRerank)
	var miss *ModalityMissError
	if !errors.As(err, &miss) || miss.AdapterUnsupported != 1 || miss.Mismatched != 0 {
		t.Fatalf("err = %v miss=%+v, want one adapter_unsupported route", err, miss)
	}
	if r := miss.Reasons(); len(r) != 1 || r[0].Code != "adapter_unsupported" {
		t.Fatalf("reasons = %+v", r)
	}
}

func TestModalityFilter_AdminOverrideWins(t *testing.T) {
	t.Setenv("ROUTING_MODALITY_FILTER", "enforce")
	seedModalityChannels(t, "custom-vectors", false, map[int]int{modOKChannel: 1})
	// No override: the name says nothing, the route is a guess, so it serves.
	if ch, err := modalitySelect(t, "custom-vectors", relayconstant.RelayModeEmbeddings); err != nil || ch == nil {
		t.Fatalf("unknown route must be compatible: ch=%v err=%v", ch, err)
	}
	// The administrator declares the model a rerank model: an embeddings call is now wrong-kind.
	repo.SetModalityOverridesForTest(map[string]string{"custom-vectors": "rerank"})
	if _, err := modalitySelect(t, "custom-vectors", relayconstant.RelayModeEmbeddings); !errors.Is(err, repo.ErrNoChannelSupportsModality) {
		t.Fatalf("override ignored: err = %v", err)
	}
	if ch, err := modalitySelect(t, "custom-vectors", relayconstant.RelayModeRerank); err != nil || ch == nil {
		t.Fatalf("override should serve rerank: ch=%v err=%v", ch, err)
	}
}

func TestRequiredModalityForRelayMode_Table(t *testing.T) {
	cases := map[int]string{
		relayconstant.RelayModeChatCompletions:   "chat/chat",
		relayconstant.RelayModeResponses:         "chat/chat",
		relayconstant.RelayModeEmbeddings:        "embedding/embeddings",
		relayconstant.RelayModeRerank:            "rerank/rerank",
		relayconstant.RelayModeSystemOne:         "decision/systemone",
		relayconstant.RelayModeImagesGenerations: "image/images",
		relayconstant.RelayModeAudioSpeech:       "audio/audio",
		relayconstant.RelayModeModerations:       "/",
		relayconstant.RelayModeUnknown:           "/",
		relayconstant.RelayModeMidjourneyImagine: "/",
		relayconstant.RelayModeGemini:            "/",
	}
	for mode, want := range cases {
		m, label := RequiredModalityForRelayMode(mode)
		if got := string(m) + "/" + label; got != want {
			t.Errorf("mode %d: got %q want %q", mode, got, want)
		}
	}
}
