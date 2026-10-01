package systemone

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// fullRequest is what a caller can send: every modelled field, including the
// self-hosted extras and the question-level extras.
func fullRequest() *dto.SystemOneRequest {
	var req dto.SystemOneRequest
	body := `{
		"state": {"body": "refund me"},
		"model": "laya-english",
		"lang": "en",
		"min_confidence": 0,
		"max_len": 512,
		"head_max_len": 128,
		"hooks": {"on_predict_start": "x"},
		"temperature": 0.2,
		"questions": {
			"dept": {"type": "choice", "instructions": "Which?", "criteria": {"a": "x", "b": "y"}},
			"churn": {"type": "noul", "instructions": "Churn?", "labels": ["no", "yes"], "option_order": ["yes", "no"]}
		}
	}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		panic(err)
	}
	return &req
}

func convertToMap(t *testing.T, channelType int, upstreamModel string, req *dto.SystemOneRequest) map[string]json.RawMessage {
	t.Helper()
	info := infoFor(channelType, "http://x", "k")
	info.UpstreamModelName = upstreamModel
	got, err := (&Adaptor{}).ConvertSystemOneRequest(nil, info, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestConvertSystemOneRequest_TypeSafeGetsOnlyStateModelQuestions(t *testing.T) {
	m := convertToMap(t, constant.ChannelTypeTypeSafe, "jev-1.13.0", fullRequest())
	if want := []string{"model", "questions", "state"}; !reflect.DeepEqual(keysOf(m), want) {
		t.Fatalf("hosted body keys = %v, want exactly %v", keysOf(m), want)
	}
	// The mapped upstream name is forwarded, not the public one.
	if string(m["model"]) != `"jev-1.13.0"` {
		t.Errorf("model = %s", m["model"])
	}
	// Question-level self-hosted extras must not reach the hosted API either.
	var qs map[string]map[string]json.RawMessage
	if err := json.Unmarshal(m["questions"], &qs); err != nil {
		t.Fatal(err)
	}
	if want := []string{"criteria", "instructions", "type"}; !reflect.DeepEqual(keysOf(qs["dept"]), want) {
		t.Errorf("choice question keys = %v, want %v", keysOf(qs["dept"]), want)
	}
	if want := []string{"instructions", "type"}; !reflect.DeepEqual(keysOf(qs["churn"]), want) {
		t.Errorf("noul question keys = %v, want %v (labels/option_order are self-hosted only)", keysOf(qs["churn"]), want)
	}
}

func TestConvertSystemOneRequest_CompatibleKeepsExtras(t *testing.T) {
	m := convertToMap(t, constant.ChannelTypeSystemOneCompatible, "english", fullRequest())
	want := []string{"head_max_len", "lang", "max_len", "min_confidence", "model", "questions", "state"}
	if !reflect.DeepEqual(keysOf(m), want) {
		t.Fatalf("compatible body keys = %v, want %v (unknown fields such as hooks/temperature are dropped)", keysOf(m), want)
	}
	if string(m["model"]) != `"english"` {
		t.Errorf("model = %s", m["model"])
	}
	// An explicit zero is "caller sent 0", not "not sent".
	if string(m["min_confidence"]) != "0" {
		t.Errorf("min_confidence = %s, want 0 preserved", m["min_confidence"])
	}
	if string(m["max_len"]) != "512" || string(m["head_max_len"]) != "128" || string(m["lang"]) != `"en"` {
		t.Errorf("extras = max_len %s head_max_len %s lang %s", m["max_len"], m["head_max_len"], m["lang"])
	}
	var qs map[string]map[string]json.RawMessage
	if err := json.Unmarshal(m["questions"], &qs); err != nil {
		t.Fatal(err)
	}
	if _, ok := qs["churn"]["labels"]; !ok {
		t.Errorf("self-hosted noul labels were dropped: %v", keysOf(qs["churn"]))
	}
}

func TestConvertSystemOneRequest_CompatibleOmitsUnsetExtras(t *testing.T) {
	req := fullRequest()
	req.Lang, req.MinConfidence, req.MaxLen, req.HeadMaxLen = nil, nil, nil, nil
	m := convertToMap(t, constant.ChannelTypeSystemOneCompatible, "english", req)
	if want := []string{"model", "questions", "state"}; !reflect.DeepEqual(keysOf(m), want) {
		t.Fatalf("keys = %v, want %v", keysOf(m), want)
	}
}

func TestConvertSystemOneRequest_ModelFallsBackToRequestModel(t *testing.T) {
	m := convertToMap(t, constant.ChannelTypeTypeSafe, "", fullRequest())
	if string(m["model"]) != `"laya-english"` {
		t.Errorf("model = %s, want the request's own model when no upstream name is set", m["model"])
	}
}

func TestConvertSystemOneRequest_StatePassesThroughVerbatim(t *testing.T) {
	for _, state := range []string{`"plain text"`, `{"a":[1,{"b":null}]}`, `["x","y"]`} {
		req := fullRequest()
		req.State = json.RawMessage(state)
		m := convertToMap(t, constant.ChannelTypeTypeSafe, "jev-latest", req)
		if string(m["state"]) != state {
			t.Errorf("state = %s, want %s", m["state"], state)
		}
	}
}
