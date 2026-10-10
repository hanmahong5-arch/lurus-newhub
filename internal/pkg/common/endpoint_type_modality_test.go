package common

import (
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestGetEndpointTypesByChannelType_Modality(t *testing.T) {
	ep := func(e ...constant.EndpointType) []constant.EndpointType { return e }
	cases := []struct {
		name  string
		ctype int
		model string
		want  []constant.EndpointType
	}{
		{"openai embedding model", constant.ChannelTypeOpenAI, "text-embedding-3-small", ep(constant.EndpointTypeEmbeddings)},
		{"jina embedding model", constant.ChannelTypeJina, "jina-embeddings-v3", ep(constant.EndpointTypeEmbeddings)},
		{"jina rerank model", constant.ChannelTypeJina, "jina-reranker-v2", ep(constant.EndpointTypeJinaRerank)},
		{"openai-compatible rerank model", constant.ChannelTypeOpenAI, "bge-reranker-v2-m3", ep(constant.EndpointTypeJinaRerank)},
		{"typesafe stays systemone even with an embed-looking name", constant.ChannelTypeTypeSafe, "embed-x", ep(constant.EndpointTypeSystemOne)},
		{"plain chat unchanged", constant.ChannelTypeOpenAI, "model-a", ep(constant.EndpointTypeOpenAI)},
		{"jina unnamed keeps legacy-free table answer", constant.ChannelTypeJina, "model-a", ep(constant.EndpointTypeEmbeddings)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := GetEndpointTypesByChannelType(c.ctype, c.model); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
