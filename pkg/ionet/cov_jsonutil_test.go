package ionet

import (
	"strings"
	"testing"
	"time"
)

// --- normalizeTimeString ------------------------------------------------

func TestNormalizeTimeString(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantChanged bool
		wantOutput  string // only checked when !wantChanged, or for exact-match cases
	}{
		{"empty string unchanged", "", false, ""},
		{"whitespace-only unchanged", "   ", false, "   "},
		{"plain non-time text unchanged", "hello world", false, "hello world"},
		{"exact RFC3339Nano unchanged", "2026-01-02T03:04:05.123456789Z", false, "2026-01-02T03:04:05.123456789Z"},
		{"exact RFC3339 unchanged", "2026-01-02T03:04:05Z", false, "2026-01-02T03:04:05Z"},
		{"RFC3339 with surrounding whitespace is trimmed", " 2026-01-02T03:04:05Z ", true, "2026-01-02T03:04:05Z"},
		{"missing timezone gets normalized to RFC3339Nano UTC", "2026-01-02T03:04:05", true, ""},
		{"missing timezone with micros gets normalized", "2026-01-02T03:04:05.123456", true, ""},
		{"missing timezone with nanos gets normalized", "2026-01-02T03:04:05.123456789", true, ""},
		{"garbage date-like string unchanged", "2026-13-45T99:99:99", false, "2026-13-45T99:99:99"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := normalizeTimeString(tt.input)
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v (got=%q)", changed, tt.wantChanged, got)
			}
			if !tt.wantChanged || tt.wantOutput != "" {
				if tt.wantOutput != "" && got != tt.wantOutput {
					t.Errorf("got %q, want %q", got, tt.wantOutput)
				} else if tt.wantOutput == "" && !tt.wantChanged && got != tt.input {
					t.Errorf("unchanged case: got %q, want original %q", got, tt.input)
				}
			}
			if tt.wantChanged && tt.wantOutput == "" {
				// timezone-less inputs must parse as a valid RFC3339Nano UTC timestamp.
				parsed, err := time.Parse(time.RFC3339Nano, got)
				if err != nil {
					t.Fatalf("normalized output %q not parseable as RFC3339Nano: %v", got, err)
				}
				if parsed.Location() != time.UTC {
					t.Errorf("normalized time not in UTC: %v", parsed)
				}
			}
		})
	}
}

// --- normalizeTimeValues (recursion through nested structures) -----------

func TestNormalizeTimeValues_NestedMapsAndArrays(t *testing.T) {
	input := map[string]interface{}{
		"created_at": "2026-05-06T07:08:09",
		"nested": map[string]interface{}{
			"updated_at": "2026-05-06T07:08:09.5",
			"label":      "not-a-time",
		},
		"events": []interface{}{
			map[string]interface{}{"time": "2026-05-06T07:08:09.999999999"},
			"plain-string-in-array",
		},
		"count": float64(3),
	}

	out := normalizeTimeValues(input)
	outMap, ok := out.(map[string]interface{})
	if !ok {
		t.Fatalf("normalizeTimeValues did not preserve map type: %T", out)
	}

	createdAt, ok := outMap["created_at"].(string)
	if !ok {
		t.Fatalf("created_at not a string: %T", outMap["created_at"])
	}
	if _, err := time.Parse(time.RFC3339Nano, createdAt); err != nil {
		t.Errorf("created_at %q not normalized to RFC3339Nano: %v", createdAt, err)
	}

	nested, ok := outMap["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested not preserved as map: %T", outMap["nested"])
	}
	if nested["label"] != "not-a-time" {
		t.Errorf("non-time string mutated: %v", nested["label"])
	}
	updatedAt, _ := nested["updated_at"].(string)
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		t.Errorf("nested updated_at %q not normalized: %v", updatedAt, err)
	}

	events, ok := outMap["events"].([]interface{})
	if !ok || len(events) != 2 {
		t.Fatalf("events not preserved as 2-element slice: %v", outMap["events"])
	}
	evt0, ok := events[0].(map[string]interface{})
	if !ok {
		t.Fatalf("events[0] not a map: %T", events[0])
	}
	evtTime, _ := evt0["time"].(string)
	if _, err := time.Parse(time.RFC3339Nano, evtTime); err != nil {
		t.Errorf("events[0].time %q not normalized: %v", evtTime, err)
	}
	if events[1] != "plain-string-in-array" {
		t.Errorf("events[1] mutated: %v", events[1])
	}

	// Non-string scalar values must pass through untouched.
	if outMap["count"] != float64(3) {
		t.Errorf("count mutated: %v", outMap["count"])
	}
}

// --- decodeWithFlexibleTimes --------------------------------------------

func TestDecodeWithFlexibleTimes_MalformedTopLevelJSON(t *testing.T) {
	var target struct{ X int }
	err := decodeWithFlexibleTimes([]byte(`{not valid`), &target)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDecodeWithFlexibleTimes_NormalizesTimezonelessField(t *testing.T) {
	type payload struct {
		CreatedAt time.Time `json:"created_at"`
		Name      string    `json:"name"`
	}
	var target payload
	err := decodeWithFlexibleTimes([]byte(`{"created_at":"2026-07-01T12:00:00","name":"widget"}`), &target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Name != "widget" {
		t.Errorf("Name = %q, want widget", target.Name)
	}
	if target.CreatedAt.IsZero() {
		t.Error("CreatedAt should have been parsed from the normalized timestamp, not left zero")
	}
	if target.CreatedAt.Year() != 2026 || target.CreatedAt.Month() != time.July || target.CreatedAt.Day() != 1 {
		t.Errorf("CreatedAt = %v, want 2026-07-01", target.CreatedAt)
	}
}

func TestDecodeWithFlexibleTimes_FinalUnmarshalTypeError(t *testing.T) {
	// After normalization, unmarshaling a string into an int field must fail.
	var target struct {
		Count int `json:"count"`
	}
	err := decodeWithFlexibleTimes([]byte(`{"count":"not-a-number"}`), &target)
	if err == nil {
		t.Fatal("expected type-mismatch unmarshal error")
	}
}

// --- decodeData / decodeDataWithFlexibleTimes ------------------------

func TestDecodeData_Success(t *testing.T) {
	var target struct {
		Name string `json:"name"`
	}
	err := decodeData([]byte(`{"data":{"name":"hello"}}`), &target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Name != "hello" {
		t.Errorf("Name = %q, want hello", target.Name)
	}
}

func TestDecodeData_MissingDataKey_LeavesZeroValue(t *testing.T) {
	target := struct {
		Name string `json:"name"`
	}{Name: "preexisting"}
	err := decodeData([]byte(`{"other":"field"}`), &target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Name != "" {
		t.Errorf("Name = %q, want zero value overwritten by empty data wrapper", target.Name)
	}
}

func TestDecodeData_MalformedJSON(t *testing.T) {
	var target struct{ X int }
	if err := decodeData([]byte(`{bad`), &target); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestDecodeDataWithFlexibleTimes_Success(t *testing.T) {
	type payload struct {
		CreatedAt time.Time `json:"created_at"`
	}
	var target payload
	err := decodeDataWithFlexibleTimes([]byte(`{"data":{"created_at":"2026-08-09T10:11:12.5"}}`), &target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.CreatedAt.IsZero() {
		t.Error("CreatedAt should be parsed from normalized timestamp inside data wrapper")
	}
}

func TestDecodeDataWithFlexibleTimes_MalformedJSON(t *testing.T) {
	var target struct{ X int }
	if err := decodeDataWithFlexibleTimes([]byte(`not json at all`), &target); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// --- APIError.Error() ---------------------------------------------------

func TestAPIError_Error_WithDetails(t *testing.T) {
	e := &APIError{Code: 500, Message: "internal error", Details: "stack trace here"}
	got := e.Error()
	if got != "internal error: stack trace here" {
		t.Errorf("Error() = %q, want %q", got, "internal error: stack trace here")
	}
}

func TestAPIError_Error_WithoutDetails(t *testing.T) {
	e := &APIError{Code: 404, Message: "not found"}
	got := e.Error()
	if got != "not found" {
		t.Errorf("Error() = %q, want %q", got, "not found")
	}
	if strings.Contains(got, ":") {
		t.Errorf("Error() = %q, should not contain a colon when Details is empty", got)
	}
}
