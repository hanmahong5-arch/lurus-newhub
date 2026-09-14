package common

// override_lurus_test.go — oracle for the __lurus_* internal-control-key skip
// (L5, routing-resilience-limits-13). Channel operators set
// __lurus_force_http1 via the existing param_override editor to steer
// newhub's own relay transport selection (app.GetHttpClientFor), not the
// upstream request body — so it must never reach the upstream JSON, in
// EITHER override format (legacy flat-merge and the "operations" document).
// New file rather than appending to override_test.go so this lane does not
// touch a file another lane may be mid-edit on.

import (
	"encoding/json"
	"testing"
)

func TestParamOverride_SkipsLurusKeys(t *testing.T) {
	t.Run("legacy_format_strips_lurus_key_keeps_others", func(t *testing.T) {
		input := []byte(`{"model":"gpt-4o","temperature":0.5}`)
		override := map[string]interface{}{
			"__lurus_force_http1": true,
			"max_tokens":          256,
		}

		out, err := ApplyParamOverride(input, override, nil)
		if err != nil {
			t.Fatalf("ApplyParamOverride() error = %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if _, present := result["__lurus_force_http1"]; present {
			t.Errorf("__lurus_force_http1 leaked into the upstream body: %s", out)
		}
		if got, _ := result["max_tokens"].(float64); got != 256 {
			t.Errorf("max_tokens = %v, want 256 (a real override key must still apply)", result["max_tokens"])
		}
	})

	t.Run("only_lurus_keys_present_yields_untouched_body", func(t *testing.T) {
		input := []byte(`{"model":"gpt-4o"}`)
		override := map[string]interface{}{"__lurus_force_http1": true}

		out, err := ApplyParamOverride(input, override, nil)
		if err != nil {
			t.Fatalf("ApplyParamOverride() error = %v", err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if _, present := result["__lurus_force_http1"]; present {
			t.Errorf("__lurus_force_http1 leaked into the upstream body: %s", out)
		}
		if result["model"] != "gpt-4o" {
			t.Errorf("model = %v, want unchanged gpt-4o", result["model"])
		}
	})

	t.Run("operations_format_never_reads_lurus_key_at_all", func(t *testing.T) {
		// The "operations" branch only ever reads the "operations" array —
		// __lurus_force_http1 sitting alongside it is inert there already;
		// this asserts that stays true (a regression here would mean a
		// future change started treating top-level keys as flat-merge
		// fields even in operations mode).
		input := []byte(`{"model":"gpt-4o"}`)
		override := map[string]interface{}{
			"__lurus_force_http1": true,
			"operations": []interface{}{
				map[string]interface{}{"path": "model", "mode": "set", "value": "gpt-4o-mini"},
			},
		}

		out, err := ApplyParamOverride(input, override, nil)
		if err != nil {
			t.Fatalf("ApplyParamOverride() error = %v", err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if _, present := result["__lurus_force_http1"]; present {
			t.Errorf("__lurus_force_http1 leaked into the upstream body: %s", out)
		}
		if result["model"] != "gpt-4o-mini" {
			t.Errorf("model = %v, want gpt-4o-mini (the real operation must still apply)", result["model"])
		}
	})

	t.Run("direct_legacy_call_also_skips_lurus_keys", func(t *testing.T) {
		// ValidateParamOverride (channel_config_validate.go) calls
		// applyOperationsLegacy directly, bypassing ApplyParamOverride's
		// entry point — the skip must live in the merge loop itself, not
		// only at ApplyParamOverride's top, or a save-time dry-run would
		// validate a document differently than the relay path applies it.
		out, err := applyOperationsLegacy([]byte(`{"model":"gpt-4o"}`), map[string]interface{}{
			"__lurus_force_http1": true,
		})
		if err != nil {
			t.Fatalf("applyOperationsLegacy() error = %v", err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if _, present := result["__lurus_force_http1"]; present {
			t.Errorf("__lurus_force_http1 leaked into the upstream body: %s", out)
		}
	})
}
