package handler

import (
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestModalityMismatches(t *testing.T) {
	set := func(v ...string) map[string]struct{} {
		m := map[string]struct{}{}
		for _, x := range v {
			m[x] = struct{}{}
		}
		return m
	}
	cases := []struct {
		name   string
		typ    int
		by     map[string]string
		wide   map[string]map[string]struct{}
		expect []modalityMismatch
	}{
		{"consistent chat", constant.ChannelTypeOpenAI, map[string]string{"model-a": "chat"},
			map[string]map[string]struct{}{"model-a": set("chat")}, []modalityMismatch{}},
		{"conflict across channels", constant.ChannelTypeOpenAI, map[string]string{"model-a": "chat"},
			map[string]map[string]struct{}{"model-a": set("chat", "rerank")},
			[]modalityMismatch{{Model: "model-a", Reason: MismatchConflict}}},
		{"unknown modality never flagged", constant.ChannelTypeOpenAI, map[string]string{"model-a": ""},
			map[string]map[string]struct{}{"model-a": set("chat", "rerank")}, []modalityMismatch{}},
		{"rerank on unsupported adapter", constant.ChannelTypeAnthropic, map[string]string{"model-b": "rerank"},
			map[string]map[string]struct{}{"model-b": set("rerank")},
			[]modalityMismatch{{Model: "model-b", Reason: MismatchAdapterUnsupported}}},
		{"rerank on supporting adapter", constant.ChannelTypeJina, map[string]string{"model-b": "rerank"},
			map[string]map[string]struct{}{"model-b": set("rerank")}, []modalityMismatch{}},
		{"sorted by model", constant.ChannelTypeOpenAI, map[string]string{"model-z": "chat", "model-a": "chat"},
			map[string]map[string]struct{}{"model-z": set("chat", "audio"), "model-a": set("chat", "audio")},
			[]modalityMismatch{{Model: "model-a", Reason: MismatchConflict}, {Model: "model-z", Reason: MismatchConflict}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := modalityMismatches(tc.typ, tc.by, tc.wide)
			if !reflect.DeepEqual(got, tc.expect) {
				t.Errorf("got %+v, want %+v", got, tc.expect)
			}
		})
	}
}
