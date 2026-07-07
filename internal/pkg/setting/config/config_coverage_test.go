package config

import (
	"encoding/json"
	"testing"
)

// --- fixtures ---

type innerStruct struct {
	Nested string `json:"nested"`
}

type ptrStruct struct {
	Value int `json:"value"`
}

type fullConfig struct {
	StrField    string            `json:"str_field"`
	BoolField   bool              `json:"bool_field"`
	IntField    int               `json:"int_field"`
	Int64Field  int64             `json:"int64_field"`
	UintField   uint              `json:"uint_field"`
	FloatField  float64           `json:"float_field"`
	PtrField    *ptrStruct        `json:"ptr_field"`
	MapField    map[string]string `json:"map_field"`
	SliceField  []string          `json:"slice_field"`
	StructField innerStruct       `json:"struct_field"`
	NoTagField  string            `json:"-"`
	Untagged    string
	unexported  string //nolint:unused
}

// unsupportedConfig has a field kind not handled by the switch (chan) to hit the
// "default: skip" branch in configToMap.
type unsupportedConfig struct {
	Ch chan int `json:"ch"`
}

func TestConfigToMap_AllFieldKinds(t *testing.T) {
	cfg := &fullConfig{
		StrField:   "hello",
		BoolField:  true,
		IntField:   -7,
		Int64Field: 1234567890123,
		UintField:  42,
		FloatField: 3.5,
		PtrField:   &ptrStruct{Value: 9},
		MapField:   map[string]string{"a": "b"},
		SliceField: []string{"x", "y"},
		StructField: innerStruct{
			Nested: "n",
		},
		NoTagField: "ignored-by-tag-dash",
		Untagged:   "untagged-value",
		unexported: "should-not-appear",
	}

	result, err := ConfigToMap(cfg)
	if err != nil {
		t.Fatalf("ConfigToMap returned error: %v", err)
	}

	if got := result["str_field"]; got != "hello" {
		t.Errorf("str_field = %q, want %q", got, "hello")
	}
	if got := result["bool_field"]; got != "true" {
		t.Errorf("bool_field = %q, want %q", got, "true")
	}
	if got := result["int_field"]; got != "-7" {
		t.Errorf("int_field = %q, want %q", got, "-7")
	}
	if got := result["int64_field"]; got != "1234567890123" {
		t.Errorf("int64_field = %q, want %q", got, "1234567890123")
	}
	if got := result["uint_field"]; got != "42" {
		t.Errorf("uint_field = %q, want %q", got, "42")
	}
	if got := result["float_field"]; got != "3.5" {
		t.Errorf("float_field = %q, want %q", got, "3.5")
	}
	wantPtr, _ := json.Marshal(&ptrStruct{Value: 9})
	if got := result["ptr_field"]; got != string(wantPtr) {
		t.Errorf("ptr_field = %q, want %q", got, string(wantPtr))
	}
	wantMap, _ := json.Marshal(map[string]string{"a": "b"})
	if got := result["map_field"]; got != string(wantMap) {
		t.Errorf("map_field = %q, want %q", got, string(wantMap))
	}
	wantSlice, _ := json.Marshal([]string{"x", "y"})
	if got := result["slice_field"]; got != string(wantSlice) {
		t.Errorf("slice_field = %q, want %q", got, string(wantSlice))
	}
	wantStruct, _ := json.Marshal(innerStruct{Nested: "n"})
	if got := result["struct_field"]; got != string(wantStruct) {
		t.Errorf("struct_field = %q, want %q", got, string(wantStruct))
	}
	// json:"-" falls back to field name (per source logic, key=="-" resets to fieldType.Name)
	if got := result["NoTagField"]; got != "ignored-by-tag-dash" {
		t.Errorf("NoTagField = %q, want %q", got, "ignored-by-tag-dash")
	}
	if got := result["Untagged"]; got != "untagged-value" {
		t.Errorf("Untagged = %q, want %q", got, "untagged-value")
	}
	if _, ok := result["unexported"]; ok {
		t.Errorf("unexported field leaked into result: %v", result)
	}
	if len(result) != 12 {
		t.Errorf("result has %d keys, want 12: %v", len(result), result)
	}
}

func TestConfigToMap_NilPtrField(t *testing.T) {
	cfg := &fullConfig{PtrField: nil}
	result, err := ConfigToMap(cfg)
	if err != nil {
		t.Fatalf("ConfigToMap returned error: %v", err)
	}
	if got := result["ptr_field"]; got != "null" {
		t.Errorf("ptr_field = %q, want %q", got, "null")
	}
}

func TestConfigToMap_NonStructInput(t *testing.T) {
	// not a struct nor pointer-to-struct -> nil, nil
	result, err := ConfigToMap(42)
	if err != nil {
		t.Fatalf("ConfigToMap returned error: %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}

	// pointer to non-struct
	x := 42
	result2, err2 := ConfigToMap(&x)
	if err2 != nil {
		t.Fatalf("ConfigToMap returned error: %v", err2)
	}
	if result2 != nil {
		t.Errorf("result2 = %v, want nil", result2)
	}
}

func TestConfigToMap_UnsupportedKindSkipped(t *testing.T) {
	cfg := &unsupportedConfig{Ch: make(chan int)}
	result, err := ConfigToMap(cfg)
	if err != nil {
		t.Fatalf("ConfigToMap returned error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("result = %v, want empty map (chan field must be skipped)", result)
	}
}

func TestUpdateConfigFromMap_AllFieldKinds(t *testing.T) {
	cfg := &fullConfig{}
	ptrJSON, _ := json.Marshal(&ptrStruct{Value: 5})
	mapJSON, _ := json.Marshal(map[string]string{"k": "v"})
	sliceJSON, _ := json.Marshal([]string{"p", "q"})
	structJSON, _ := json.Marshal(innerStruct{Nested: "z"})

	configMap := map[string]string{
		"str_field":    "world",
		"bool_field":   "true",
		"int_field":    "-3",
		"int64_field":  "99999999999",
		"uint_field":   "7",
		"float_field":  "1.25",
		"ptr_field":    string(ptrJSON),
		"map_field":    string(mapJSON),
		"slice_field":  string(sliceJSON),
		"struct_field": string(structJSON),
		"NoTagField":   "restored",
		"Untagged":     "restored2",
	}

	if err := UpdateConfigFromMap(cfg, configMap); err != nil {
		t.Fatalf("UpdateConfigFromMap returned error: %v", err)
	}

	if cfg.StrField != "world" {
		t.Errorf("StrField = %q, want %q", cfg.StrField, "world")
	}
	if cfg.BoolField != true {
		t.Errorf("BoolField = %v, want true", cfg.BoolField)
	}
	if cfg.IntField != -3 {
		t.Errorf("IntField = %d, want -3", cfg.IntField)
	}
	if cfg.Int64Field != 99999999999 {
		t.Errorf("Int64Field = %d, want 99999999999", cfg.Int64Field)
	}
	if cfg.UintField != 7 {
		t.Errorf("UintField = %d, want 7", cfg.UintField)
	}
	if cfg.FloatField != 1.25 {
		t.Errorf("FloatField = %v, want 1.25", cfg.FloatField)
	}
	if cfg.PtrField == nil || cfg.PtrField.Value != 5 {
		t.Errorf("PtrField = %+v, want &{Value:5}", cfg.PtrField)
	}
	if cfg.MapField["k"] != "v" {
		t.Errorf("MapField = %v, want map[k:v]", cfg.MapField)
	}
	if len(cfg.SliceField) != 2 || cfg.SliceField[0] != "p" || cfg.SliceField[1] != "q" {
		t.Errorf("SliceField = %v, want [p q]", cfg.SliceField)
	}
	if cfg.StructField.Nested != "z" {
		t.Errorf("StructField.Nested = %q, want %q", cfg.StructField.Nested, "z")
	}
	if cfg.NoTagField != "restored" {
		t.Errorf("NoTagField = %q, want %q", cfg.NoTagField, "restored")
	}
	if cfg.Untagged != "restored2" {
		t.Errorf("Untagged = %q, want %q", cfg.Untagged, "restored2")
	}
}

func TestUpdateConfigFromMap_ExistingPtrReused(t *testing.T) {
	// Field already non-nil: exercise the branch that does NOT allocate a new pointer.
	cfg := &fullConfig{PtrField: &ptrStruct{Value: 1}}
	newJSON, _ := json.Marshal(&ptrStruct{Value: 100})
	err := UpdateConfigFromMap(cfg, map[string]string{"ptr_field": string(newJSON)})
	if err != nil {
		t.Fatalf("UpdateConfigFromMap returned error: %v", err)
	}
	if cfg.PtrField.Value != 100 {
		t.Errorf("PtrField.Value = %d, want 100", cfg.PtrField.Value)
	}
}

func TestUpdateConfigFromMap_NullClearsPtr(t *testing.T) {
	cfg := &fullConfig{PtrField: &ptrStruct{Value: 1}}
	err := UpdateConfigFromMap(cfg, map[string]string{"ptr_field": "null"})
	if err != nil {
		t.Fatalf("UpdateConfigFromMap returned error: %v", err)
	}
	if cfg.PtrField != nil {
		t.Errorf("PtrField = %+v, want nil", cfg.PtrField)
	}
}

func TestUpdateConfigFromMap_InvalidValuesSkipped(t *testing.T) {
	cfg := &fullConfig{
		StrField:  "keep-str",
		BoolField: true,
	}
	configMap := map[string]string{
		"bool_field":   "not-a-bool",
		"int_field":    "not-an-int",
		"uint_field":   "not-a-uint",
		"float_field":  "not-a-float",
		"ptr_field":    "not-json",
		"map_field":    "not-json",
		"slice_field":  "not-json",
		"struct_field": "not-json",
		"missing_key":  "irrelevant", // no matching field -> continue via !ok
	}
	if err := UpdateConfigFromMap(cfg, configMap); err != nil {
		t.Fatalf("UpdateConfigFromMap returned error: %v", err)
	}
	// All invalid conversions should leave zero-values / previous values untouched.
	if cfg.BoolField != true {
		t.Errorf("BoolField changed on invalid input: %v", cfg.BoolField)
	}
	if cfg.IntField != 0 {
		t.Errorf("IntField changed on invalid input: %v", cfg.IntField)
	}
	if cfg.UintField != 0 {
		t.Errorf("UintField changed on invalid input: %v", cfg.UintField)
	}
	if cfg.FloatField != 0 {
		t.Errorf("FloatField changed on invalid input: %v", cfg.FloatField)
	}
	// PtrField starts nil; the switch allocates a new zero-value pointer before
	// attempting json.Unmarshal, so a failed unmarshal still leaves a non-nil
	// pointer to the zero value (the allocation happens before the error check).
	if cfg.PtrField == nil || cfg.PtrField.Value != 0 {
		t.Errorf("PtrField = %+v, want allocated zero-value pointer", cfg.PtrField)
	}
	if cfg.MapField != nil {
		t.Errorf("MapField changed on invalid input: %v", cfg.MapField)
	}
	if cfg.StrField != "keep-str" {
		t.Errorf("unrelated StrField mutated: %q", cfg.StrField)
	}
}

func TestUpdateConfigFromMap_NonPointerOrNonStructInput(t *testing.T) {
	// Not a pointer -> returns nil immediately.
	if err := UpdateConfigFromMap(fullConfig{}, map[string]string{"str_field": "x"}); err != nil {
		t.Fatalf("expected nil error for non-pointer input, got %v", err)
	}

	// Pointer to non-struct -> returns nil immediately.
	x := 1
	if err := UpdateConfigFromMap(&x, map[string]string{"str_field": "x"}); err != nil {
		t.Fatalf("expected nil error for pointer-to-non-struct, got %v", err)
	}
}

func TestUpdateConfigFromMap_UnexportedFieldSkipped(t *testing.T) {
	cfg := &fullConfig{}
	if err := UpdateConfigFromMap(cfg, map[string]string{"unexported": "leak"}); err != nil {
		t.Fatalf("UpdateConfigFromMap returned error: %v", err)
	}
	if cfg.unexported != "" {
		t.Errorf("unexported field was set via reflection: %q", cfg.unexported)
	}
}

// --- ConfigManager tests ---

func TestConfigManager_RegisterGetRoundTrip(t *testing.T) {
	cm := NewConfigManager()

	if got := cm.Get("missing"); got != nil {
		t.Errorf("Get on empty manager = %v, want nil", got)
	}

	cfg := &fullConfig{StrField: "registered"}
	cm.Register("mymod", cfg)

	got := cm.Get("mymod")
	gotCfg, ok := got.(*fullConfig)
	if !ok {
		t.Fatalf("Get returned wrong type: %T", got)
	}
	if gotCfg.StrField != "registered" {
		t.Errorf("StrField = %q, want %q", gotCfg.StrField, "registered")
	}
}

func TestConfigManager_LoadFromDB(t *testing.T) {
	cm := NewConfigManager()
	cfg := &fullConfig{}
	cm.Register("modA", cfg)

	options := map[string]string{
		"modA.str_field":  "loaded",
		"modA.bool_field": "true",
		"modB.str_field":  "unrelated-module-should-be-ignored",
		"no-prefix-match": "ignored-too",
	}

	if err := cm.LoadFromDB(options); err != nil {
		t.Fatalf("LoadFromDB returned error: %v", err)
	}
	if cfg.StrField != "loaded" {
		t.Errorf("StrField = %q, want %q", cfg.StrField, "loaded")
	}
	if cfg.BoolField != true {
		t.Errorf("BoolField = %v, want true", cfg.BoolField)
	}
}

func TestConfigManager_LoadFromDB_NoMatchingKeysNoop(t *testing.T) {
	cm := NewConfigManager()
	cfg := &fullConfig{StrField: "unchanged"}
	cm.Register("modA", cfg)

	// options with no keys matching the "modA." prefix -> configMap stays empty, update skipped.
	if err := cm.LoadFromDB(map[string]string{"other.field": "x"}); err != nil {
		t.Fatalf("LoadFromDB returned error: %v", err)
	}
	if cfg.StrField != "unchanged" {
		t.Errorf("StrField = %q, want %q (should be untouched)", cfg.StrField, "unchanged")
	}
}

func TestConfigManager_SaveToDB(t *testing.T) {
	cm := NewConfigManager()
	cfg := &fullConfig{StrField: "to-save", BoolField: false}
	cm.Register("modA", cfg)

	collected := map[string]string{}
	err := cm.SaveToDB(func(key, value string) error {
		collected[key] = value
		return nil
	})
	if err != nil {
		t.Fatalf("SaveToDB returned error: %v", err)
	}
	if got := collected["modA.str_field"]; got != "to-save" {
		t.Errorf("collected[modA.str_field] = %q, want %q", got, "to-save")
	}
	if got := collected["modA.bool_field"]; got != "false" {
		t.Errorf("collected[modA.bool_field] = %q, want %q", got, "false")
	}
}

func TestConfigManager_SaveToDB_UpdateFuncError(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("modA", &fullConfig{StrField: "x"})

	wantErr := errUpdateFuncFailed
	err := cm.SaveToDB(func(key, value string) error {
		return wantErr
	})
	if err != wantErr {
		t.Errorf("SaveToDB error = %v, want %v", err, wantErr)
	}
}

var errUpdateFuncFailed = &saveErr{"update func failed"}

type saveErr struct{ msg string }

func (e *saveErr) Error() string { return e.msg }

func TestConfigManager_ExportAllConfigs(t *testing.T) {
	cm := NewConfigManager()
	cm.Register("mod1", &fullConfig{StrField: "v1"})
	cm.Register("mod2", &fullConfig{StrField: "v2"})

	result := cm.ExportAllConfigs()
	if got := result["mod1.str_field"]; got != "v1" {
		t.Errorf("mod1.str_field = %q, want %q", got, "v1")
	}
	if got := result["mod2.str_field"]; got != "v2" {
		t.Errorf("mod2.str_field = %q, want %q", got, "v2")
	}
}

func TestConfigManager_ExportAllConfigs_SkipsErrorConfigs(t *testing.T) {
	cm := NewConfigManager()
	// Non-struct, non-pointer value: configToMap returns (nil, nil) -> no error,
	// so it should just contribute zero keys (not an error path but validates the loop
	// handles a config that yields an empty map gracefully).
	cm.Register("bad", 42)
	cm.Register("good", &fullConfig{StrField: "ok"})

	result := cm.ExportAllConfigs()
	if got := result["good.str_field"]; got != "ok" {
		t.Errorf("good.str_field = %q, want %q", got, "ok")
	}
	for k := range result {
		if len(k) >= len("bad.") && k[:4] == "bad." {
			t.Errorf("unexpected key from bad config: %q", k)
		}
	}
}

func TestGlobalConfig_IsUsableConfigManager(t *testing.T) {
	// Snapshot/restore GlobalConfig's registration state to stay -count=1 safe.
	const name = "coverage_test_probe"
	prev := GlobalConfig.Get(name)
	t.Cleanup(func() {
		if prev == nil {
			GlobalConfig.mutex.Lock()
			delete(GlobalConfig.configs, name)
			GlobalConfig.mutex.Unlock()
			return
		}
		GlobalConfig.Register(name, prev)
	})

	GlobalConfig.Register(name, &fullConfig{StrField: "global-probe"})
	got, ok := GlobalConfig.Get(name).(*fullConfig)
	if !ok || got.StrField != "global-probe" {
		t.Errorf("GlobalConfig round trip failed: %+v", GlobalConfig.Get(name))
	}
}
