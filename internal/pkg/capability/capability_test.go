package capability

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestInfer_Table(t *testing.T) {
	cases := []struct {
		name     string
		override string
		ctype    int
		model    string
		want     Modality
		src      Source
	}{
		{"override beats channel type", "embedding", constant.ChannelTypeTypeSafe, "x", Embedding, SourceOverride},
		{"override beats name", "chat", constant.ChannelTypeOpenAI, "text-embedding-3-small", Chat, SourceOverride},
		{"invalid override ignored", "bogus", constant.ChannelTypeOpenAI, "text-embedding-3-small", Embedding, SourceName},
		{"typesafe is decision", "", constant.ChannelTypeTypeSafe, "model-a", Decision, SourceChannelType},
		{"system one compatible is decision", "", constant.ChannelTypeSystemOneCompatible, "embed-x", Decision, SourceChannelType},
		{"jina rerank by name", "", constant.ChannelTypeJina, "jina-reranker-v2", Rerank, SourceChannelType},
		{"jina other is embedding", "", constant.ChannelTypeJina, "jina-embeddings-v3", Embedding, SourceName},
		{"jina unnamed falls to table", "", constant.ChannelTypeJina, "model-a", Embedding, SourceTable},
		{"rerank beats embedding words", "", constant.ChannelTypeOpenAI, "bge-reranker-v2-m3", Rerank, SourceName},
		{"embedding", "", constant.ChannelTypeOpenAI, "text-embedding-3-large", Embedding, SourceName},
		{"bge", "", constant.ChannelTypeOpenAI, "BGE-M3", Embedding, SourceName},
		{"gte", "", constant.ChannelTypeOpenAI, "gte-large", Embedding, SourceName},
		{"e5", "", constant.ChannelTypeOpenAI, "multilingual-e5-large", Embedding, SourceName},
		{"voyage", "", constant.ChannelTypeOpenAI, "voyage-3", Embedding, SourceName},
		{"sentence", "", constant.ChannelTypeOpenAI, "sentence-t5", Embedding, SourceName},
		{"tts", "", constant.ChannelTypeOpenAI, "tts-1", Audio, SourceName},
		{"whisper", "", constant.ChannelTypeOpenAI, "whisper-1", Audio, SourceName},
		{"transcribe", "", constant.ChannelTypeOpenAI, "model-a-transcribe", Audio, SourceName},
		{"dall-e", "", constant.ChannelTypeOpenAI, "dall-e-3", Image, SourceName},
		{"image", "", constant.ChannelTypeOpenAI, "model-a-image-1", Image, SourceName},
		{"flux", "", constant.ChannelTypeOpenAI, "flux-pro", Image, SourceName},
		{"midjourney by type", "", constant.ChannelTypeMidjourney, "mj-x", Image, SourceTable},
		{"plain chat is default", "", constant.ChannelTypeOpenAI, "model-a", Chat, SourceDefault},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Infer(c.override, c.ctype, c.model)
			if got.Modality != c.want || got.Source != c.src {
				t.Fatalf("Infer(%q,%d,%q) = %+v, want {%s %s}", c.override, c.ctype, c.model, got, c.want, c.src)
			}
		})
	}
}

func TestInference_StoredBlanksDefaults(t *testing.T) {
	if s := Infer("", constant.ChannelTypeOpenAI, "model-a").Stored(); s != "" {
		t.Errorf("defaulted chat must be stored as unknown, got %q", s)
	}
	if s := Infer("", constant.ChannelTypeOpenAI, "tts-1").Stored(); s != "audio" {
		t.Errorf("Stored = %q, want audio", s)
	}
}

func TestCompatible_Table(t *testing.T) {
	emb := Inference{Embedding, SourceName}
	cases := []struct {
		name string
		req  Modality
		got  Inference
		want bool
	}{
		{"no requirement", "", emb, true},
		{"unknown route", Embedding, Inference{}, true},
		{"guessed chat route is compatible", Embedding, Inference{Chat, SourceDefault}, true},
		{"embedding on embedding", Embedding, emb, true},
		{"embedding on rerank", Embedding, Inference{Rerank, SourceName}, false},
		{"rerank on embedding", Rerank, emb, false},
		{"chat on embedding", Chat, emb, false},
		{"chat on decision", Chat, Inference{Decision, SourceChannelType}, false},
		{"chat on rerank", Chat, Inference{Rerank, SourceName}, false},
		{"chat on multimodal audio", Chat, Inference{Audio, SourceName}, true},
		{"chat on multimodal image", Chat, Inference{Image, SourceName}, true},
		{"decision on decision", Decision, Inference{Decision, SourceChannelType}, true},
		{"decision on overridden chat", Decision, Inference{Chat, SourceOverride}, false},
		{"image on audio", Image, Inference{Audio, SourceName}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compatible(c.req, c.got); got != c.want {
				t.Fatalf("Compatible(%q,%+v) = %v, want %v", c.req, c.got, got, c.want)
			}
		})
	}
}

func TestSupportsRerank(t *testing.T) {
	for _, ty := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeCohere, constant.ChannelTypeJina, constant.ChannelTypeAzure} {
		if !SupportsRerank(ty) {
			t.Errorf("type %d should support rerank", ty)
		}
	}
	for _, ty := range []int{constant.ChannelTypeAnthropic, constant.ChannelTypeGemini, constant.ChannelTypeZhipu_v4, constant.ChannelTypeTypeSafe} {
		if SupportsRerank(ty) {
			t.Errorf("type %d should not support rerank", ty)
		}
	}
	if AdapterSupports(constant.ChannelTypeAnthropic, Chat) != true {
		t.Error("non-rerank modalities are not tabulated and must read as supported")
	}
}

func TestFilterMode(t *testing.T) {
	cases := map[string]string{"": FilterObserve, "observe": FilterObserve, "enforce": FilterEnforce, "off": FilterOff, "ENFORCE": FilterObserve, "true": FilterObserve}
	for env, want := range cases {
		t.Setenv(filterEnvVar, env)
		if got := FilterMode(); got != want {
			t.Errorf("FilterMode(%q) = %q, want %q", env, got, want)
		}
	}
}
