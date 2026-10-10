package routingdecision

import (
	"encoding/json"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
)

func TestBuildQuestion(t *testing.T) {
	ins, crit, err := BuildQuestion("", []entity.RoutingCandidate{
		{ID: "cheap", Model: "model-a", Criteria: "simple"},
		{ID: "strong", Model: "model-b", Criteria: "hard"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if json.Unmarshal(ins, &s) != nil || s != defaultInstructions {
		t.Fatalf("instructions = %s, want the default", ins)
	}
	var m map[string]string
	if err := json.Unmarshal(crit, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 || m["cheap"] != "simple" || m["strong"] != "hard" || m[NoPreferenceID] == "" {
		t.Fatalf("criteria = %v: candidates plus the always-present no_preference option", m)
	}
}

func TestParseAnswer(t *testing.T) {
	good := `{"model":"x","answers":{"route":{"type":"choice","choice":"cheap","probabilities":{"cheap":0.8,"strong":0.2},"confidence":0.7}},"usage":{"input_tokens":120,"output_tokens":3}}`
	out, err := parseAnswer([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if out.Choice != "cheap" || out.Probabilities["cheap"] != 0.8 || out.InputTokens != 120 || out.OutputTokens != 3 {
		t.Fatalf("out = %+v", out)
	}
	// No per-option probability: fall back to the answer confidence, never to 1.
	out, err = parseAnswer([]byte(`{"answers":{"route":{"type":"choice","choice":"cheap","confidence":0.55}},"usage":{"input_tokens":1,"output_tokens":0}}`))
	if err != nil || out.Probabilities["cheap"] != 0.55 {
		t.Fatalf("fallback: %+v err=%v", out, err)
	}
	for name, bad := range map[string]string{
		"not json":         `{`,
		"no answers":       `{"answers":{}}`,
		"wrong type":       `{"answers":{"route":{"type":"score","choice":"cheap"}}}`,
		"empty choice":     `{"answers":{"route":{"type":"choice","choice":""}}}`,
		"no probability":   `{"answers":{"route":{"type":"choice","choice":"cheap"}}}`,
		"other question":   `{"answers":{"other":{"type":"choice","choice":"cheap","probabilities":{"cheap":1}}}}`,
		"answers is array": `{"answers":[]}`,
	} {
		if _, err := parseAnswer([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMapModel(t *testing.T) {
	if got := mapModel(`{"evaluator-a":"vendor-eval-1"}`, "evaluator-a"); got != "vendor-eval-1" {
		t.Errorf("got %q", got)
	}
	if got := mapModel(`{"a":"b","b":"a"}`, "a"); got != "b" {
		t.Errorf("cycle must stop, got %q", got)
	}
	for _, m := range []string{"", "{}", "not json"} {
		if got := mapModel(m, "evaluator-a"); got != "evaluator-a" {
			t.Errorf("mapping %q changed the model to %q", m, got)
		}
	}
}

func TestEvalQuota(t *testing.T) {
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"evaluator-a":0.5,"evaluator-free":0}`); err != nil {
		t.Fatal(err)
	}
	if err := ratio_setting.UpdateModelPriceByJSONString(`{"evaluator-priced":0.001}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelRatioByJSONString(`{}`)
		_ = ratio_setting.UpdateModelPriceByJSONString(`{}`)
	})
	if got := EvalQuota("evaluator-a", "default", "default", 1000); got != 500 {
		t.Errorf("1000 input tokens x 0.5 = 500, got %d", got)
	}
	if got := EvalQuota("evaluator-a", "default", "default", 1); got != 1 {
		t.Errorf("a chargeable call must not round down to free, got %d", got)
	}
	if got := EvalQuota("evaluator-free", "default", "default", 1000); got != 0 {
		t.Errorf("ratio 0 is free, got %d", got)
	}
	if got := EvalQuota("evaluator-unknown", "default", "default", 1000); got != 0 {
		t.Errorf("an unpriced model must not block routing, got %d", got)
	}
	if got := EvalQuota("evaluator-priced", "default", "default", 0); got <= 0 {
		t.Errorf("per-call price applies regardless of tokens, got %d", got)
	}
}
