package gemini

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// ---------------------------------------------------------------------------
// cleanFunctionParameters
// ---------------------------------------------------------------------------

func TestCleanFunctionParameters_Nil(t *testing.T) {
	if got := cleanFunctionParameters(nil); got != nil {
		t.Errorf("cleanFunctionParameters(nil) = %v, want nil", got)
	}
}

func TestCleanFunctionParameters_PrimitivePassthrough(t *testing.T) {
	if got := cleanFunctionParameters("plain string"); got != "plain string" {
		t.Errorf("cleanFunctionParameters(string) = %v, want unchanged", got)
	}
	if got := cleanFunctionParameters(42); got != 42 {
		t.Errorf("cleanFunctionParameters(int) = %v, want unchanged", got)
	}
}

func TestCleanFunctionParameters_StripsUnsupportedRootFields(t *testing.T) {
	input := map[string]interface{}{
		"type":                 "string",
		"default":              "x",
		"exclusiveMaximum":     10,
		"exclusiveMinimum":     0,
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"additionalProperties": false,
	}
	got := cleanFunctionParameters(input).(map[string]interface{})
	for _, key := range []string{"default", "exclusiveMaximum", "exclusiveMinimum", "$schema", "additionalProperties"} {
		if _, ok := got[key]; ok {
			t.Errorf("key %q not stripped, got %+v", key, got)
		}
	}
	if got["type"] != "string" {
		t.Errorf("type = %v, want string (must be preserved)", got["type"])
	}
}

func TestCleanFunctionParameters_DoesNotMutateOriginal(t *testing.T) {
	input := map[string]interface{}{"type": "string", "default": "x"}
	_ = cleanFunctionParameters(input)
	if _, ok := input["default"]; !ok {
		t.Error("original map was mutated: cleanFunctionParameters must operate on a copy")
	}
}

func TestCleanFunctionParameters_FormatFieldOnStringType(t *testing.T) {
	tests := []struct {
		name       string
		format     string
		wantKept   bool
	}{
		{"enum kept", "enum", true},
		{"date-time kept", "date-time", true},
		{"uuid stripped", "uuid", false},
		{"email stripped", "email", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := map[string]interface{}{"type": "string", "format": tt.format}
			got := cleanFunctionParameters(input).(map[string]interface{})
			_, has := got["format"]
			if has != tt.wantKept {
				t.Errorf("format kept = %v, want %v (format=%q)", has, tt.wantKept, tt.format)
			}
		})
	}
}

func TestCleanFunctionParameters_FormatFieldIgnoredOnNonStringType(t *testing.T) {
	input := map[string]interface{}{"type": "integer", "format": "int64"}
	got := cleanFunctionParameters(input).(map[string]interface{})
	if got["format"] != "int64" {
		t.Errorf("format = %v, want preserved for non-string type", got["format"])
	}
}

func TestCleanFunctionParameters_RecursesIntoProperties(t *testing.T) {
	input := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "default": "bob"},
		},
	}
	got := cleanFunctionParameters(input).(map[string]interface{})
	props := got["properties"].(map[string]interface{})
	nameProp := props["name"].(map[string]interface{})
	if _, ok := nameProp["default"]; ok {
		t.Errorf("nested property 'default' not stripped: %+v", nameProp)
	}
}

func TestCleanFunctionParameters_RecursesIntoItemsObject(t *testing.T) {
	input := map[string]interface{}{
		"type":  "array",
		"items": map[string]interface{}{"type": "string", "default": "x"},
	}
	got := cleanFunctionParameters(input).(map[string]interface{})
	items := got["items"].(map[string]interface{})
	if _, ok := items["default"]; ok {
		t.Errorf("items.default not stripped: %+v", items)
	}
}

func TestCleanFunctionParameters_RecursesIntoItemsArray(t *testing.T) {
	input := map[string]interface{}{
		"type": "array",
		"items": []interface{}{
			map[string]interface{}{"type": "string", "default": "a"},
			map[string]interface{}{"type": "number", "default": 1},
		},
	}
	got := cleanFunctionParameters(input).(map[string]interface{})
	items := got["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	for i, it := range items {
		m := it.(map[string]interface{})
		if _, ok := m["default"]; ok {
			t.Errorf("items[%d].default not stripped: %+v", i, m)
		}
	}
}

func TestCleanFunctionParameters_RecursesIntoCompositionKeywords(t *testing.T) {
	for _, field := range []string{"allOf", "anyOf", "oneOf"} {
		t.Run(field, func(t *testing.T) {
			input := map[string]interface{}{
				field: []interface{}{
					map[string]interface{}{"type": "string", "default": "x"},
				},
			}
			got := cleanFunctionParameters(input).(map[string]interface{})
			nested := got[field].([]interface{})
			m := nested[0].(map[string]interface{})
			if _, ok := m["default"]; ok {
				t.Errorf("%s[0].default not stripped: %+v", field, m)
			}
		})
	}
}

func TestCleanFunctionParameters_RecursesIntoPatternPropertiesDefinitionsDefs(t *testing.T) {
	input := map[string]interface{}{
		"patternProperties": map[string]interface{}{
			"^x-": map[string]interface{}{"type": "string", "default": "a"},
		},
		"definitions": map[string]interface{}{
			"Foo": map[string]interface{}{"type": "string", "default": "b"},
		},
		"$defs": map[string]interface{}{
			"Bar": map[string]interface{}{"type": "string", "default": "c"},
		},
	}
	got := cleanFunctionParameters(input).(map[string]interface{})

	pp := got["patternProperties"].(map[string]interface{})["^x-"].(map[string]interface{})
	if _, ok := pp["default"]; ok {
		t.Errorf("patternProperties nested default not stripped: %+v", pp)
	}
	defs := got["definitions"].(map[string]interface{})["Foo"].(map[string]interface{})
	if _, ok := defs["default"]; ok {
		t.Errorf("definitions nested default not stripped: %+v", defs)
	}
	dollarDefs := got["$defs"].(map[string]interface{})["Bar"].(map[string]interface{})
	if _, ok := dollarDefs["default"]; ok {
		t.Errorf("$defs nested default not stripped: %+v", dollarDefs)
	}
}

func TestCleanFunctionParameters_RecursesIntoConditionalKeywords(t *testing.T) {
	for _, field := range []string{"if", "then", "else", "not"} {
		t.Run(field, func(t *testing.T) {
			input := map[string]interface{}{
				field: map[string]interface{}{"type": "string", "default": "x"},
			}
			got := cleanFunctionParameters(input).(map[string]interface{})
			nested := got[field].(map[string]interface{})
			if _, ok := nested["default"]; ok {
				t.Errorf("%s.default not stripped: %+v", field, nested)
			}
		})
	}
}

func TestCleanFunctionParameters_TopLevelArray(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{"type": "string", "default": "a"},
		"plain",
	}
	got := cleanFunctionParameters(input).([]interface{})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	m := got[0].(map[string]interface{})
	if _, ok := m["default"]; ok {
		t.Errorf("array[0].default not stripped: %+v", m)
	}
	if got[1] != "plain" {
		t.Errorf("array[1] = %v, want unchanged 'plain'", got[1])
	}
}

// ---------------------------------------------------------------------------
// removeAdditionalPropertiesWithDepth
// ---------------------------------------------------------------------------

func TestRemoveAdditionalPropertiesWithDepth_NonMapPassthrough(t *testing.T) {
	if got := removeAdditionalPropertiesWithDepth("str", 0); got != "str" {
		t.Errorf("got %v, want unchanged 'str'", got)
	}
	if got := removeAdditionalPropertiesWithDepth(nil, 0); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestRemoveAdditionalPropertiesWithDepth_EmptyMapPassthrough(t *testing.T) {
	input := map[string]interface{}{}
	got := removeAdditionalPropertiesWithDepth(input, 0)
	if !reflect.DeepEqual(got, input) {
		t.Errorf("got %+v, want unchanged empty map", got)
	}
}

func TestRemoveAdditionalPropertiesWithDepth_DepthLimitStopsRecursion(t *testing.T) {
	input := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"title":                "should stay at depth limit",
	}
	got := removeAdditionalPropertiesWithDepth(input, 5).(map[string]interface{})
	// depth >= 5 returns schema unmodified (title/additionalProperties preserved)
	if got["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want preserved at depth limit", got["additionalProperties"])
	}
	if got["title"] != "should stay at depth limit" {
		t.Errorf("title = %v, want preserved at depth limit", got["title"])
	}
}

func TestRemoveAdditionalPropertiesWithDepth_StripsTitleAndSchema(t *testing.T) {
	input := map[string]interface{}{
		"type":    "object",
		"title":   "MySchema",
		"$schema": "http://json-schema.org/draft-07/schema#",
	}
	got := removeAdditionalPropertiesWithDepth(input, 0).(map[string]interface{})
	if _, ok := got["title"]; ok {
		t.Errorf("title not stripped: %+v", got)
	}
	if _, ok := got["$schema"]; ok {
		t.Errorf("$schema not stripped: %+v", got)
	}
}

func TestRemoveAdditionalPropertiesWithDepth_NonObjectArrayTypeReturnsEarly(t *testing.T) {
	input := map[string]interface{}{
		"type":  "string",
		"title": "should be dropped even though type isn't object/array",
	}
	got := removeAdditionalPropertiesWithDepth(input, 0).(map[string]interface{})
	// title/$schema deletion happens before the type check, so title IS gone,
	// but since type isn't object/array, no further processing (e.g. no panic,
	// returns as-is otherwise).
	if _, ok := got["title"]; ok {
		t.Errorf("title = %+v, want stripped regardless of type", got)
	}
}

func TestRemoveAdditionalPropertiesWithDepth_ObjectStripsAdditionalPropertiesAndRecurses(t *testing.T) {
	input := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"child": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": true,
				"title":                "nested",
			},
		},
	}
	got := removeAdditionalPropertiesWithDepth(input, 0).(map[string]interface{})
	if _, ok := got["additionalProperties"]; ok {
		t.Errorf("root additionalProperties not stripped: %+v", got)
	}
	child := got["properties"].(map[string]interface{})["child"].(map[string]interface{})
	if _, ok := child["additionalProperties"]; ok {
		t.Errorf("nested additionalProperties not stripped: %+v", child)
	}
	if _, ok := child["title"]; ok {
		t.Errorf("nested title not stripped: %+v", child)
	}
}

func TestRemoveAdditionalPropertiesWithDepth_ObjectRecursesIntoComposition(t *testing.T) {
	for _, field := range []string{"allOf", "anyOf", "oneOf"} {
		t.Run(field, func(t *testing.T) {
			input := map[string]interface{}{
				"type": "object",
				field: []interface{}{
					map[string]interface{}{"type": "object", "additionalProperties": false, "title": "x"},
				},
			}
			got := removeAdditionalPropertiesWithDepth(input, 0).(map[string]interface{})
			nested := got[field].([]interface{})[0].(map[string]interface{})
			if _, ok := nested["additionalProperties"]; ok {
				t.Errorf("%s[0].additionalProperties not stripped: %+v", field, nested)
			}
		})
	}
}

func TestRemoveAdditionalPropertiesWithDepth_ArrayRecursesIntoItems(t *testing.T) {
	input := map[string]interface{}{
		"type": "array",
		"items": map[string]interface{}{
			"type":                 "object",
			"additionalProperties": false,
			"title":                "item schema",
		},
	}
	got := removeAdditionalPropertiesWithDepth(input, 0).(map[string]interface{})
	items := got["items"].(map[string]interface{})
	if _, ok := items["additionalProperties"]; ok {
		t.Errorf("items.additionalProperties not stripped: %+v", items)
	}
	if _, ok := items["title"]; ok {
		t.Errorf("items.title not stripped: %+v", items)
	}
}

// ---------------------------------------------------------------------------
// unescapeString
// ---------------------------------------------------------------------------

func TestUnescapeString_ValidEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"quote", `hello \"world\"`, `hello "world"`},
		{"backslash", `a\\b`, `a\b`},
		{"forward slash", `a\/b`, `a/b`},
		{"newline", `line1\nline2`, "line1\nline2"},
		{"tab", `a\tb`, "a\tb"},
		{"carriage return", `a\rb`, "a\rb"},
		{"backspace", `a\bb`, "a\bb"},
		{"formfeed", `a\fb`, "a\fb"},
		{"single quote", `it\'s`, "it's"},
		{"no escapes", "plain text", "plain text"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := unescapeString(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("unescapeString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestUnescapeString_UnknownEscapeKeptLiteral(t *testing.T) {
	got, err := unescapeString(`a\zb`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `a\zb` {
		t.Errorf("got %q, want literal backslash preserved for unknown escape", got)
	}
}

func TestUnescapeString_UnicodeMultibyte(t *testing.T) {
	got, err := unescapeString(`你好\n世界`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "你好\n世界"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnescapeString_TrailingBackslash(t *testing.T) {
	// a lone trailing backslash sets escaped=true but the loop ends before
	// consuming another rune; nothing is appended for it (documents behavior).
	got, err := unescapeString(`abc\`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc" {
		t.Errorf("got %q, want %q (trailing lone backslash dropped)", got, "abc")
	}
}

// ---------------------------------------------------------------------------
// unescapeMapOrSlice
// ---------------------------------------------------------------------------

func TestUnescapeMapOrSlice_String(t *testing.T) {
	got := unescapeMapOrSlice(`a\nb`)
	if got != "a\nb" {
		t.Errorf("got %v, want unescaped string", got)
	}
}

func TestUnescapeMapOrSlice_NestedMap(t *testing.T) {
	input := map[string]interface{}{
		"a": `line1\nline2`,
		"b": map[string]interface{}{"c": `x\ty`},
	}
	got := unescapeMapOrSlice(input).(map[string]interface{})
	if got["a"] != "line1\nline2" {
		t.Errorf("got[a] = %v, want unescaped", got["a"])
	}
	nested := got["b"].(map[string]interface{})
	if nested["c"] != "x\ty" {
		t.Errorf("got[b][c] = %v, want unescaped", nested["c"])
	}
}

func TestUnescapeMapOrSlice_NestedSlice(t *testing.T) {
	input := []interface{}{`a\nb`, 42, map[string]interface{}{"k": `x\/y`}}
	got := unescapeMapOrSlice(input).([]interface{})
	if got[0] != "a\nb" {
		t.Errorf("got[0] = %v, want unescaped", got[0])
	}
	if got[1] != 42 {
		t.Errorf("got[1] = %v, want unchanged 42", got[1])
	}
	nested := got[2].(map[string]interface{})
	if nested["k"] != "x/y" {
		t.Errorf("got[2][k] = %v, want unescaped", nested["k"])
	}
}

func TestUnescapeMapOrSlice_NonStringPassthrough(t *testing.T) {
	if got := unescapeMapOrSlice(42); got != 42 {
		t.Errorf("got %v, want unchanged 42", got)
	}
	if got := unescapeMapOrSlice(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if got := unescapeMapOrSlice(true); got != true {
		t.Errorf("got %v, want unchanged true", got)
	}
}

// ---------------------------------------------------------------------------
// hasFunctionCallContent
// ---------------------------------------------------------------------------

func TestHasFunctionCallContent(t *testing.T) {
	tests := []struct {
		name string
		call *dto.FunctionCall
		want bool
	}{
		{"nil call", nil, false},
		{"non-empty name", &dto.FunctionCall{FunctionName: "f"}, true},
		{"whitespace-only name, nil args", &dto.FunctionCall{FunctionName: "   ", Arguments: nil}, false},
		{"empty name, nil args", &dto.FunctionCall{FunctionName: "", Arguments: nil}, false},
		{"empty name, whitespace string args", &dto.FunctionCall{FunctionName: "", Arguments: "   "}, false},
		{"empty name, non-empty string args", &dto.FunctionCall{FunctionName: "", Arguments: "x"}, true},
		{"empty name, empty map args", &dto.FunctionCall{FunctionName: "", Arguments: map[string]interface{}{}}, false},
		{"empty name, non-empty map args", &dto.FunctionCall{FunctionName: "", Arguments: map[string]interface{}{"a": 1}}, true},
		{"empty name, empty slice args", &dto.FunctionCall{FunctionName: "", Arguments: []interface{}{}}, false},
		{"empty name, non-empty slice args", &dto.FunctionCall{FunctionName: "", Arguments: []interface{}{1}}, true},
		{"empty name, other type args (number)", &dto.FunctionCall{FunctionName: "", Arguments: 42}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasFunctionCallContent(tt.call)
			if got != tt.want {
				t.Errorf("hasFunctionCallContent(%+v) = %v, want %v", tt.call, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getResponseToolCall
// ---------------------------------------------------------------------------

func TestGetResponseToolCall_MapArguments(t *testing.T) {
	part := &dto.GeminiPart{
		FunctionCall: &dto.FunctionCall{
			FunctionName: "get_weather",
			Arguments:    map[string]interface{}{"city": "SF"},
		},
	}
	got := getResponseToolCall(part)
	if got == nil {
		t.Fatal("got nil, want non-nil ToolCallResponse")
	}
	if got.Function.Name != "get_weather" {
		t.Errorf("Function.Name = %q, want %q", got.Function.Name, "get_weather")
	}
	if got.Type != "function" {
		t.Errorf("Type = %v, want %q", got.Type, "function")
	}
	if got.ID == "" || len(got.ID) < len("call_") {
		t.Errorf("ID = %q, want non-empty call_<uuid>", got.ID)
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(got.Function.Arguments), &args); err != nil {
		t.Fatalf("Arguments not valid JSON: %v (%q)", err, got.Function.Arguments)
	}
	if args["city"] != "SF" {
		t.Errorf("args[city] = %v, want SF", args["city"])
	}
}

func TestGetResponseToolCall_NonMapArguments(t *testing.T) {
	part := &dto.GeminiPart{
		FunctionCall: &dto.FunctionCall{
			FunctionName: "echo",
			Arguments:    "raw-string-arg",
		},
	}
	got := getResponseToolCall(part)
	if got == nil {
		t.Fatal("got nil, want non-nil")
	}
	if got.Function.Arguments != `"raw-string-arg"` {
		t.Errorf("Arguments = %q, want JSON-marshaled string", got.Function.Arguments)
	}
}

func TestGetResponseToolCall_UnescapesNestedEscapedText(t *testing.T) {
	part := &dto.GeminiPart{
		FunctionCall: &dto.FunctionCall{
			FunctionName: "note",
			Arguments:    map[string]interface{}{"text": `line1\nline2`},
		},
	}
	got := getResponseToolCall(part)
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(got.Function.Arguments), &args); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if args["text"] != "line1\nline2" {
		t.Errorf("args[text] = %q, want unescaped newline", args["text"])
	}
}

func TestGetResponseToolCall_NilArguments(t *testing.T) {
	part := &dto.GeminiPart{
		FunctionCall: &dto.FunctionCall{FunctionName: "noop", Arguments: nil},
	}
	got := getResponseToolCall(part)
	if got == nil {
		t.Fatal("got nil, want non-nil")
	}
	if got.Function.Arguments != "null" {
		t.Errorf("Arguments = %q, want %q for nil args", got.Function.Arguments, "null")
	}
}
