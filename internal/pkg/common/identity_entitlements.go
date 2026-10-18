package common

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Entitlements is a key→value map describing an account's product permissions.
//
// platform's GET /internal/v1/accounts/:id/entitlements/:product_id emits a
// flat map[string]string today (plan features such as plan_code / daily_quota /
// model_group, or {"plan_code":"free"} when there is none). UnmarshalJSON below
// also accepts non-string scalars so that a future numeric/bool value (e.g.
// "quota_remaining": 0) is stringified instead of failing the whole decode —
// a decode failure degrades GetEntitlements to {"plan_code":"free"}, which
// would make every key-based gate (middleware.EntitlementCheck) fail OPEN.
type Entitlements map[string]string

// UnmarshalJSON decodes a JSON object whose values may be strings, numbers,
// booleans or null. Strings are kept verbatim, numbers keep their literal text
// (so "0" and 0 are equivalent for GetInt), booleans become "true"/"false",
// null values are dropped, and nested objects/arrays keep their raw JSON text.
func (e *Entitlements) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(Entitlements, len(raw))
	for k, v := range raw {
		v = bytes.TrimSpace(v)
		if len(v) == 0 || bytes.Equal(v, []byte("null")) {
			continue
		}
		switch v[0] {
		case '"':
			var str string
			if err := json.Unmarshal(v, &str); err != nil {
				return err
			}
			out[k] = str
		default:
			// number / true / false / object / array: keep the literal text.
			out[k] = string(v)
		}
	}
	*e = out
	return nil
}

// GetString returns a string entitlement value, falling back to defaultVal.
func (e Entitlements) GetString(key, defaultVal string) string {
	if v, ok := e[key]; ok {
		return v
	}
	return defaultVal
}

// GetInt returns an integer entitlement value, falling back to defaultVal.
func (e Entitlements) GetInt(key string, defaultVal int) int {
	v := e.GetString(key, "")
	if v == "" {
		return defaultVal
	}
	var i int
	if _, err := fmt.Sscanf(v, "%d", &i); err != nil {
		return defaultVal
	}
	return i
}

// GetBool returns a boolean entitlement value, falling back to defaultVal.
func (e Entitlements) GetBool(key string, defaultVal bool) bool {
	v := e.GetString(key, "")
	switch v {
	case "true":
		return true
	case "false":
		return false
	default:
		return defaultVal
	}
}
