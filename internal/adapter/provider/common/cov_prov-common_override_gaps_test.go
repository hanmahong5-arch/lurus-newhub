package common

import (
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

// ---- compareEqual (via ApplyParamOverride condition mode "full") ----

func TestApplyParamOverride_ConditionFullMode(t *testing.T) {
	tests := []struct {
		name       string
		conditionV interface{}
		jsonValue  string // raw JSON literal embedded for the "checked" field
		wantSet    bool   // whether the operation should have applied (condition true)
	}{
		{"string equal", "gpt-4", `"gpt-4"`, true},
		{"string not equal", "gpt-4", `"gpt-3.5"`, false},
		{"number equal", float64(5), `5`, true},
		{"bool true equal", true, `true`, true},
		{"bool mismatch", true, `false`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"checked":` + tt.jsonValue + `,"flag":"unset"}`)
			override := map[string]interface{}{
				"operations": []interface{}{
					map[string]interface{}{
						"path": "flag",
						"mode": "set",
						"value": "matched",
						"conditions": []interface{}{
							map[string]interface{}{"path": "checked", "mode": "full", "value": tt.conditionV},
						},
					},
				},
			}
			out, err := ApplyParamOverride(input, override, nil)
			if err != nil {
				t.Fatalf("ApplyParamOverride() error = %v", err)
			}
			flag := gjson.GetBytes(out, "flag").String()
			if tt.wantSet && flag != "matched" {
				t.Errorf("flag = %q, want 'matched' (condition should have been true)", flag)
			}
			if !tt.wantSet && flag != "unset" {
				t.Errorf("flag = %q, want 'unset' (condition should have been false)", flag)
			}
		})
	}
}

func TestApplyParamOverride_ConditionFullMode_NullHandling(t *testing.T) {
	// Both null -> equal; one null one not -> not equal.
	input := []byte(`{"checked":null,"flag":"unset"}`)
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path": "flag", "mode": "set", "value": "matched",
				"conditions": []interface{}{
					map[string]interface{}{"path": "checked", "mode": "full", "value": nil},
				},
			},
		},
	}
	out, err := ApplyParamOverride(input, override, nil)
	if err != nil {
		t.Fatalf("ApplyParamOverride() error = %v", err)
	}
	if gjson.GetBytes(out, "flag").String() != "matched" {
		t.Errorf("expected null == null to match, got flag=%s", gjson.GetBytes(out, "flag").Raw)
	}
}

func TestApplyParamOverride_ConditionFullMode_TypeMismatchIsError(t *testing.T) {
	input := []byte(`{"checked":"a-string","flag":"unset"}`)
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path": "flag", "mode": "set", "value": "matched",
				"conditions": []interface{}{
					map[string]interface{}{"path": "checked", "mode": "full", "value": float64(1)},
				},
			},
		},
	}
	_, err := ApplyParamOverride(input, override, nil)
	if err == nil {
		t.Fatal("expected an error comparing a string field against a numeric condition value (type mismatch)")
	}
}

// ---- compareGjsonValues: suffix / contains / gte / lt / lte ----

func TestApplyParamOverride_ConditionSuffixContainsAndNumericBounds(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		value   interface{}
		checked string
		wantSet bool
	}{
		{"suffix match", "suffix", "-latest", `"gpt-4-latest"`, true},
		{"suffix no match", "suffix", "-latest", `"gpt-4"`, false},
		{"contains match", "contains", "4o", `"gpt-4o-mini"`, true},
		{"contains no match", "contains", "4o", `"gpt-3.5"`, false},
		{"gte equal passes", "gte", float64(5), `5`, true},
		{"gte greater passes", "gte", float64(5), `6`, true},
		{"gte less fails", "gte", float64(5), `4`, false},
		{"lt passes", "lt", float64(5), `4`, true},
		{"lt equal fails", "lt", float64(5), `5`, false},
		{"lte equal passes", "lte", float64(5), `5`, true},
		{"lte greater fails", "lte", float64(5), `6`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"checked":` + tt.checked + `,"flag":"unset"}`)
			override := map[string]interface{}{
				"operations": []interface{}{
					map[string]interface{}{
						"path": "flag", "mode": "set", "value": "matched",
						"conditions": []interface{}{
							map[string]interface{}{"path": "checked", "mode": tt.mode, "value": tt.value},
						},
					},
				},
			}
			out, err := ApplyParamOverride(input, override, nil)
			if err != nil {
				t.Fatalf("ApplyParamOverride() error = %v", err)
			}
			flag := gjson.GetBytes(out, "flag").String()
			got := flag == "matched"
			if got != tt.wantSet {
				t.Errorf("condition mode=%s value=%v checked=%s: matched=%v, want %v", tt.mode, tt.value, tt.checked, got, tt.wantSet)
			}
		})
	}
}

func TestApplyParamOverride_ConditionNumericTypeMismatchIsError(t *testing.T) {
	input := []byte(`{"checked":"not-a-number","flag":"unset"}`)
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path": "flag", "mode": "set", "value": "matched",
				"conditions": []interface{}{
					map[string]interface{}{"path": "checked", "mode": "gt", "value": float64(1)},
				},
			},
		},
	}
	_, err := ApplyParamOverride(input, override, nil)
	if err == nil {
		t.Fatal("expected an error for a numeric comparison against a non-numeric field")
	}
}

// ---- applyOperationsLegacy (via ApplyParamOverride with a plain map) ----

func TestApplyParamOverride_LegacyPlainMapOverwritesTopLevelKeys(t *testing.T) {
	input := []byte(`{"model":"gpt-3.5","temperature":0.5,"stream":false}`)
	override := map[string]interface{}{
		"model":  "gpt-4",
		"stream": true,
	}
	out, err := ApplyParamOverride(input, override, nil)
	if err != nil {
		t.Fatalf("ApplyParamOverride() error = %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if got["model"] != "gpt-4" {
		t.Errorf("model = %v, want overwritten to gpt-4", got["model"])
	}
	if got["stream"] != true {
		t.Errorf("stream = %v, want overwritten to true", got["stream"])
	}
	if got["temperature"] != 0.5 {
		t.Errorf("temperature = %v, want untouched 0.5 (legacy mode is a shallow merge)", got["temperature"])
	}
}

func TestApplyParamOverride_LegacyPlainMap_MalformedJSONErrors(t *testing.T) {
	_, err := ApplyParamOverride([]byte(`{not valid`), map[string]interface{}{"model": "gpt-4"}, nil)
	if err == nil {
		t.Fatal("expected error unmarshalling malformed JSON in the legacy override path")
	}
}

func TestApplyParamOverride_EmptyOverrideIsNoop(t *testing.T) {
	input := []byte(`{"model":"gpt-4"}`)
	out, err := ApplyParamOverride(input, nil, nil)
	if err != nil {
		t.Fatalf("ApplyParamOverride() error = %v", err)
	}
	if string(out) != string(input) {
		t.Errorf("out = %s, want input echoed back unchanged for an empty override map", out)
	}
}

// ---- BuildParamOverrideContext ----

func TestBuildParamOverrideContext(t *testing.T) {
	tests := []struct {
		name string
		info *RelayInfo
		want map[string]interface{}
	}{
		{
			name: "nil info",
			info: nil,
			want: nil,
		},
		{
			name: "nil ChannelMeta",
			info: &RelayInfo{},
			want: nil,
		},
		{
			name: "only upstream model set",
			info: &RelayInfo{ChannelMeta: &ChannelMeta{UpstreamModelName: "gpt-4o-mapped"}},
			want: map[string]interface{}{"model": "gpt-4o-mapped", "upstream_model": "gpt-4o-mapped"},
		},
		{
			name: "only origin model set",
			info: &RelayInfo{OriginModelName: "gpt-4o", ChannelMeta: &ChannelMeta{}},
			want: map[string]interface{}{"model": "gpt-4o", "original_model": "gpt-4o"},
		},
		{
			name: "both set: model prefers upstream, original_model always origin",
			info: &RelayInfo{
				OriginModelName: "gpt-4o-alias",
				ChannelMeta:     &ChannelMeta{UpstreamModelName: "gpt-4o-mapped"},
			},
			want: map[string]interface{}{
				"model":          "gpt-4o-mapped",
				"upstream_model": "gpt-4o-mapped",
				"original_model": "gpt-4o-alias",
			},
		},
		{
			name: "both empty strings -> nil",
			info: &RelayInfo{ChannelMeta: &ChannelMeta{}},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildParamOverrideContext(tt.info)
			if tt.want == nil {
				if got != nil {
					t.Errorf("got = %+v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got = %+v, want %+v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("got[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestApplyParamOverride_ConditionUsesContextWhenFieldMissingFromBody(t *testing.T) {
	// checkSingleCondition falls back to contextJSON when the path is absent
	// from the request body — this is how BuildParamOverrideContext's "model"
	// key becomes usable inside operation conditions.
	input := []byte(`{"flag":"unset"}`)
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path": "flag", "mode": "set", "value": "matched",
				"conditions": []interface{}{
					map[string]interface{}{"path": "model", "mode": "full", "value": "gpt-4o"},
				},
			},
		},
	}
	out, err := ApplyParamOverride(input, override, map[string]interface{}{"model": "gpt-4o"})
	if err != nil {
		t.Fatalf("ApplyParamOverride() error = %v", err)
	}
	if gjson.GetBytes(out, "flag").String() != "matched" {
		t.Errorf("expected condition to match via context fallback, got flag=%s", gjson.GetBytes(out, "flag").Raw)
	}
}
