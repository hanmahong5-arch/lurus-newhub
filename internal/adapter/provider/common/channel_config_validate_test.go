package common

import (
	"errors"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestValidateParamOverride_MalformedJSON(t *testing.T) {
	if err := ValidateParamOverride("{"); err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestValidateParamOverride_Empty(t *testing.T) {
	if err := ValidateParamOverride(""); err != nil {
		t.Fatalf("expected nil for empty raw, got %v", err)
	}
}

func TestValidateParamOverride_ValidOperationsDocument(t *testing.T) {
	raw := `{"operations":[{"path":"model","mode":"trim_prefix","value":"openai/"}]}`
	if err := ValidateParamOverride(raw); err != nil {
		t.Fatalf("expected nil for a valid operations document, got %v", err)
	}
}

func TestValidateParamOverride_LegacyFlatMerge(t *testing.T) {
	raw := `{"temperature":0.2}`
	if err := ValidateParamOverride(raw); err != nil {
		t.Fatalf("expected nil for a legacy flat-merge document, got %v", err)
	}
}

func TestValidateParamOverride_UnknownOperationMode(t *testing.T) {
	raw := `{"operations":[{"path":"model","mode":"not_a_real_mode","value":"x"}]}`
	err := ValidateParamOverride(raw)
	if err == nil {
		t.Fatal("expected error for an unknown operation mode, got nil")
	}
	if !strings.Contains(err.Error(), "not_a_real_mode") {
		t.Fatalf("expected error to name the offending mode, got: %v", err)
	}
	var verr *ChannelConfigValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *ChannelConfigValidationError, got %T", err)
	}
	if verr.Code != types.ErrorCodeChannelParamOverrideInvalid {
		t.Fatalf("unexpected code: %s", verr.Code)
	}
}

func TestValidateHeaderOverride_NotAnObject(t *testing.T) {
	if err := ValidateHeaderOverride("[1]"); err == nil {
		t.Fatal("expected error for a JSON array, got nil")
	}
}

func TestValidateHeaderOverride_ValidStringHeader(t *testing.T) {
	if err := ValidateHeaderOverride(`{"X-A":"b"}`); err != nil {
		t.Fatalf("expected nil for a valid string header, got %v", err)
	}
}

func TestValidateHeaderOverride_InvalidHeaderKey(t *testing.T) {
	if err := ValidateHeaderOverride(`{"bad header":"x"}`); err == nil {
		t.Fatal("expected error for a header key containing a space, got nil")
	}
}

func TestValidateHeaderOverride_NonStringValue(t *testing.T) {
	if err := ValidateHeaderOverride(`{"X-A":1}`); err == nil {
		t.Fatal("expected error for a non-string header value, got nil")
	}
}

func TestValidateModelMapping_EmptyKey(t *testing.T) {
	if err := ValidateModelMapping(`{"":"x"}`); err == nil {
		t.Fatal("expected error for an empty model_mapping key, got nil")
	}
}

func TestValidateModelMapping_EmptyValue(t *testing.T) {
	if err := ValidateModelMapping(`{"a":""}`); err == nil {
		t.Fatal("expected error for an empty model_mapping value, got nil")
	}
}

func TestValidateModelMapping_Valid(t *testing.T) {
	if err := ValidateModelMapping(`{"gpt-4":"gpt-4o"}`); err != nil {
		t.Fatalf("expected nil for a valid mapping, got %v", err)
	}
}

func TestValidateChannelSetting_TypeMismatch(t *testing.T) {
	if err := ValidateChannelSetting(`{"proxy":1}`); err == nil {
		t.Fatal("expected error for proxy given as a number, got nil")
	}
}

func TestValidateChannelSetting_Valid(t *testing.T) {
	if err := ValidateChannelSetting(`{"proxy":"http://p"}`); err != nil {
		t.Fatalf("expected nil for a valid setting document, got %v", err)
	}
}

func TestValidateChannelSetting_Empty(t *testing.T) {
	if err := ValidateChannelSetting(""); err != nil {
		t.Fatalf("expected nil for empty raw, got %v", err)
	}
}

// TestValidateParamOverride_MoveTargetingFieldAbsentFromProbe locks the
// finding-1 fix: a move whose `from` targets a field the probe body
// ({model, messages}) does not carry (max_tokens) must not be rejected —
// upstream New API documents move as valid against a real request body that
// does carry max_tokens. Reverting dryRunErrorIsRequestDependent to always
// return false (i.e. treating "source path does not exist" as structural)
// turns this red.
func TestValidateParamOverride_MoveTargetingFieldAbsentFromProbe(t *testing.T) {
	raw := `{"operations":[{"mode":"move","from":"max_tokens","to":"max_completion_tokens"}]}`
	if err := ValidateParamOverride(raw); err != nil {
		t.Fatalf("expected nil for a move targeting a field absent from the probe, got %v", err)
	}
}

// TestValidateParamOverride_AppendTargetingFieldAbsentFromProbe locks the
// finding-1 fix for the "operation not supported for type: Null" shape
// (modifyValue/append on a path gjson reports as Null because the probe body
// does not carry `tools`).
func TestValidateParamOverride_AppendTargetingFieldAbsentFromProbe(t *testing.T) {
	raw := `{"operations":[{"path":"tools","mode":"append","value":{"type":"function"}}]}`
	if err := ValidateParamOverride(raw); err != nil {
		t.Fatalf("expected nil for an append targeting a field absent from the probe, got %v", err)
	}
}

// TestValidateParamOverride_UnknownOperationModeStillRejected re-asserts that
// classifying request-dependent dry-run failures as acceptable did not widen
// to cover genuinely structural documents: an unknown mode must still be
// rejected regardless of which path it targets.
func TestValidateParamOverride_UnknownOperationModeStillRejected(t *testing.T) {
	raw := `{"operations":[{"path":"max_tokens","mode":"not_a_real_mode","value":1}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for an unknown operation mode targeting a field absent from the probe, got nil")
	}
}

// TestValidateParamOverride_RegexCompileErrorStillRejected: a regex compile
// failure is structural (independent of the request body) and must stay
// rejected even though regex_replace targets a field, model, that IS present
// on the probe.
func TestValidateParamOverride_RegexCompileErrorStillRejected(t *testing.T) {
	raw := `{"operations":[{"path":"model","mode":"regex_replace","from":"(","to":"x"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for an invalid regex pattern, got nil")
	}
}

// TestValidateParamOverride_CopyMissingFromToStillRejected: copy with no
// from/to is structural ("copy from/to is required") and must stay rejected.
func TestValidateParamOverride_CopyMissingFromToStillRejected(t *testing.T) {
	raw := `{"operations":[{"mode":"copy"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for copy with no from/to, got nil")
	}
}

// ============================================================================
// validateOperationsStructure lock tests (residuals round 2: the substring
// classifier in dryRunErrorIsRequestDependent let structurally broken
// documents save because the engine's own dry-run either never reaches the
// structural check — the type check on an absent path short-circuits before
// regexp.Compile — or tryParseOperations silently falls back to the legacy
// flat-merge branch for a non-object entry instead of reporting an error).
// Removing the validateOperationsStructure call from ValidateParamOverride
// (channel_config_validate.go) turns every test below red.
// ============================================================================

// TestValidateParamOverride_MoveWithoutFrom_Rejected locks the finding: a
// move with no `from` used to be accepted because moveValue's "source path
// does not exist: " (empty path) error matches the same substring as a
// legitimate probe-only miss.
func TestValidateParamOverride_MoveWithoutFrom_Rejected(t *testing.T) {
	raw := `{"operations":[{"mode":"move","to":"max_completion_tokens"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for a move with no from, got nil")
	}
}

// TestValidateParamOverride_MoveWithoutTo_Rejected is the from/to-symmetric
// case: a move with no `to` is equally nonsensical and must be rejected
// before it ever reaches moveValue.
func TestValidateParamOverride_MoveWithoutTo_Rejected(t *testing.T) {
	raw := `{"operations":[{"mode":"move","from":"max_tokens"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for a move with no to, got nil")
	}
}

// TestValidateParamOverride_RegexReplaceInvalidPatternOnAbsentPath_Rejected
// locks the finding: the engine type-checks a path before it would ever
// call regexp.Compile, so an invalid pattern on a path the probe does not
// carry (max_tokens) previously reached "operation not supported for type:
// Null" — a request-dependent message — and the invalid regex itself was
// never compiled. validateOperationsStructure compiles the pattern
// independently of the probe.
func TestValidateParamOverride_RegexReplaceInvalidPatternOnAbsentPath_Rejected(t *testing.T) {
	raw := `{"operations":[{"path":"max_tokens","mode":"regex_replace","from":"(","to":"x"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for an invalid regex pattern on a path absent from the probe, got nil")
	}
}

// TestValidateParamOverride_NonObjectOperationEntry_Rejected locks the
// finding: tryParseOperations returns ok=false for a non-object entry, which
// previously made ValidateParamOverride silently fall back to
// applyOperationsLegacy (a flat-merge that just sets a literal "operations"
// key), accepting a document that was never a valid operations document.
func TestValidateParamOverride_NonObjectOperationEntry_Rejected(t *testing.T) {
	raw := `{"operations":[1]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for a non-object operation entry, got nil")
	}
}

// TestValidateParamOverride_PathLessAppend_Rejected locks the finding: an
// append with no path used to reach modifyValue on an empty path, which
// gjson reports as the Null type — the same request-dependent shape as a
// path the probe simply does not carry — accepting a document with no path
// at all.
func TestValidateParamOverride_PathLessAppend_Rejected(t *testing.T) {
	raw := `{"operations":[{"mode":"append","value":"x"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for an append with no path, got nil")
	}
}

// TestValidateParamOverride_TrimPrefixMissingValueOnAbsentPath_Rejected
// locks the finding: trimStringValue checks the path's type before it
// checks value==nil, so a missing value on a path absent from the probe
// (max_tokens) previously surfaced as "operation not supported for type:
// Null" instead of "trim value is required" and was accepted.
func TestValidateParamOverride_TrimPrefixMissingValueOnAbsentPath_Rejected(t *testing.T) {
	raw := `{"operations":[{"path":"max_tokens","mode":"trim_prefix"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for a trim_prefix missing value on a path absent from the probe, got nil")
	}
}

// TestValidateParamOverride_EnsureSuffixMissingValueOnAbsentPath_Rejected is
// the ensure_* counterpart of the trim_prefix case above.
func TestValidateParamOverride_EnsureSuffixMissingValueOnAbsentPath_Rejected(t *testing.T) {
	raw := `{"operations":[{"path":"max_tokens","mode":"ensure_suffix"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for an ensure_suffix missing value on a path absent from the probe, got nil")
	}
}

// TestValidateParamOverride_ReplaceMissingFromOnAbsentPath_Rejected locks the
// same finding for replace's search text (op.From): replaceStringValue also
// checks the path's type before checking from=="", so a missing from on a
// path absent from the probe previously surfaced as the same
// request-dependent "Null" message and was accepted.
func TestValidateParamOverride_ReplaceMissingFromOnAbsentPath_Rejected(t *testing.T) {
	raw := `{"operations":[{"path":"max_tokens","mode":"replace","to":"y"}]}`
	if err := ValidateParamOverride(raw); err == nil {
		t.Fatal("expected error for a replace missing from on a path absent from the probe, got nil")
	}
}

// TestValidateParamOverride_MoveStillSavesRegressionGuard re-asserts the
// pre-pass did not widen into rejecting the exact move the plan doc's oracle
// names as the must-still-save case: max_tokens -> max_completion_tokens,
// with both from and to present, targeting a field absent from the probe.
func TestValidateParamOverride_MoveStillSavesRegressionGuard(t *testing.T) {
	raw := `{"operations":[{"mode":"move","from":"max_tokens","to":"max_completion_tokens"}]}`
	if err := ValidateParamOverride(raw); err != nil {
		t.Fatalf("expected the max_tokens->max_completion_tokens move to still save, got %v", err)
	}
}
