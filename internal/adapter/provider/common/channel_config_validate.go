package common

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// New save-time codes. header_override reuses the existing relay-time code
// (types.ErrorCodeChannelParamOverrideInvalid / ErrorCodeChannelHeaderOverrideInvalid,
// error.go) so the code an admin sees at save time is the same one the relay
// path already emits when a bad document slips through (ErrorCodeChannelParamOverrideInvalid
// stays the relay-time backstop for rows saved before this validator existed).
const (
	ErrorCodeChannelModelMappingInvalid types.ErrorCode = "channel:model_mapping_invalid"
	ErrorCodeChannelSettingInvalid      types.ErrorCode = "channel:setting_invalid"
)

// ChannelConfigValidationError names the offending channel JSON field so a
// save-time 400 can point an admin straight at the document to fix, instead of
// the failure only surfacing later at relay time.
type ChannelConfigValidationError struct {
	Field   string
	Code    types.ErrorCode
	Message string
}

func (e *ChannelConfigValidationError) Error() string {
	return e.Message
}

// headerTokenRegexp matches a valid HTTP header field-name (RFC 7230 token
// chars), rejecting things like "bad header" that contain a space.
var headerTokenRegexp = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

// probeRelayBody is the minimal chat-completions-shaped request body used to
// dry-run a param_override document at save time. It only carries {model,
// messages}, so an operation that targets a field a real request body would
// carry but the probe does not (max_tokens, tools, ...) fails the dry-run
// against the probe alone; dryRunErrorIsRequestDependent tells that apart
// from a document that is broken regardless of the request body. The engine
// itself type-checks a path before it would ever reach a value/pattern
// check (e.g. regexReplaceStringValue rejects on the probe's Null type for
// an absent path before it calls regexp.Compile), so a substring match on
// the dry-run error alone cannot tell "broken regardless of the request"
// apart from "only fails on this probe" for those cases — that is what
// validateOperationsStructure (below) checks before the dry-run ever runs,
// independently of the probe. What it still catches, unconditionally,
// before any dry-run: a non-object operation entry, a missing or unknown
// mode, move/copy with an empty from or to, any other path-based mode with
// an empty path, an unparseable regex_replace pattern, and a missing
// trim_prefix/trim_suffix/ensure_prefix/ensure_suffix value or replace from
// — all independent of whether the targeted path exists on the probe, and
// independent of any `conditions` gate (the pre-pass ignores conditions and
// inspects every operation). What is not caught: whether a path the probe
// does not carry exists on a real request body (request-dependent, see
// dryRunErrorIsRequestDependent), and — for the dry-run only — a type
// mismatch such as to_lower on `messages` sitting behind a `conditions`
// gate the probe body does not satisfy (applyOperations skips the gated
// operation body, so only the pre-pass rules above apply to it).
const probeRelayBody = `{"model":"probe","messages":[{"role":"user","content":"x"}]}`

// knownOperationModes are the modes applyOperations (override.go:311)
// implements, mirrored from its switch (override.go:334); any other mode
// string reaches the switch's default case, "unknown operation: %s".
var knownOperationModes = map[string]bool{
	"delete": true, "set": true, "move": true, "copy": true,
	"prepend": true, "append": true,
	"trim_prefix": true, "trim_suffix": true,
	"ensure_prefix": true, "ensure_suffix": true,
	"trim_space": true, "to_lower": true, "to_upper": true,
	"replace": true, "regex_replace": true,
}

// pathBasedOperationModes are every known mode except move/copy, which
// address a from/to pair instead of a single path.
var pathBasedOperationModes = map[string]bool{
	"delete": true, "set": true, "prepend": true, "append": true,
	"trim_prefix": true, "trim_suffix": true,
	"ensure_prefix": true, "ensure_suffix": true,
	"trim_space": true, "to_lower": true, "to_upper": true,
	"replace": true, "regex_replace": true,
}

// paramOverrideStructuralError builds a *ChannelConfigValidationError for a
// param_override structural defect found by validateOperationsStructure.
func paramOverrideStructuralError(msg string) error {
	return &ChannelConfigValidationError{
		Field:   "param_override",
		Code:    types.ErrorCodeChannelParamOverrideInvalid,
		Message: msg,
	}
}

// validateOperationsStructure walks a parsed param_override document's
// "operations" array (if present — a document without one is a legacy
// flat-merge document and has no structure beyond being a JSON object,
// already checked by ValidateParamOverride) and rejects anything the
// dry-run's probe-relative substring classification (see
// dryRunErrorIsRequestDependent above) cannot reliably tell apart from a
// request-dependent failure: a non-object entry, a missing/unknown mode,
// move/copy with an empty from or to, any other path-based mode with an
// empty path, an unparseable regex_replace pattern (checked independently
// of whether its path exists on the probe — the engine itself never reaches
// regexp.Compile for a path absent from the probe, because it type-checks
// the path first), and a missing trim_prefix/trim_suffix/ensure_prefix/
// ensure_suffix value or replace from (checked independently of the probe
// for the same reason). It does not check whether a path exists on a real
// request body — that stays request-dependent and is left to the dry-run.
func validateOperationsStructure(m map[string]interface{}) error {
	opsRaw, exists := m["operations"]
	if !exists {
		return nil
	}
	ops, ok := opsRaw.([]interface{})
	if !ok {
		return paramOverrideStructuralError("param_override.operations must be an array")
	}

	for i, rawEntry := range ops {
		entry, ok := rawEntry.(map[string]interface{})
		if !ok {
			return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] must be an object", i))
		}

		mode, _ := entry["mode"].(string)
		if mode == "" || !knownOperationModes[mode] {
			return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] has an unknown mode %q", i, mode))
		}

		if mode == "move" || mode == "copy" {
			from, _ := entry["from"].(string)
			to, _ := entry["to"].(string)
			if from == "" || to == "" {
				return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] (%s) requires a non-empty from and to", i, mode))
			}
			continue
		}

		if pathBasedOperationModes[mode] {
			path, _ := entry["path"].(string)
			if path == "" {
				return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] (%s) requires a non-empty path", i, mode))
			}
		}

		switch mode {
		case "regex_replace":
			pattern, _ := entry["from"].(string)
			if pattern == "" {
				return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] (regex_replace) requires a non-empty from as the regex pattern", i))
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] has an invalid regex pattern: %v", i, err))
			}
		case "replace":
			from, _ := entry["from"].(string)
			if from == "" {
				return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] (replace) requires a non-empty from", i))
			}
		case "trim_prefix", "trim_suffix":
			if v, exists := entry["value"]; !exists || v == nil {
				return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] (%s) requires a value", i, mode))
			}
		case "ensure_prefix", "ensure_suffix":
			v, exists := entry["value"]
			if !exists || v == nil || fmt.Sprintf("%v", v) == "" {
				return paramOverrideStructuralError(fmt.Sprintf("param_override.operations[%d] (%s) requires a non-empty value", i, mode))
			}
		}
	}
	return nil
}

// dryRunErrorIsRequestDependent reports whether a probe dry-run failure comes
// only from the probe body's limited shape ({model, messages}) rather than
// from the operations document itself being broken. moveValue/copyValue
// (override.go) report "source path does not exist" whenever `from` targets
// a field the probe does not carry (e.g. max_tokens); modifyValue and the
// string-only ops (trim_*/ensure_*/to_*/replace/regex_replace) report
// "operation not supported for type: Null" for the same reason, because
// gjson reports a missing path as the Null type. Both are legitimate against
// a real request body that does carry the field, so the caller must accept
// them rather than reject a document upstream New API documents as
// supported. Every structural defect this substring match could otherwise
// misclassify as request-dependent (an invalid regex pattern or a missing
// value/from on a path absent from the probe, in particular) is caught
// upstream by validateOperationsStructure before the dry-run runs, so by the
// time a "source path does not exist"/"Null" error reaches here it is safe
// to accept.
func dryRunErrorIsRequestDependent(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "source path does not exist") ||
		strings.Contains(msg, "operation not supported for type: Null")
}

// ValidateParamOverride dry-runs raw against the same override engine the
// relay path uses (ApplyParamOverride's two branches: the "operations" format
// via applyOperations, or the legacy flat-merge via applyOperationsLegacy), so
// a channel cannot be saved with a document that only fails once it takes
// live traffic. A dry-run failure that only reflects the probe body's
// limited shape (see dryRunErrorIsRequestDependent) is accepted: it is
// request-dependent, not a defect in the document.
func ValidateParamOverride(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return &ChannelConfigValidationError{
			Field:   "param_override",
			Code:    types.ErrorCodeChannelParamOverrideInvalid,
			Message: "param_override must be valid JSON",
		}
	}

	var m map[string]interface{}
	if err := common.Unmarshal([]byte(raw), &m); err != nil {
		return &ChannelConfigValidationError{
			Field:   "param_override",
			Code:    types.ErrorCodeChannelParamOverrideInvalid,
			Message: "param_override must be a JSON object: " + err.Error(),
		}
	}

	if err := validateOperationsStructure(m); err != nil {
		return err
	}

	var dryRunErr error
	if operations, ok := tryParseOperations(m); ok {
		_, dryRunErr = applyOperations(probeRelayBody, operations, map[string]interface{}{})
	} else {
		_, dryRunErr = applyOperationsLegacy([]byte(probeRelayBody), m)
	}
	if dryRunErr != nil && !dryRunErrorIsRequestDependent(dryRunErr) {
		return &ChannelConfigValidationError{
			Field:   "param_override",
			Code:    types.ErrorCodeChannelParamOverrideInvalid,
			Message: "param_override failed dry-run: " + dryRunErr.Error(),
		}
	}
	return nil
}

// ValidateHeaderOverride requires an object of string values keyed by valid
// header tokens, mirroring the value-type check processHeaderOverride
// (internal/adapter/provider/api_request.go) enforces at relay time, so a bad
// document is caught at save time rather than failing mid-relay.
func ValidateHeaderOverride(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return &ChannelConfigValidationError{
			Field:   "header_override",
			Code:    types.ErrorCodeChannelHeaderOverrideInvalid,
			Message: "header_override must be valid JSON",
		}
	}

	var m map[string]interface{}
	if err := common.Unmarshal([]byte(raw), &m); err != nil {
		return &ChannelConfigValidationError{
			Field:   "header_override",
			Code:    types.ErrorCodeChannelHeaderOverrideInvalid,
			Message: "header_override must be a JSON object: " + err.Error(),
		}
	}

	for k, v := range m {
		if !headerTokenRegexp.MatchString(k) {
			return &ChannelConfigValidationError{
				Field:   "header_override",
				Code:    types.ErrorCodeChannelHeaderOverrideInvalid,
				Message: fmt.Sprintf("header_override key %q is not a valid header name", k),
			}
		}
		if _, ok := v.(string); !ok {
			return &ChannelConfigValidationError{
				Field:   "header_override",
				Code:    types.ErrorCodeChannelHeaderOverrideInvalid,
				Message: fmt.Sprintf("header_override[%q] must be a string", k),
			}
		}
	}
	return nil
}

// ValidateModelMapping requires a flat non-empty-string -> non-empty-string
// document. The relay applies a single lookup (no chain-following), so this
// validator only rejects empty keys/values and does not attempt cycle
// detection.
func ValidateModelMapping(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return &ChannelConfigValidationError{
			Field:   "model_mapping",
			Code:    ErrorCodeChannelModelMappingInvalid,
			Message: "model_mapping must be valid JSON",
		}
	}

	var m map[string]string
	if err := common.Unmarshal([]byte(raw), &m); err != nil {
		return &ChannelConfigValidationError{
			Field:   "model_mapping",
			Code:    ErrorCodeChannelModelMappingInvalid,
			Message: "model_mapping must be an object of string to string: " + err.Error(),
		}
	}

	for from, to := range m {
		if from == "" {
			return &ChannelConfigValidationError{
				Field:   "model_mapping",
				Code:    ErrorCodeChannelModelMappingInvalid,
				Message: "model_mapping keys must not be empty",
			}
		}
		if to == "" {
			return &ChannelConfigValidationError{
				Field:   "model_mapping",
				Code:    ErrorCodeChannelModelMappingInvalid,
				Message: fmt.Sprintf("model_mapping[%q] must not map to an empty model name", from),
			}
		}
	}
	return nil
}

// ValidateChannelSetting mirrors entity.Channel.ValidateSettings' own decode
// (non-strict Unmarshal into dto.ChannelSettings) so a row carrying keys this
// build does not know about stays editable, while a type mismatch on a known
// field (e.g. proxy given as a number) is caught at save time instead of only
// surfacing wherever that field is later read.
func ValidateChannelSetting(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return &ChannelConfigValidationError{
			Field:   "setting",
			Code:    ErrorCodeChannelSettingInvalid,
			Message: "setting must be valid JSON",
		}
	}

	var settings dto.ChannelSettings
	if err := common.Unmarshal([]byte(raw), &settings); err != nil {
		return &ChannelConfigValidationError{
			Field:   "setting",
			Code:    ErrorCodeChannelSettingInvalid,
			Message: "setting failed to decode: " + err.Error(),
		}
	}
	return nil
}
