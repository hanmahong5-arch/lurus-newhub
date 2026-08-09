package entity

// cov_channel_test.go — business-acceptance tests for the Channel entity's
// key/model/group parsing, nullable-pointer accessors, settings validation,
// and multi-key rotation/cooldown decision logic (GetNextEnabledKeyIndex /
// noAvailableKeyError). These are the money-adjacent decisions that pick
// which upstream key a relay request uses and whether a channel is reported
// as "no available key" (permanent) vs "all keys cooling" (retryable 503).

import (
	"math"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

func TestChannel_GetKeys(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		want    []string
	}{
		{"empty key returns empty slice not nil-length", Channel{Key: ""}, []string{}},
		{"single key no newline", Channel{Key: "sk-abc"}, []string{"sk-abc"}},
		{"newline separated multi key", Channel{Key: "sk-a\nsk-b\nsk-c"}, []string{"sk-a", "sk-b", "sk-c"}},
		{"trims leading/trailing newline before split", Channel{Key: "\nsk-a\nsk-b\n"}, []string{"sk-a", "sk-b"}},
		{"json array key (vertex-style)", Channel{Key: `["k1","k2"]`}, []string{`"k1"`, `"k2"`}},
		{"json array with whitespace prefix still detected", Channel{Key: "  [\"k1\"]"}, []string{`"k1"`}},
		{"malformed json array falls back to newline split of raw string", Channel{Key: "[not-json"}, []string{"[not-json"}},
		{"cached Keys field takes priority over Key string", Channel{Key: "sk-should-be-ignored", Keys: []string{"cached-1", "cached-2"}}, []string{"cached-1", "cached-2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.channel.GetKeys()
			if len(got) != len(tt.want) {
				t.Fatalf("GetKeys() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("GetKeys()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestChannel_GetModels(t *testing.T) {
	tests := []struct {
		name   string
		models string
		want   []string
	}{
		{"empty", "", []string{}},
		{"single", "gpt-4", []string{"gpt-4"}},
		{"comma separated", "gpt-4,gpt-3.5,claude-3", []string{"gpt-4", "gpt-3.5", "claude-3"}},
		{"trims leading/trailing comma", ",gpt-4,gpt-3.5,", []string{"gpt-4", "gpt-3.5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Channel{Models: tt.models}
			got := c.GetModels()
			if len(got) != len(tt.want) {
				t.Fatalf("GetModels() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("GetModels()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestChannel_ManagedModelsBySync_RoundTrip(t *testing.T) {
	t.Run("get on empty field returns empty slice", func(t *testing.T) {
		c := Channel{}
		got := c.GetManagedModelsBySync()
		if len(got) != 0 {
			t.Fatalf("expected empty slice, got %#v", got)
		}
	})

	t.Run("get on malformed json logs and returns empty slice, does not panic", func(t *testing.T) {
		c := Channel{ManagedModelsBySync: "{not valid"}
		got := c.GetManagedModelsBySync()
		if len(got) != 0 {
			t.Fatalf("expected empty slice on unmarshal failure, got %#v", got)
		}
	})

	t.Run("set nil normalizes to empty json array, not null", func(t *testing.T) {
		c := Channel{}
		if err := c.SetManagedModelsBySync(nil); err != nil {
			t.Fatalf("SetManagedModelsBySync(nil) error: %v", err)
		}
		if c.ManagedModelsBySync != "[]" {
			t.Fatalf("expected literal empty array %q, got %q", "[]", c.ManagedModelsBySync)
		}
		// Round trip: GetManagedModelsBySync on "[]" must yield a valid empty
		// (non-nil-panicking) slice, proving Set/Get compose correctly.
		if got := c.GetManagedModelsBySync(); len(got) != 0 {
			t.Fatalf("round trip of empty set produced %#v", got)
		}
	})

	t.Run("set then get round trips model list exactly, preserving order", func(t *testing.T) {
		c := Channel{}
		models := []string{"model-a", "model-b", "model-c"}
		if err := c.SetManagedModelsBySync(models); err != nil {
			t.Fatalf("SetManagedModelsBySync error: %v", err)
		}
		got := c.GetManagedModelsBySync()
		if len(got) != len(models) {
			t.Fatalf("round trip length mismatch: got %#v want %#v", got, models)
		}
		for i := range models {
			if got[i] != models[i] {
				t.Fatalf("round trip[%d] = %q, want %q", i, got[i], models[i])
			}
		}
	})
}

func TestChannel_GetGroups(t *testing.T) {
	tests := []struct {
		name  string
		group string
		want  []string
	}{
		{"empty", "", []string{}},
		{"single default", "default", []string{"default"}},
		{"comma separated trims whitespace per entry", "default, vip , enterprise", []string{"default", "vip", "enterprise"}},
		{"trims leading/trailing comma", ",default,vip,", []string{"default", "vip"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Channel{Group: tt.group}
			got := c.GetGroups()
			if len(got) != len(tt.want) {
				t.Fatalf("GetGroups() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("GetGroups()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestChannel_OtherInfo_RoundTrip(t *testing.T) {
	c := Channel{Name: "chan-1"}
	if got := c.GetOtherInfo(); len(got) != 0 {
		t.Fatalf("expected empty map for unset OtherInfo, got %#v", got)
	}

	c.SetOtherInfo(map[string]interface{}{"vertex_project": "p1", "region": "us-central1"})
	got := c.GetOtherInfo()
	if got["vertex_project"] != "p1" || got["region"] != "us-central1" {
		t.Fatalf("OtherInfo round trip mismatch: %#v", got)
	}

	// Malformed underlying JSON must degrade to an empty map, never panic.
	c2 := Channel{Name: "chan-2", OtherInfo: "{broken"}
	if got := c2.GetOtherInfo(); len(got) != 0 {
		t.Fatalf("expected empty map on malformed OtherInfo, got %#v", got)
	}
}

func TestChannel_SetOtherInfo_MarshalFailureLeavesFieldUntouched(t *testing.T) {
	// A non-finite float (NaN) cannot be represented in JSON — encoding/json
	// rejects it. This is a real business scenario (a computed metric that
	// went NaN, e.g. 0/0 upstream) rather than a contrived unmarshalable
	// type, and it must fail safe: log and leave OtherInfo unmodified rather
	// than writing a partial/corrupt value.
	c := Channel{Name: "chan-nan", OtherInfo: "previous-value-should-survive"}
	c.SetOtherInfo(map[string]interface{}{"score": math.NaN()})
	if c.OtherInfo != "previous-value-should-survive" {
		t.Fatalf("SetOtherInfo on marshal failure overwrote OtherInfo: %q", c.OtherInfo)
	}
}

func TestChannel_TagAccessors(t *testing.T) {
	c := Channel{}
	if got := c.GetTag(); got != "" {
		t.Fatalf("GetTag() on nil pointer = %q, want empty string", got)
	}
	c.SetTag("prod")
	if got := c.GetTag(); got != "prod" {
		t.Fatalf("GetTag() after SetTag = %q, want %q", got, "prod")
	}
	c.SetTag("")
	if got := c.GetTag(); got != "" {
		t.Fatalf("GetTag() after SetTag(\"\") = %q, want empty string (not still-nil)", got)
	}
}

func TestChannel_GetAutoBan(t *testing.T) {
	one, zero := 1, 0
	tests := []struct {
		name    string
		autoBan *int
		want    bool
	}{
		{"nil pointer defaults false", nil, false},
		{"explicit 1 is true", &one, true},
		{"explicit 0 is false", &zero, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Channel{AutoBan: tt.autoBan}
			if got := c.GetAutoBan(); got != tt.want {
				t.Fatalf("GetAutoBan() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChannel_GetPriority(t *testing.T) {
	neg := int64(-5)
	pos := int64(42)
	tests := []struct {
		name     string
		priority *int64
		want     int64
	}{
		{"nil pointer defaults zero", nil, 0},
		{"negative priority preserved (lower priority wins scheduling elsewhere)", &neg, -5},
		{"positive priority preserved", &pos, 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Channel{Priority: tt.priority}
			if got := c.GetPriority(); got != tt.want {
				t.Fatalf("GetPriority() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChannel_GetWeight(t *testing.T) {
	zero := uint(0)
	pos := uint(10)
	tests := []struct {
		name   string
		weight *uint
		want   int
	}{
		{"nil pointer defaults zero", nil, 0},
		{"explicit zero weight (channel effectively excluded from weighted pick)", &zero, 0},
		{"positive weight", &pos, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Channel{Weight: tt.weight}
			if got := c.GetWeight(); got != tt.want {
				t.Fatalf("GetWeight() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChannel_GetBaseURL(t *testing.T) {
	custom := "https://custom.example.com"
	empty := ""
	tests := []struct {
		name    string
		channel Channel
		want    string
	}{
		{"nil pointer defaults empty", Channel{Type: constant.ChannelTypeOpenAI, BaseURL: nil}, ""},
		{"explicit empty string falls back to vendor default for type", Channel{Type: constant.ChannelTypeOpenAI, BaseURL: &empty}, constant.ChannelBaseURLs[constant.ChannelTypeOpenAI]},
		{"explicit non-empty overrides vendor default", Channel{Type: constant.ChannelTypeOpenAI, BaseURL: &custom}, custom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.channel.GetBaseURL(); got != tt.want {
				t.Fatalf("GetBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChannel_GetModelMapping_GetStatusCodeMapping(t *testing.T) {
	mapping := `{"gpt-4":"gpt-4-turbo"}`
	c := Channel{}
	if got := c.GetModelMapping(); got != "" {
		t.Fatalf("GetModelMapping() nil pointer = %q, want empty", got)
	}
	c.ModelMapping = &mapping
	if got := c.GetModelMapping(); got != mapping {
		t.Fatalf("GetModelMapping() = %q, want %q", got, mapping)
	}

	scm := `{"429":"503"}`
	c2 := Channel{}
	if got := c2.GetStatusCodeMapping(); got != "" {
		t.Fatalf("GetStatusCodeMapping() nil pointer = %q, want empty", got)
	}
	c2.StatusCodeMapping = &scm
	if got := c2.GetStatusCodeMapping(); got != scm {
		t.Fatalf("GetStatusCodeMapping() = %q, want %q", got, scm)
	}
}

func TestChannel_ValidateSettings(t *testing.T) {
	valid := `{"proxy":"http://127.0.0.1:1080"}`
	malformed := `{"proxy":`
	empty := ""
	tests := []struct {
		name    string
		setting *string
		wantErr bool
	}{
		{"nil setting is valid (no settings configured)", nil, false},
		{"empty string setting is valid", &empty, false},
		{"well-formed json is valid", &valid, false},
		{"malformed json is rejected", &malformed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Channel{Setting: tt.setting}
			err := c.ValidateSettings()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSettings() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestChannel_SetSetting_SetOtherSettings(t *testing.T) {
	c := Channel{}
	c.SetSetting(dto.ChannelSettings{Proxy: "socks5://127.0.0.1:1080", ForceFormat: true})
	if c.Setting == nil {
		t.Fatal("SetSetting did not populate the Setting pointer")
	}
	if !strings.Contains(*c.Setting, "socks5://127.0.0.1:1080") {
		t.Fatalf("SetSetting output missing proxy value: %s", *c.Setting)
	}
	// The value written must itself validate cleanly (round trip).
	if err := c.ValidateSettings(); err != nil {
		t.Fatalf("value written by SetSetting failed ValidateSettings: %v", err)
	}

	c.SetOtherSettings(dto.ChannelOtherSettings{AzureResponsesVersion: "2024-05-01"})
	if !strings.Contains(c.OtherSettings, "2024-05-01") {
		t.Fatalf("SetOtherSettings output missing version value: %s", c.OtherSettings)
	}
}

func TestChannel_GetParamOverride_GetHeaderOverride(t *testing.T) {
	empty := ""
	valid := `{"temperature":0.5}`
	malformed := `{"temperature":`

	c := Channel{}
	if got := c.GetParamOverride(); len(got) != 0 {
		t.Fatalf("GetParamOverride() nil pointer = %#v, want empty map", got)
	}
	c.ParamOverride = &empty
	if got := c.GetParamOverride(); len(got) != 0 {
		t.Fatalf("GetParamOverride() empty string = %#v, want empty map", got)
	}
	c.ParamOverride = &valid
	got := c.GetParamOverride()
	if got["temperature"] != 0.5 {
		t.Fatalf("GetParamOverride() = %#v, want temperature=0.5", got)
	}
	c.ParamOverride = &malformed
	if got := c.GetParamOverride(); len(got) != 0 {
		t.Fatalf("GetParamOverride() malformed = %#v, want empty map (not panic)", got)
	}

	hc := Channel{}
	if got := hc.GetHeaderOverride(); len(got) != 0 {
		t.Fatalf("GetHeaderOverride() nil pointer = %#v, want empty map", got)
	}
	hc.HeaderOverride = &empty
	if got := hc.GetHeaderOverride(); len(got) != 0 {
		t.Fatalf("GetHeaderOverride() empty string = %#v, want empty map", got)
	}
	hdrValid := `{"X-Custom":"1"}`
	hc.HeaderOverride = &hdrValid
	if got := hc.GetHeaderOverride(); got["X-Custom"] != "1" {
		t.Fatalf("GetHeaderOverride() = %#v, want X-Custom=1", got)
	}
	hdrMalformed := `{"X-Custom":`
	hc.HeaderOverride = &hdrMalformed
	if got := hc.GetHeaderOverride(); len(got) != 0 {
		t.Fatalf("GetHeaderOverride() malformed = %#v, want empty map (not panic)", got)
	}
}

func TestChannel_GetNextEnabledKeyIndex(t *testing.T) {
	t.Run("non multi-key channel returns the single Key verbatim", func(t *testing.T) {
		c := Channel{Key: "sk-solo"}
		key, idx, apiErr := c.GetNextEnabledKeyIndex(nil)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if key != "sk-solo" || idx != 0 {
			t.Fatalf("got key=%q idx=%d, want key=sk-solo idx=0", key, idx)
		}
	})

	t.Run("multi-key with zero keys returns ErrorCodeChannelNoAvailableKey", func(t *testing.T) {
		c := Channel{Key: "", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		_, _, apiErr := c.GetNextEnabledKeyIndex(nil)
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Fatalf("got %v, want ErrorCodeChannelNoAvailableKey", apiErr)
		}
	})

	t.Run("nil statusList treats every key as enabled, returns first", func(t *testing.T) {
		c := Channel{Key: "k1\nk2\nk3", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		key, idx, apiErr := c.GetNextEnabledKeyIndex(nil)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if key != "k1" || idx != 0 {
			t.Fatalf("got key=%q idx=%d, want k1/0", key, idx)
		}
	})

	t.Run("skips disabled leading keys, returns first enabled", func(t *testing.T) {
		c := Channel{Key: "k1\nk2\nk3", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		statusList := map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusEnabled}
		key, idx, apiErr := c.GetNextEnabledKeyIndex(statusList)
		if apiErr != nil {
			t.Fatalf("unexpected error: %v", apiErr)
		}
		if key != "k2" || idx != 1 {
			t.Fatalf("got key=%q idx=%d, want k2/1", key, idx)
		}
	})

	t.Run("all keys disabled with no cooldown data returns permanent no-available-key", func(t *testing.T) {
		c := Channel{Key: "k1\nk2", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		statusList := map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusManuallyDisabled}
		_, _, apiErr := c.GetNextEnabledKeyIndex(statusList)
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Fatalf("got %v, want ErrorCodeChannelNoAvailableKey", apiErr)
		}
	})

	t.Run("all keys disabled but all cooling returns retryable AllKeysCooling with earliest deadline", func(t *testing.T) {
		c := Channel{
			Key: "k1\nk2",
			ChannelInfo: ChannelInfo{
				IsMultiKey:            true,
				MultiKeyCooldownUntil: map[int]int64{0: 2000, 1: 1000},
			},
		}
		statusList := map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusManuallyDisabled}
		_, _, apiErr := c.GetNextEnabledKeyIndex(statusList)
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelAllKeysCooling {
			t.Fatalf("got %v, want ErrorCodeChannelAllKeysCooling", apiErr)
		}
		if apiErr.RetryAfterUnix != 1000 {
			t.Fatalf("RetryAfterUnix = %d, want earliest deadline 1000", apiErr.RetryAfterUnix)
		}
	})

	t.Run("mixed: one disabled key permanently disabled (no cooldown entry) downgrades to permanent error", func(t *testing.T) {
		// Business rule: "all cooling" must only be claimed when EVERY disabled
		// key is provably temporary. One permanently-disabled key in the mix
		// must not let the relay retry a channel that can never recover.
		c := Channel{
			Key: "k1\nk2",
			ChannelInfo: ChannelInfo{
				IsMultiKey:            true,
				MultiKeyCooldownUntil: map[int]int64{0: 2000}, // key 1 has no cooldown entry
			},
		}
		statusList := map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusManuallyDisabled}
		_, _, apiErr := c.GetNextEnabledKeyIndex(statusList)
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Fatalf("got %v, want ErrorCodeChannelNoAvailableKey (mixed permanent+cooling must not claim all-cooling)", apiErr)
		}
	})

	t.Run("cooldown entry present but non-positive treated as permanent", func(t *testing.T) {
		c := Channel{
			Key: "k1",
			ChannelInfo: ChannelInfo{
				IsMultiKey:            true,
				MultiKeyCooldownUntil: map[int]int64{0: 0},
			},
		}
		statusList := map[int]int{0: common.ChannelStatusManuallyDisabled}
		_, _, apiErr := c.GetNextEnabledKeyIndex(statusList)
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Fatalf("got %v, want ErrorCodeChannelNoAvailableKey for non-positive cooldown deadline", apiErr)
		}
	})
}

func TestChannel_NoAvailableKeyError_DirectEdges(t *testing.T) {
	// Direct coverage of the keyCount==0 defensive branch, which
	// GetNextEnabledKeyIndex's own len(keys)==0 guard prevents reaching in
	// normal flow but which must still fail safe if called directly.
	c := &Channel{}
	apiErr := c.noAvailableKeyError(func(int) int { return common.ChannelStatusEnabled }, 0)
	if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
		t.Fatalf("got %v, want ErrorCodeChannelNoAvailableKey for zero keyCount", apiErr)
	}

	t.Run("loop skips indices reported enabled even when cooldown data exists for others", func(t *testing.T) {
		c := &Channel{ChannelInfo: ChannelInfo{MultiKeyCooldownUntil: map[int]int64{1: 100}}}
		getStatus := func(i int) int {
			if i == 0 {
				return common.ChannelStatusEnabled
			}
			return common.ChannelStatusManuallyDisabled
		}
		apiErr := c.noAvailableKeyError(getStatus, 2)
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelAllKeysCooling {
			t.Fatalf("got %v, want ErrorCodeChannelAllKeysCooling (index 0 enabled must be skipped, not counted as permanent)", apiErr)
		}
		if apiErr.RetryAfterUnix != 100 {
			t.Fatalf("RetryAfterUnix = %d, want 100", apiErr.RetryAfterUnix)
		}
	})

	t.Run("no disabled index found (defensive: caller claims all enabled) falls back to permanent error", func(t *testing.T) {
		c := &Channel{ChannelInfo: ChannelInfo{MultiKeyCooldownUntil: map[int]int64{0: 100, 1: 200}}}
		getStatus := func(int) int { return common.ChannelStatusEnabled }
		apiErr := c.noAvailableKeyError(getStatus, 2)
		if apiErr == nil || apiErr.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Fatalf("got %v, want ErrorCodeChannelNoAvailableKey when the loop finds no disabled key to cool down from", apiErr)
		}
	})
}

func TestChannelInfo_Value_Scan_RoundTrip(t *testing.T) {
	ci := ChannelInfo{
		IsMultiKey:           true,
		MultiKeySize:         3,
		MultiKeyStatusList:   map[int]int{0: 1, 1: 2},
		MultiKeyPollingIndex: 1,
		MultiKeyMode:         constant.MultiKeyModePolling,
	}
	v, err := ci.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	raw, ok := v.([]byte)
	if !ok {
		t.Fatalf("Value() returned %T, want []byte", v)
	}

	var scanned ChannelInfo
	if err := scanned.Scan(raw); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if scanned.MultiKeySize != 3 || !scanned.IsMultiKey || scanned.MultiKeyMode != constant.MultiKeyModePolling {
		t.Fatalf("Scan() round trip mismatch: %#v", scanned)
	}
	if scanned.MultiKeyStatusList[0] != 1 || scanned.MultiKeyStatusList[1] != 2 {
		t.Fatalf("Scan() lost status list: %#v", scanned.MultiKeyStatusList)
	}
}
