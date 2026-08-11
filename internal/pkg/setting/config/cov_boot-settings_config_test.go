package config

import (
	"errors"
	"testing"
)

// boot_settings_configSample mirrors the field-kind coverage a real settings
// struct exercises through the config manager's reflection-based mapping
// (string / bool / int / uint / float / ptr / map / slice / struct).
type boot_settings_configSample struct {
	Name       string            `json:"name"`
	Enabled    bool              `json:"enabled"`
	Count      int               `json:"count"`
	Unsigned   uint              `json:"unsigned"`
	Rate       float64           `json:"rate"`
	Tags       []string          `json:"tags"`
	Meta       map[string]string `json:"meta"`
	unexported string            //nolint:unused // must be skipped by reflection walk
}

type boot_settings_configPtrSample struct {
	Opt *boot_settings_configInner `json:"opt"`
}

type boot_settings_configInner struct {
	X int `json:"x"`
}

func TestConfigManager_RegisterAndGet(t *testing.T) {
	cm := NewConfigManager()
	sample := &boot_settings_configSample{Name: "a"}
	cm.Register("sample", sample)

	got := cm.Get("sample")
	if got != sample {
		t.Fatalf("expected Get to return the exact registered pointer")
	}
	if cm.Get("missing") != nil {
		t.Fatalf("expected nil for unregistered config name")
	}
}

func TestConfigToMap_AllFieldKinds(t *testing.T) {
	s := &boot_settings_configSample{
		Name:       "hub",
		Enabled:    true,
		Count:      -7,
		Unsigned:   42,
		Rate:       1.5,
		Tags:       []string{"x", "y"},
		Meta:       map[string]string{"k": "v"},
		unexported: "must not leak",
	}
	m, err := ConfigToMap(s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["name"] != "hub" {
		t.Fatalf("expected name=hub, got %q", m["name"])
	}
	if m["enabled"] != "true" {
		t.Fatalf("expected enabled=true, got %q", m["enabled"])
	}
	if m["count"] != "-7" {
		t.Fatalf("expected count=-7, got %q", m["count"])
	}
	if m["unsigned"] != "42" {
		t.Fatalf("expected unsigned=42, got %q", m["unsigned"])
	}
	if m["rate"] != "1.5" {
		t.Fatalf("expected rate=1.5, got %q", m["rate"])
	}
	if m["tags"] != `["x","y"]` {
		t.Fatalf("expected tags JSON array, got %q", m["tags"])
	}
	if m["meta"] != `{"k":"v"}` {
		t.Fatalf("expected meta JSON object, got %q", m["meta"])
	}
	// Unexported field must never be serialized — it would leak internal
	// state (or crash on reflect.Value.Interface() of an unexported field).
	if _, ok := m["unexported"]; ok {
		t.Fatalf("unexported field must not appear in exported map")
	}
}

func TestConfigToMap_PtrField(t *testing.T) {
	nilPtr := &boot_settings_configPtrSample{}
	m, err := ConfigToMap(nilPtr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["opt"] != "null" {
		t.Fatalf("expected nil pointer to serialize as \"null\", got %q", m["opt"])
	}

	set := &boot_settings_configPtrSample{Opt: &boot_settings_configInner{X: 9}}
	m2, err := ConfigToMap(set)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m2["opt"] != `{"x":9}` {
		t.Fatalf("expected pointer contents JSON-serialized, got %q", m2["opt"])
	}
}

func TestConfigToMap_NonStructReturnsNil(t *testing.T) {
	m, err := ConfigToMap(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil map for a non-struct config value, got %v", m)
	}
}

func TestUpdateConfigFromMap_AllFieldKinds(t *testing.T) {
	s := &boot_settings_configSample{
		Name:     "orig",
		Enabled:  false,
		Count:    1,
		Unsigned: 1,
		Rate:     1,
		Tags:     []string{"orig"},
		Meta:     map[string]string{"orig": "v"},
	}
	err := UpdateConfigFromMap(s, map[string]string{
		"name":     "updated",
		"enabled":  "true",
		"count":    "-99",
		"unsigned": "7",
		"rate":     "3.25",
		"tags":     `["new1","new2"]`,
		"meta":     `{"nk":"nv"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "updated" || !s.Enabled || s.Count != -99 || s.Unsigned != 7 || s.Rate != 3.25 {
		t.Fatalf("scalar fields not updated correctly: %+v", s)
	}
	if len(s.Tags) != 2 || s.Tags[0] != "new1" {
		t.Fatalf("tags not updated correctly: %v", s.Tags)
	}
	if s.Meta["nk"] != "nv" {
		t.Fatalf("meta not updated correctly: %v", s.Meta)
	}
}

func TestUpdateConfigFromMap_BoolParsingVariants(t *testing.T) {
	// Business edge case: config values arrive as strings from a DB text
	// column, so callers may write "1"/"0"/"True"/"TRUE" etc. strconv.ParseBool
	// accepts a fixed set; anything outside it must be silently ignored
	// (field left unchanged) rather than corrupting the setting.
	tests := []struct {
		name    string
		value   string
		want    bool
		changed bool
	}{
		{"lowercase true", "true", true, true},
		{"uppercase TRUE", "TRUE", true, true},
		{"numeric 1", "1", true, true},
		{"numeric 0", "0", false, true},
		{"lowercase false", "false", false, true},
		{"unsupported word yes", "yes", false, false}, // not parsed -> field untouched
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &boot_settings_configSample{Enabled: false}
			if tc.name == "unsupported word yes" {
				s.Enabled = false // baseline stays false either way; verify via a true baseline instead
				s.Enabled = true
			}
			baseline := s.Enabled
			err := UpdateConfigFromMap(s, map[string]string{"enabled": tc.value})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.changed {
				if s.Enabled != tc.want {
					t.Fatalf("value %q: expected Enabled=%v, got %v", tc.value, tc.want, s.Enabled)
				}
			} else {
				if s.Enabled != baseline {
					t.Fatalf("value %q: expected field left unchanged at %v, got %v", tc.value, baseline, s.Enabled)
				}
			}
		})
	}
}

func TestUpdateConfigFromMap_InvalidNumericSkipsField(t *testing.T) {
	s := &boot_settings_configSample{Count: 5, Unsigned: 5, Rate: 5}
	err := UpdateConfigFromMap(s, map[string]string{
		"count":    "not-a-number",
		"unsigned": "-5", // invalid for uint
		"rate":     "NaN-ish-garbage",
	})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if s.Count != 5 || s.Unsigned != 5 || s.Rate != 5 {
		t.Fatalf("expected fields to remain unchanged on parse failure, got %+v", s)
	}
}

func TestUpdateConfigFromMap_UnknownKeysIgnored(t *testing.T) {
	s := &boot_settings_configSample{Name: "keep"}
	if err := UpdateConfigFromMap(s, map[string]string{"does_not_exist": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "keep" {
		t.Fatalf("expected untouched struct, got %+v", s)
	}
}

func TestUpdateConfigFromMap_NonPointerIsNoOp(t *testing.T) {
	s := boot_settings_configSample{Name: "immutable"}
	if err := UpdateConfigFromMap(s, map[string]string{"name": "changed"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Passing a non-pointer means reflect can't write back to the caller's
	// value at all (Go is call-by-value) — this asserts the function returns
	// cleanly (nil error) rather than panicking, not that it mutates s.
}

func TestUpdateConfigFromMap_PtrFieldLifecycle(t *testing.T) {
	s := &boot_settings_configPtrSample{}
	// Initialize a nil pointer field from JSON.
	if err := UpdateConfigFromMap(s, map[string]string{"opt": `{"x":5}`}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Opt == nil || s.Opt.X != 5 {
		t.Fatalf("expected pointer field initialized with X=5, got %+v", s.Opt)
	}

	// "null" must reset the pointer back to nil.
	if err := UpdateConfigFromMap(s, map[string]string{"opt": "null"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Opt != nil {
		t.Fatalf("expected pointer field reset to nil on \"null\", got %+v", s.Opt)
	}
}

func TestConfigManager_LoadFromDB_PrefixScoping(t *testing.T) {
	cm := NewConfigManager()
	a := &boot_settings_configSample{Name: "a-orig"}
	b := &boot_settings_configSample{Name: "b-orig"}
	cm.Register("moduleA", a)
	cm.Register("moduleB", b)

	err := cm.LoadFromDB(map[string]string{
		"moduleA.name":       "a-updated",
		"moduleB.name":       "b-updated",
		"moduleC.name":       "ignored-unregistered-module",
		"unrelated_flat_key": "ignored-no-prefix-match",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name != "a-updated" {
		t.Fatalf("expected moduleA scoped update only, got %q", a.Name)
	}
	if b.Name != "b-updated" {
		t.Fatalf("expected moduleB scoped update only, got %q", b.Name)
	}
}

func TestConfigManager_SaveToDB_EmitsNamespacedKeys(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("mod", &boot_settings_configSample{Name: "n", Enabled: true, Tags: []string{}, Meta: map[string]string{}})

	seen := map[string]string{}
	err := cm.SaveToDB(func(key, value string) error {
		seen[key] = value
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen["mod.name"] != "n" {
		t.Fatalf("expected namespaced key mod.name=n, got %v", seen)
	}
	if seen["mod.enabled"] != "true" {
		t.Fatalf("expected namespaced key mod.enabled=true, got %v", seen)
	}
}

func TestConfigManager_SaveToDB_PropagatesUpdateFuncError(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("mod", &boot_settings_configSample{Name: "n"})

	boom := errUpdateFailed
	err := cm.SaveToDB(func(key, value string) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected SaveToDB to propagate updateFunc error, got %v", err)
	}
}

var errUpdateFailed = &boot_settings_sentinelErr{"update failed"}

type boot_settings_sentinelErr struct{ msg string }

func (e *boot_settings_sentinelErr) Error() string { return e.msg }

func TestConfigManager_ExportAllConfigs(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("modx", &boot_settings_configSample{Name: "exported-val"})

	out := cm.ExportAllConfigs()
	if out["modx.name"] != "exported-val" {
		t.Fatalf("expected modx.name=exported-val in export, got %v", out)
	}
}
