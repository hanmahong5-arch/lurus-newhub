package claude

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
)

// jsonObjectInstruction stands in for OpenAI's `json_object` mode, which
// Anthropic has no native counterpart for (its structured outputs always
// need a schema). It is appended AFTER the caller's own system prompt so the
// caller's prefix stays cacheable.
const jsonObjectInstruction = "Respond with a single valid JSON object and nothing else: no prose, no code fences."

// Keywords Anthropic's structured-output grammar rejects with a 400. They are
// stripped from the wire schema and restated as prose in the property's
// description — the same trade the official SDKs make — so the model still
// sees the intent while the grammar compiles. minItems is only allowed as 0
// or 1 and is handled separately.
var claudeUnsupportedConstraints = []string{
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "maxItems",
}

// applyResponseFormat maps an OpenAI `response_format` onto the Anthropic
// request. `json_schema` becomes `output_config.format` (the GA shape, no
// beta header); `json_object` becomes a trailing system instruction; `text`
// or absent is a no-op. A `json_schema` without a usable schema is a caller
// error and is refused here rather than forwarded as an upstream 400 the
// caller would be billed for.
func applyResponseFormat(rf *dto.ResponseFormat, req *dto.ClaudeRequest) error {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_schema":
		if len(rf.JsonSchema) == 0 {
			return fmt.Errorf("response_format.json_schema is required when type is json_schema")
		}
		var spec dto.FormatJsonSchema
		if err := common.Unmarshal(rf.JsonSchema, &spec); err != nil {
			return fmt.Errorf("response_format.json_schema: %w", err)
		}
		schema, ok := spec.Schema.(map[string]any)
		if !ok || len(schema) == 0 {
			return fmt.Errorf("response_format.json_schema.schema must be a JSON Schema object")
		}
		cfg, err := json.Marshal(map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"schema": normaliseClaudeSchema(schema),
			},
		})
		if err != nil {
			return err
		}
		req.OutputConfig = cfg
	case "json_object":
		blocks, _ := req.System.([]dto.ClaudeMediaMessage)
		req.System = append(blocks, dto.ClaudeMediaMessage{
			Type: "text",
			Text: common.GetPointer[string](jsonObjectInstruction),
		})
	}
	return nil
}

// normaliseClaudeSchema returns a copy of the schema that Anthropic's grammar
// compiler accepts: every object schema carries additionalProperties:false
// (only defaulted when the caller left it out — an explicit value is the
// caller's decision and is passed through), unsupported constraints are
// moved into the description, minItems above 1 is dropped. The caller's map
// is never mutated.
func normaliseClaudeSchema(node any) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v)+1)
		var moved []string
		for k, val := range v {
			switch {
			case isSchemaContainer(k):
				// name → schema maps: the keys are property/definition
				// names, never keywords, so a property called "minimum"
				// must survive untouched.
				if children, ok := val.(map[string]any); ok {
					copied := make(map[string]any, len(children))
					for name, child := range children {
						copied[name] = normaliseClaudeSchema(child)
					}
					out[k] = copied
				} else {
					out[k] = val
				}
			case isUnsupportedConstraint(k):
				moved = append(moved, fmt.Sprintf("%s %v", k, val))
			case k == "minItems":
				if n, ok := val.(float64); ok && n > 1 {
					moved = append(moved, fmt.Sprintf("minItems %v", val))
				} else {
					out[k] = val
				}
			default:
				out[k] = normaliseClaudeSchema(val)
			}
		}
		if isObjectSchema(out) {
			if _, has := out["additionalProperties"]; !has {
				out["additionalProperties"] = false
			}
		}
		if len(moved) > 0 {
			sort.Strings(moved)
			note := "Constraints: " + strings.Join(moved, ", ") + "."
			if desc, _ := out["description"].(string); desc != "" {
				out["description"] = strings.TrimRight(desc, " ") + " " + note
			} else {
				out["description"] = note
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normaliseClaudeSchema(item)
		}
		return out
	default:
		return v
	}
}

func isSchemaContainer(key string) bool {
	switch key {
	case "properties", "patternProperties", "$defs", "definitions":
		return true
	}
	return false
}

func isUnsupportedConstraint(key string) bool {
	for _, k := range claudeUnsupportedConstraints {
		if k == key {
			return true
		}
	}
	return false
}

func isObjectSchema(s map[string]any) bool {
	if t, ok := s["type"].(string); ok {
		return t == "object"
	}
	if ts, ok := s["type"].([]any); ok {
		for _, t := range ts {
			if t == "object" {
				return true
			}
		}
	}
	_, hasProps := s["properties"]
	return hasProps
}
