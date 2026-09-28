package claude

// structured_output_test.go — an OpenAI-wire caller's response_format must
// reach an Anthropic-wire upstream, not vanish.
//
// Until 2026-09-28 RequestOpenAI2ClaudeMessage never read
// textRequest.ResponseFormat: a client asking for `json_schema` got free
// text back with a 200, and the only way to notice was a JSON parse error on
// its side. Gemini and Ollama had handled the same field for months.
//
// Anthropic's GA shape is `output_config.format` (no beta header); it
// requires `additionalProperties:false` on every object and rejects the
// numeric / length constraints below with a 400, so the schema is normalised
// the way Anthropic's own SDKs do it (constraint stripped, kept as prose in
// the description). `json_object` has no native counterpart: it becomes a
// trailing system instruction, the same thing OpenAI's own docs ask the
// caller to write.
//
// Mutation oracle: drop the applyResponseFormat call and the first three
// tests fail on a nil OutputConfig / missing hint; drop the
// additionalProperties default in normaliseClaudeSchema and the nested
// object assertion fails; strip the container special-case and the
// keyword-named property test fails.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

func structuredRequest(rf *dto.ResponseFormat, system string) dto.GeneralOpenAIRequest {
	msgs := []dto.Message{}
	if system != "" {
		msgs = append(msgs, dto.Message{Role: "system", Content: system})
	}
	msgs = append(msgs, dto.Message{Role: "user", Content: "Extract the contact."})
	return dto.GeneralOpenAIRequest{
		Model:          "claude-sonnet-4-5-20250929",
		Messages:       msgs,
		ResponseFormat: rf,
	}
}

func jsonSchemaFormat(t *testing.T, schema any) *dto.ResponseFormat {
	t.Helper()
	raw, err := json.Marshal(dto.FormatJsonSchema{Name: "contact", Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	return &dto.ResponseFormat{Type: "json_schema", JsonSchema: raw}
}

func decodeOutputConfig(t *testing.T, req *dto.ClaudeRequest) map[string]any {
	t.Helper()
	if len(req.OutputConfig) == 0 {
		t.Fatal("output_config is empty: response_format was dropped on the floor")
	}
	var cfg map[string]any
	if err := json.Unmarshal(req.OutputConfig, &cfg); err != nil {
		t.Fatalf("output_config is not JSON: %v", err)
	}
	return cfg
}

func TestRequestOpenAI2ClaudeMessage_JSONSchema_BecomesOutputConfigFormat(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"email": map[string]any{"type": "string", "format": "email"},
		},
		"required":             []any{"name", "email"},
		"additionalProperties": false,
	}
	c := newTestGinContext()
	got, err := RequestOpenAI2ClaudeMessage(c, structuredRequest(jsonSchemaFormat(t, schema), "You extract contacts."))
	if err != nil {
		t.Fatal(err)
	}

	cfg := decodeOutputConfig(t, got)
	format, _ := cfg["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("output_config.format.type = %v, want json_schema", format["type"])
	}
	wantSchema, _ := json.Marshal(schema)
	gotSchema, _ := json.Marshal(format["schema"])
	if string(wantSchema) != string(gotSchema) {
		t.Fatalf("schema was altered although it already satisfied Anthropic:\n got %s\nwant %s", gotSchema, wantSchema)
	}
	if len(got.OutputFormat) != 0 {
		t.Fatal("the deprecated top-level output_format must not be sent; it needs a beta header the caller never set")
	}

	// The wire body: output_config present, no OpenAI leftovers, and no
	// json_object hint smuggled into system for a schema request.
	body, _ := json.Marshal(got)
	if !strings.Contains(string(body), `"output_config":{"format":{`) || !strings.Contains(string(body), `"type":"json_schema"}}`) {
		t.Fatalf("wire body lacks output_config.format: %s", body)
	}
	if strings.Contains(string(body), "response_format") {
		t.Fatalf("OpenAI response_format leaked onto the Anthropic wire: %s", body)
	}
	sys, _ := got.System.([]dto.ClaudeMediaMessage)
	if len(sys) != 1 || *sys[0].Text != "You extract contacts." {
		t.Fatalf("system was rewritten for a json_schema request: %+v", sys)
	}
}

func TestRequestOpenAI2ClaudeMessage_JSONSchema_NormalisedTheWayAnthropicSDKsDoIt(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"age": map[string]any{"type": "integer", "minimum": 0, "maximum": 150, "description": "Age in years."},
			"tag": map[string]any{"type": "string", "minLength": 1, "maxLength": 32},
			"address": map[string]any{ // nested object without additionalProperties
				"type":       "object",
				"properties": map[string]any{"city": map[string]any{"type": "string"}},
			},
			"phones": map[string]any{
				"type":     "array",
				"minItems": 2,
				"items":    map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "string"}}},
			},
		},
		"required": []any{"age"},
	}
	c := newTestGinContext()
	got, err := RequestOpenAI2ClaudeMessage(c, structuredRequest(jsonSchemaFormat(t, schema), ""))
	if err != nil {
		t.Fatal(err)
	}
	format := decodeOutputConfig(t, got)["format"].(map[string]any)
	out := format["schema"].(map[string]any)

	if out["additionalProperties"] != false {
		t.Fatalf("root object must get additionalProperties:false, got %v", out["additionalProperties"])
	}
	props := out["properties"].(map[string]any)
	address := props["address"].(map[string]any)
	if address["additionalProperties"] != false {
		t.Fatalf("nested object must get additionalProperties:false, got %v", address["additionalProperties"])
	}
	items := props["phones"].(map[string]any)["items"].(map[string]any)
	if items["additionalProperties"] != false {
		t.Fatalf("array item object must get additionalProperties:false, got %v", items["additionalProperties"])
	}
	if _, has := props["phones"].(map[string]any)["minItems"]; has {
		t.Fatal("minItems > 1 is rejected upstream and must be stripped")
	}

	age := props["age"].(map[string]any)
	for _, k := range []string{"minimum", "maximum"} {
		if _, has := age[k]; has {
			t.Fatalf("%s must be stripped (Anthropic 400s on it)", k)
		}
	}
	desc, _ := age["description"].(string)
	if !strings.HasPrefix(desc, "Age in years.") || !strings.Contains(desc, "minimum 0") || !strings.Contains(desc, "maximum 150") {
		t.Fatalf("stripped constraints must survive as prose in the description, got %q", desc)
	}
	tag := props["tag"].(map[string]any)
	if _, has := tag["minLength"]; has {
		t.Fatal("minLength must be stripped")
	}
	if d, _ := tag["description"].(string); !strings.Contains(d, "minLength 1") || !strings.Contains(d, "maxLength 32") {
		t.Fatalf("a property without description still gets the constraints as prose, got %q", d)
	}
}

func TestRequestOpenAI2ClaudeMessage_JSONSchema_PropertyNamedLikeAKeywordSurvives(t *testing.T) {
	// `properties` and `$defs` map NAMES to schemas; a property called
	// "minimum" is data, not a constraint, and must not be stripped.
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"minimum":    map[string]any{"type": "number"},
			"properties": map[string]any{"type": "string"},
		},
		"$defs": map[string]any{
			"maxLength": map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}},
		},
	}
	got, err := RequestOpenAI2ClaudeMessage(newTestGinContext(), structuredRequest(jsonSchemaFormat(t, schema), ""))
	if err != nil {
		t.Fatal(err)
	}
	out := decodeOutputConfig(t, got)["format"].(map[string]any)["schema"].(map[string]any)
	props := out["properties"].(map[string]any)
	if _, ok := props["minimum"]; !ok {
		t.Fatal("property named minimum was stripped as if it were a constraint")
	}
	if _, ok := props["properties"]; !ok {
		t.Fatal("property named properties was lost")
	}
	if _, has := props["additionalProperties"]; has {
		t.Fatal("the properties container is not a schema and must not get additionalProperties")
	}
	if _, has := out["description"]; has {
		t.Fatalf("no constraints were stripped, so no note may be added, got %v", out["description"])
	}
	def := out["$defs"].(map[string]any)["maxLength"].(map[string]any)
	if def["additionalProperties"] != false {
		t.Fatal("a definition named like a keyword is still an object schema and gets additionalProperties:false")
	}
}

func TestRequestOpenAI2ClaudeMessage_JSONObject_BecomesSystemInstruction(t *testing.T) {
	c := newTestGinContext()
	got, err := RequestOpenAI2ClaudeMessage(c, structuredRequest(&dto.ResponseFormat{Type: "json_object"}, "Be terse."))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.OutputConfig) != 0 {
		t.Fatalf("json_object has no schema; output_config must stay empty, got %s", got.OutputConfig)
	}
	sys, _ := got.System.([]dto.ClaudeMediaMessage)
	if len(sys) != 2 {
		t.Fatalf("want caller system + one JSON instruction, got %d blocks: %+v", len(sys), sys)
	}
	if *sys[0].Text != "Be terse." {
		t.Fatalf("caller's system prompt must stay first (prompt-cache prefix), got %q", *sys[0].Text)
	}
	if !strings.Contains(*sys[1].Text, "JSON object") {
		t.Fatalf("trailing block must instruct JSON-only output, got %q", *sys[1].Text)
	}

	// Without a caller system prompt the instruction is the whole system.
	got, err = RequestOpenAI2ClaudeMessage(newTestGinContext(), structuredRequest(&dto.ResponseFormat{Type: "json_object"}, ""))
	if err != nil {
		t.Fatal(err)
	}
	sys, _ = got.System.([]dto.ClaudeMediaMessage)
	if len(sys) != 1 || !strings.Contains(*sys[0].Text, "JSON object") {
		t.Fatalf("json_object without a system prompt must still carry the instruction, got %+v", sys)
	}
}

func TestRequestOpenAI2ClaudeMessage_NoOrTextResponseFormat_IsANoOp(t *testing.T) {
	for _, rf := range []*dto.ResponseFormat{nil, {Type: "text"}, {Type: ""}} {
		got, err := RequestOpenAI2ClaudeMessage(newTestGinContext(), structuredRequest(rf, "S"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.OutputConfig) != 0 {
			t.Fatalf("response_format %+v must not produce output_config", rf)
		}
		sys, _ := got.System.([]dto.ClaudeMediaMessage)
		if len(sys) != 1 {
			t.Fatalf("response_format %+v must not touch system, got %+v", rf, sys)
		}
	}
}

func TestRequestOpenAI2ClaudeMessage_JSONSchemaWithoutSchema_IsRejectedNotForwarded(t *testing.T) {
	for name, rf := range map[string]*dto.ResponseFormat{
		"no json_schema":    {Type: "json_schema"},
		"empty schema":      {Type: "json_schema", JsonSchema: json.RawMessage(`{"name":"x"}`)},
		"malformed":         {Type: "json_schema", JsonSchema: json.RawMessage(`{"name":`)},
		"schema not object": {Type: "json_schema", JsonSchema: json.RawMessage(`{"name":"x","schema":"string"}`)},
	} {
		if _, err := RequestOpenAI2ClaudeMessage(newTestGinContext(), structuredRequest(rf, "")); err == nil {
			t.Fatalf("%s: a json_schema request without a usable schema must fail here with a caller error, not become an upstream 400 the caller pays for", name)
		}
	}
}
