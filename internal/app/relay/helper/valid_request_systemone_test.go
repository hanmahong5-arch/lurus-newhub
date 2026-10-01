package helper

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

const soState = `"state":{"body":"I was billed twice"}`
const soChoiceQ = `{"type":"choice","instructions":"which dept?","criteria":{"billing":"refunds","tech":"bugs"}}`

func soBody(model, state, questions string) string {
	parts := []string{}
	if model != "" {
		parts = append(parts, `"model":"`+model+`"`)
	}
	if state != "" {
		parts = append(parts, state)
	}
	if questions != "" {
		parts = append(parts, `"questions":`+questions)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func soMany(n int, tmpl string) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(tmpl, i)
	}
	return strings.Join(items, ",")
}

func TestGetAndValidateSystemOneRequest_Valid(t *testing.T) {
	cases := map[string]string{
		"choice object criteria": soBody("model-a", soState, `{"dept":`+soChoiceQ+`}`),
		"choice list criteria":   soBody("model-a", soState, `{"dept":{"type":"choice","instructions":"x","criteria":["a","b"]}}`),
		"choice single option":   soBody("model-a", soState, `{"dept":{"type":"choice","instructions":"x","criteria":{"only":"one"}}}`),
		"score two levels":       soBody("model-a", soState, `{"dept":{"type":"score","instructions":"x","criteria":["low","high"]}}`),
		"noul no criteria":       soBody("model-a", soState, `{"dept":{"type":"noul","instructions":"x"}}`),
		"empty string state":     soBody("model-a", `"state":""`, `{"dept":`+soChoiceQ+`}`),
		"string state":           soBody("model-a", `"state":"text"`, `{"dept":`+soChoiceQ+`}`),
		"array state":            soBody("model-a", `"state":[{"a":1}]`, `{"dept":`+soChoiceQ+`}`),
		"unknown top-level":      `{"model":"model-a",` + soState + `,"temperature":0.2,"foo":"bar","questions":{"dept":` + soChoiceQ + `}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := newJSONCtx("POST", "/v1/systemone", body)
			req, err := GetAndValidateSystemOneRequest(c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.Model != "model-a" || len(req.Questions) != 1 {
				t.Fatalf("parsed wrong: model=%q questions=%d", req.Model, len(req.Questions))
			}
		})
	}
}

func TestGetAndValidateSystemOneRequest_Rejects(t *testing.T) {
	q := `{"dept":` + soChoiceQ + `}`
	hook := func(field string) string {
		return `{"model":"model-a",` + soState + `,"` + field + `":1,"questions":` + q + `}`
	}
	cases := []struct {
		name, body, wantMsg string
	}{
		{"invalid json", `{"model":`, ""},
		{"missing model", soBody("", soState, q), "model"},
		{"empty model", `{"model":"",` + soState + `,"questions":` + q + `}`, "model"},
		{"missing state", soBody("model-a", "", q), "state"},
		{"null state", soBody("model-a", `"state":null`, q), "state"},
		{"questions missing", soBody("model-a", soState, ""), "questions"},
		{"questions array", soBody("model-a", soState, `[1,2]`), "questions"},
		{"questions string", soBody("model-a", soState, `"x"`), "questions"},
		{"questions null", soBody("model-a", soState, `null`), "questions"},
		{"questions empty", soBody("model-a", soState, `{}`), "questions"},
		{"question string", soBody("model-a", soState, `{"q":"choice"}`), "'q'"},
		{"question null", soBody("model-a", soState, `{"q":null}`), "'q'"},
		{"unknown type", soBody("model-a", soState, `{"q":{"type":"bogus","instructions":"x"}}`), "bogus"},
		{"type wrong case", soBody("model-a", soState, `{"q":{"type":"NOUL","instructions":"x"}}`), "NOUL"},
		{"type missing", soBody("model-a", soState, `{"q":{"instructions":"x"}}`), "type"},
		{"instructions missing", soBody("model-a", soState, `{"q":{"type":"noul"}}`), "instructions"},
		{"instructions empty", soBody("model-a", soState, `{"q":{"type":"noul","instructions":""}}`), "instructions"},
		{"instructions null", soBody("model-a", soState, `{"q":{"type":"noul","instructions":null}}`), "instructions"},
		{"choice criteria missing", soBody("model-a", soState, `{"q":{"type":"choice","instructions":"x"}}`), "criteria"},
		{"choice criteria empty object", soBody("model-a", soState, `{"q":{"type":"choice","instructions":"x","criteria":{}}}`), "criteria"},
		{"choice criteria empty array", soBody("model-a", soState, `{"q":{"type":"choice","instructions":"x","criteria":[]}}`), "criteria"},
		{"choice criteria string", soBody("model-a", soState, `{"q":{"type":"choice","instructions":"x","criteria":"a"}}`), "criteria"},
		{"score criteria missing", soBody("model-a", soState, `{"q":{"type":"score","instructions":"x"}}`), "criteria"},
		{"score criteria object", soBody("model-a", soState, `{"q":{"type":"score","instructions":"x","criteria":{"a":"b"}}}`), "criteria"},
		{"score one level", soBody("model-a", soState, `{"q":{"type":"score","instructions":"x","criteria":["only"]}}`), "2"},
		{"batch states", `{"model":"model-a","states":["a","b"],"questions":` + q + `}`, "states"},
		{"batch states with state", `{"model":"model-a",` + soState + `,"states":["a"],"questions":` + q + `}`, "states"},
		{"hooks", hook("hooks"), "hooks"},
		{"on_predict_start", hook("on_predict_start"), "on_predict_start"},
		{"on_predict_end", hook("on_predict_end"), "on_predict_end"},
		{"hooks_raise", hook("hooks_raise"), "hooks_raise"},
		{"hooks_timeout", hook("hooks_timeout"), "hooks_timeout"},
		{"65 questions", soBody("model-a", soState, "{"+soMany(65, `"q%d":`+soChoiceQ)+"}"), "64"},
		{"256 choice options", soBody("model-a", soState, `{"q":{"type":"choice","instructions":"x","criteria":{`+soMany(256, `"l%d":"d"`)+`}}}`), "255"},
		{"256 choice options as list", soBody("model-a", soState, `{"q":{"type":"choice","instructions":"x","criteria":[`+soMany(256, `"l%d"`)+`]}}`), "255"},
		{"11 score levels", soBody("model-a", soState, `{"q":{"type":"score","instructions":"x","criteria":[`+soMany(11, `"l%d"`)+`]}}`), "10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newJSONCtx("POST", "/v1/systemone", tc.body)
			req, err := GetAndValidateSystemOneRequest(c)
			if err == nil {
				t.Fatalf("expected an error, got request %+v", req)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q should mention %q", err.Error(), tc.wantMsg)
			}
			// A malformed body must come back as a plain error so Relay
			// answers 400: a typed NewAPIError keeps NewError's default 500.
			var typed *types.NewAPIError
			if errors.As(err, &typed) {
				t.Errorf("validation must return a plain error so Relay answers 400, got typed %T", err)
			}
		})
	}
}

func TestGetAndValidateSystemOneRequest_LimitsAtBoundaryAccepted(t *testing.T) {
	cases := map[string]string{
		"64 questions":       soBody("model-a", soState, "{"+soMany(64, `"q%d":`+soChoiceQ)+"}"),
		"255 choice options": soBody("model-a", soState, `{"q":{"type":"choice","instructions":"x","criteria":{`+soMany(255, `"l%d":"d"`)+`}}}`),
		"10 score levels":    soBody("model-a", soState, `{"q":{"type":"score","instructions":"x","criteria":[`+soMany(10, `"l%d"`)+`]}}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c, _ := newJSONCtx("POST", "/v1/systemone", body)
			if _, err := GetAndValidateSystemOneRequest(c); err != nil {
				t.Fatalf("boundary must be accepted: %v", err)
			}
		})
	}
}

func TestGetAndValidateRequest_SystemOneFormat(t *testing.T) {
	c, _ := newJSONCtx("POST", "/v1/systemone", soBody("model-a", soState, `{"dept":`+soChoiceQ+`}`))
	req, err := GetAndValidateRequest(c, types.RelayFormatSystemOne)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if req.IsStream(c) {
		t.Error("system one never streams")
	}
}

// Validation reads the body reusably: the distributor and the retry loop read
// it again after us.
func TestGetAndValidateSystemOneRequest_BodyStaysReadable(t *testing.T) {
	body := soBody("model-a", soState, `{"dept":`+soChoiceQ+`}`)
	c, _ := newJSONCtx("POST", "/v1/systemone", body)
	if _, err := GetAndValidateSystemOneRequest(c); err != nil {
		t.Fatal(err)
	}
	again, err := common.GetRequestBody(c)
	if err != nil || string(again) != body {
		t.Fatalf("body not reusable: %q %v", again, err)
	}
}
