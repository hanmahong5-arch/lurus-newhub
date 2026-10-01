package dto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemOneRequest_StateShapesRoundTrip(t *testing.T) {
	for name, state := range map[string]string{
		"string": `"order #1 is late"`,
		"object": `{"a":1,"b":["x"]}`,
		"array":  `[{"role":"user","content":"hi"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			in := `{"state":` + state + `,"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"urgent?"}}}`
			var req SystemOneRequest
			if err := json.Unmarshal([]byte(in), &req); err != nil {
				t.Fatal(err)
			}
			if string(req.State) != state {
				t.Fatalf("state = %s, want %s", req.State, state)
			}
			out, err := json.Marshal(&req)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), `"state":`+state) {
				t.Fatalf("marshal lost the state: %s", out)
			}
			if strings.Contains(string(out), "lang") || strings.Contains(string(out), "max_len") || strings.Contains(string(out), "min_confidence") {
				t.Fatalf("unset extras must be omitted: %s", out)
			}
		})
	}
}

func TestSystemOneRequest_ExtrasAndUnknownFields(t *testing.T) {
	in := `{"state":"s","model":"laya-auto","lang":"zh","min_confidence":0,"max_len":512,"head_max_len":128,
	  "hooks":"x","task":"y","questions":{"q":{"type":"choice","criteria":["a","b"],"labels":["a"],"option_order":[1,0],"future":1}}}`
	var req SystemOneRequest
	if err := json.Unmarshal([]byte(in), &req); err != nil {
		t.Fatal(err)
	}
	if req.Lang == nil || *req.Lang != "zh" || req.MinConfidence == nil || *req.MinConfidence != 0 ||
		req.MaxLen == nil || *req.MaxLen != 512 || req.HeadMaxLen == nil || *req.HeadMaxLen != 128 {
		t.Fatalf("extras not decoded (a zero min_confidence must stay distinguishable from unset): %+v", req)
	}
	q := req.Questions["q"]
	if string(q.Criteria) != `["a","b"]` || string(q.Labels) != `["a"]` || string(q.OptionOrder) != `[1,0]` {
		t.Fatalf("per-question optional fields lost: %+v", q)
	}
	out, err := json.Marshal(&req)
	if err != nil {
		t.Fatal(err)
	}
	for _, dropped := range []string{"hooks", "task", "future"} {
		if strings.Contains(string(out), dropped) {
			t.Errorf("unknown field %q survived re-marshal: %s", dropped, out)
		}
	}
	if !strings.Contains(string(out), `"min_confidence":0`) {
		t.Errorf("min_confidence 0 dropped: %s", out)
	}
}

func TestSystemOneRequest_Interface(t *testing.T) {
	var r Request = &SystemOneRequest{}
	if r.IsStream(nil) {
		t.Fatal("system one never streams")
	}
	r.SetModelName("")
	r.SetModelName("jev-1.13.0")
	if got := r.(*SystemOneRequest).Model; got != "jev-1.13.0" {
		t.Fatalf("model = %q", got)
	}
	r.SetModelName("")
	if got := r.(*SystemOneRequest).Model; got != "jev-1.13.0" {
		t.Fatalf("empty SetModelName must not clear the model, got %q", got)
	}
}

// Pre-consume estimates must be stable: the same request always yields the
// same text, questions in id order, instructions before criteria, string
// values unquoted and structured values kept as their JSON text.
func TestSystemOneRequest_TokenMetaContentAndOrder(t *testing.T) {
	in := `{"state":"STATE","model":"m","questions":{
	  "b":{"type":"choice","instructions":"INS-B","criteria":{"yes":"CRIT-B"}},
	  "a":{"type":"score","instructions":["INS-A"],"criteria":["LOW","HIGH"]},
	  "c":{"type":"noul"}}}`
	var req SystemOneRequest
	if err := json.Unmarshal([]byte(in), &req); err != nil {
		t.Fatal(err)
	}
	want := "STATE\n" + `["INS-A"]` + "\n" + `["LOW","HIGH"]` + "\nINS-B\n" + `{"yes":"CRIT-B"}`
	for i := 0; i < 20; i++ { // map iteration order is random; the text must not be
		if got := req.GetTokenCountMeta().CombineText; got != want {
			t.Fatalf("CombineText = %q, want %q", got, want)
		}
	}
	var obj SystemOneRequest
	if err := json.Unmarshal([]byte(`{"state":{"k":"v"},"model":"m","questions":{}}`), &obj); err != nil {
		t.Fatal(err)
	}
	if got := obj.GetTokenCountMeta().CombineText; got != `{"k":"v"}` {
		t.Fatalf("object state text = %q", got)
	}
}

func TestSystemOneResponse_RoundTripKeepsAnswersVerbatim(t *testing.T) {
	in := `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.5,"answer_confidence":0.9}},"usage":{"input_tokens":123,"output_tokens":0,"truncated":true}}`
	var resp SystemOneResponse
	if err := json.Unmarshal([]byte(in), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Usage.InputTokens != 123 || resp.Usage.OutputTokens != 0 || !resp.Usage.Truncated {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	out, err := json.Marshal(&resp)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Fatalf("round trip changed the payload:\n got %s\nwant %s", out, in)
	}
	var plain SystemOneResponse
	if err := json.Unmarshal([]byte(`{"model":"m","answers":{},"usage":{"input_tokens":1,"output_tokens":0}}`), &plain); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(&plain)
	if strings.Contains(string(b), "truncated") {
		t.Fatalf("truncated must be omitted when false: %s", b)
	}
}
