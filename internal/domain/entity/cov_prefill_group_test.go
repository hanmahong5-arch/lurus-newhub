package entity

// cov_prefill_group_test.go — business-acceptance tests for JSONValue, the
// driver.Valuer/Scanner/json.Marshaler/Unmarshaler quad that lets
// PrefillGroup.Items round-trip through both the SQL layer and the JSON API
// layer. Edge cases: empty value must become SQL NULL (not the zero-length
// byte slice, which some drivers reject), and a nil UnmarshalJSON receiver
// must not panic (defensive contract for encoding/json edge cases).

import (
	"encoding/json"
	"testing"
)

func TestJSONValue_Value(t *testing.T) {
	t.Run("empty JSONValue maps to SQL NULL", func(t *testing.T) {
		var j JSONValue
		v, err := j.Value()
		if err != nil {
			t.Fatalf("Value() error: %v", err)
		}
		if v != nil {
			t.Fatalf("Value() on empty JSONValue = %#v, want nil", v)
		}
	})

	t.Run("non-empty JSONValue passes bytes through verbatim", func(t *testing.T) {
		j := JSONValue(`{"a":1}`)
		v, err := j.Value()
		if err != nil {
			t.Fatalf("Value() error: %v", err)
		}
		b, ok := v.([]byte)
		if !ok || string(b) != `{"a":1}` {
			t.Fatalf("Value() = %#v, want []byte(`{\"a\":1}`)", v)
		}
	})
}

func TestJSONValue_Scan(t *testing.T) {
	t.Run("nil scan value normalizes to the literal JSON null", func(t *testing.T) {
		var j JSONValue
		if err := j.Scan(nil); err != nil {
			t.Fatalf("Scan(nil) error: %v", err)
		}
		if string(j) != "null" {
			t.Fatalf("Scan(nil) = %q, want %q", string(j), "null")
		}
	})

	t.Run("[]byte scan value is copied as-is", func(t *testing.T) {
		var j JSONValue
		if err := j.Scan([]byte(`{"x":1}`)); err != nil {
			t.Fatalf("Scan([]byte) error: %v", err)
		}
		if string(j) != `{"x":1}` {
			t.Fatalf("Scan([]byte) = %q", string(j))
		}
	})

	t.Run("string scan value (some drivers hand back string) is accepted", func(t *testing.T) {
		var j JSONValue
		if err := j.Scan(`{"y":2}`); err != nil {
			t.Fatalf("Scan(string) error: %v", err)
		}
		if string(j) != `{"y":2}` {
			t.Fatalf("Scan(string) = %q", string(j))
		}
	})

	t.Run("unsupported scan type leaves value untouched, does not error or panic", func(t *testing.T) {
		j := JSONValue(`{"keep":1}`)
		if err := j.Scan(42); err != nil {
			t.Fatalf("Scan(int) error: %v", err)
		}
		if string(j) != `{"keep":1}` {
			t.Fatalf("Scan(unsupported type) mutated value to %q, want unchanged", string(j))
		}
	})
}

func TestJSONValue_MarshalJSON(t *testing.T) {
	t.Run("empty JSONValue marshals to the JSON null literal", func(t *testing.T) {
		var j JSONValue
		b, err := j.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON() error: %v", err)
		}
		if string(b) != "null" {
			t.Fatalf("MarshalJSON() = %q, want null", string(b))
		}
	})

	t.Run("non-empty JSONValue marshals verbatim", func(t *testing.T) {
		j := JSONValue(`{"a":1}`)
		b, err := j.MarshalJSON()
		if err != nil {
			t.Fatalf("MarshalJSON() error: %v", err)
		}
		if string(b) != `{"a":1}` {
			t.Fatalf("MarshalJSON() = %q", string(b))
		}
	})
}

func TestJSONValue_UnmarshalJSON(t *testing.T) {
	t.Run("nil receiver is a no-op, not a panic", func(t *testing.T) {
		var j *JSONValue
		if err := j.UnmarshalJSON([]byte(`{"a":1}`)); err != nil {
			t.Fatalf("UnmarshalJSON() on nil receiver error: %v", err)
		}
	})

	t.Run("captures the raw bytes verbatim", func(t *testing.T) {
		var j JSONValue
		if err := j.UnmarshalJSON([]byte(`{"a":1}`)); err != nil {
			t.Fatalf("UnmarshalJSON() error: %v", err)
		}
		if string(j) != `{"a":1}` {
			t.Fatalf("UnmarshalJSON() = %q", string(j))
		}
	})
}

func TestJSONValue_EndToEnd_ViaEncodingJSON(t *testing.T) {
	// Proves the Marshaler/Unmarshaler pair composes correctly through the
	// standard library, not just when called directly.
	type wrapper struct {
		Items JSONValue `json:"items"`
	}
	w := wrapper{Items: JSONValue(`[1,2,3]`)}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	var out wrapper
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}
	if string(out.Items) != `[1,2,3]` {
		t.Fatalf("round trip via encoding/json = %q, want [1,2,3]", string(out.Items))
	}
}
