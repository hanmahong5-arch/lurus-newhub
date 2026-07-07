package setting

import (
	"testing"
)

// --- auto_group.go ---

func TestContainsAutoGroup(t *testing.T) {
	orig := autoGroups
	t.Cleanup(func() { autoGroups = orig })

	autoGroups = []string{"default", "vip"}

	cases := []struct {
		group string
		want  bool
	}{
		{"default", true},
		{"vip", true},
		{"nonexistent", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ContainsAutoGroup(c.group); got != c.want {
			t.Errorf("ContainsAutoGroup(%q) = %v, want %v", c.group, got, c.want)
		}
	}
}

func TestUpdateAutoGroupsByJsonString(t *testing.T) {
	orig := autoGroups
	t.Cleanup(func() { autoGroups = orig })

	if err := UpdateAutoGroupsByJsonString(`["a","b","c"]`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := GetAutoGroups()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("index %d: got %q, want %q", i, got[i], v)
		}
	}

	// invalid JSON returns error
	if err := UpdateAutoGroupsByJsonString(`not json`); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestAutoGroups2JsonString(t *testing.T) {
	orig := autoGroups
	t.Cleanup(func() { autoGroups = orig })

	autoGroups = []string{"g1", "g2"}
	got := AutoGroups2JsonString()
	want := `["g1","g2"]`
	if got != want {
		t.Errorf("AutoGroups2JsonString() = %q, want %q", got, want)
	}

	autoGroups = []string{}
	got = AutoGroups2JsonString()
	want = `[]`
	if got != want {
		t.Errorf("AutoGroups2JsonString() with empty slice = %q, want %q", got, want)
	}
}

func TestGetAutoGroups(t *testing.T) {
	orig := autoGroups
	t.Cleanup(func() { autoGroups = orig })

	autoGroups = []string{"only-one"}
	got := GetAutoGroups()
	if len(got) != 1 || got[0] != "only-one" {
		t.Errorf("GetAutoGroups() = %v, want [only-one]", got)
	}
}

// --- chat.go ---

func TestUpdateChatsByJsonString(t *testing.T) {
	orig := Chats
	t.Cleanup(func() { Chats = orig })

	if err := UpdateChatsByJsonString(`[{"a":"b"},{"c":"d"}]`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(Chats) != 2 {
		t.Fatalf("got %d entries, want 2", len(Chats))
	}
	if Chats[0]["a"] != "b" {
		t.Errorf("Chats[0][a] = %q, want b", Chats[0]["a"])
	}
	if Chats[1]["c"] != "d" {
		t.Errorf("Chats[1][c] = %q, want d", Chats[1]["c"])
	}

	if err := UpdateChatsByJsonString(`not json`); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestChats2JsonString(t *testing.T) {
	orig := Chats
	t.Cleanup(func() { Chats = orig })

	Chats = []map[string]string{{"x": "y"}}
	got := Chats2JsonString()
	want := `[{"x":"y"}]`
	if got != want {
		t.Errorf("Chats2JsonString() = %q, want %q", got, want)
	}

	Chats = []map[string]string{}
	got = Chats2JsonString()
	want = `[]`
	if got != want {
		t.Errorf("Chats2JsonString() empty = %q, want %q", got, want)
	}
}

// --- midjourney.go ---
// Package-level bool vars only; assert their documented default values.

func TestMidjourneyDefaults(t *testing.T) {
	if MjNotifyEnabled != false {
		t.Errorf("MjNotifyEnabled default = %v, want false", MjNotifyEnabled)
	}
	if MjAccountFilterEnabled != false {
		t.Errorf("MjAccountFilterEnabled default = %v, want false", MjAccountFilterEnabled)
	}
	if MjModeClearEnabled != false {
		t.Errorf("MjModeClearEnabled default = %v, want false", MjModeClearEnabled)
	}
	if MjForwardUrlEnabled != true {
		t.Errorf("MjForwardUrlEnabled default = %v, want true", MjForwardUrlEnabled)
	}
	if MjActionCheckSuccessEnabled != true {
		t.Errorf("MjActionCheckSuccessEnabled default = %v, want true", MjActionCheckSuccessEnabled)
	}
}

// --- rate_limit.go ---

func TestModelRequestRateLimitGroup2JSONString(t *testing.T) {
	orig := ModelRequestRateLimitGroup
	t.Cleanup(func() { ModelRequestRateLimitGroup = orig })

	ModelRequestRateLimitGroup = map[string][2]int{"g1": {1, 2}}
	got := ModelRequestRateLimitGroup2JSONString()
	want := `{"g1":[1,2]}`
	if got != want {
		t.Errorf("ModelRequestRateLimitGroup2JSONString() = %q, want %q", got, want)
	}
}

func TestUpdateModelRequestRateLimitGroupByJSONString(t *testing.T) {
	orig := ModelRequestRateLimitGroup
	t.Cleanup(func() { ModelRequestRateLimitGroup = orig })

	if err := UpdateModelRequestRateLimitGroupByJSONString(`{"g1":[3,4]}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	limits, ok := ModelRequestRateLimitGroup["g1"]
	if !ok {
		t.Fatal("expected g1 key to exist")
	}
	if limits[0] != 3 || limits[1] != 4 {
		t.Errorf("limits = %v, want [3 4]", limits)
	}

	if err := UpdateModelRequestRateLimitGroupByJSONString(`not json`); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestGetGroupRateLimit(t *testing.T) {
	orig := ModelRequestRateLimitGroup
	t.Cleanup(func() { ModelRequestRateLimitGroup = orig })

	// nil map branch
	ModelRequestRateLimitGroup = nil
	total, success, found := GetGroupRateLimit("any")
	if total != 0 || success != 0 || found != false {
		t.Errorf("nil map: got (%d,%d,%v), want (0,0,false)", total, success, found)
	}

	// not found branch
	ModelRequestRateLimitGroup = map[string][2]int{"g1": {5, 6}}
	total, success, found = GetGroupRateLimit("missing")
	if total != 0 || success != 0 || found != false {
		t.Errorf("missing key: got (%d,%d,%v), want (0,0,false)", total, success, found)
	}

	// found branch
	total, success, found = GetGroupRateLimit("g1")
	if total != 5 || success != 6 || found != true {
		t.Errorf("found key: got (%d,%d,%v), want (5,6,true)", total, success, found)
	}
}

func TestCheckModelRequestRateLimitGroup(t *testing.T) {
	cases := []struct {
		name    string
		jsonStr string
		wantErr bool
	}{
		{"valid", `{"g1":[1,2]}`, false},
		{"invalid json", `not json`, true},
		{"negative total", `{"g1":[-1,2]}`, true},
		{"success below 1", `{"g1":[1,0]}`, true},
		{"total exceeds max int32", `{"g1":[2147483648,2]}`, true},
		{"success exceeds max int32", `{"g1":[1,2147483648]}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckModelRequestRateLimitGroup(c.jsonStr)
			if (err != nil) != c.wantErr {
				t.Errorf("CheckModelRequestRateLimitGroup(%q) error = %v, wantErr %v", c.jsonStr, err, c.wantErr)
			}
		})
	}
}

// --- sensitive.go ---

func TestSensitiveWordsToString(t *testing.T) {
	orig := SensitiveWords
	t.Cleanup(func() { SensitiveWords = orig })

	SensitiveWords = []string{"foo", "bar", "baz"}
	got := SensitiveWordsToString()
	want := "foo\nbar\nbaz"
	if got != want {
		t.Errorf("SensitiveWordsToString() = %q, want %q", got, want)
	}

	SensitiveWords = []string{}
	got = SensitiveWordsToString()
	if got != "" {
		t.Errorf("SensitiveWordsToString() empty = %q, want empty string", got)
	}
}

func TestSensitiveWordsFromString(t *testing.T) {
	orig := SensitiveWords
	t.Cleanup(func() { SensitiveWords = orig })

	SensitiveWordsFromString("foo\n bar \n\n baz\n")
	want := []string{"foo", "bar", "baz"}
	if len(SensitiveWords) != len(want) {
		t.Fatalf("got %v, want %v", SensitiveWords, want)
	}
	for i, w := range want {
		if SensitiveWords[i] != w {
			t.Errorf("index %d: got %q, want %q", i, SensitiveWords[i], w)
		}
	}

	// all-blank input yields empty slice
	SensitiveWordsFromString("   \n  \n")
	if len(SensitiveWords) != 0 {
		t.Errorf("blank input: got %v, want empty slice", SensitiveWords)
	}
}

func TestShouldCheckPromptSensitive(t *testing.T) {
	origCheck := CheckSensitiveEnabled
	origPrompt := CheckSensitiveOnPromptEnabled
	t.Cleanup(func() {
		CheckSensitiveEnabled = origCheck
		CheckSensitiveOnPromptEnabled = origPrompt
	})

	cases := []struct {
		checkEnabled  bool
		promptEnabled bool
		want          bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, c := range cases {
		CheckSensitiveEnabled = c.checkEnabled
		CheckSensitiveOnPromptEnabled = c.promptEnabled
		if got := ShouldCheckPromptSensitive(); got != c.want {
			t.Errorf("ShouldCheckPromptSensitive() with (%v,%v) = %v, want %v",
				c.checkEnabled, c.promptEnabled, got, c.want)
		}
	}
}

// --- user_usable_group.go ---

func TestGetUserUsableGroupsCopy(t *testing.T) {
	orig := userUsableGroups
	t.Cleanup(func() { userUsableGroups = orig })

	userUsableGroups = map[string]string{"default": "默认分组"}
	got := GetUserUsableGroupsCopy()
	if len(got) != 1 || got["default"] != "默认分组" {
		t.Fatalf("got %v, want map[default:默认分组]", got)
	}

	// verify it's a real copy: mutating it must not affect the package var
	got["default"] = "mutated"
	if userUsableGroups["default"] != "默认分组" {
		t.Errorf("mutation of copy leaked into userUsableGroups: %v", userUsableGroups["default"])
	}
}

func TestUserUsableGroups2JSONString(t *testing.T) {
	orig := userUsableGroups
	t.Cleanup(func() { userUsableGroups = orig })

	userUsableGroups = map[string]string{"only": "one"}
	got := UserUsableGroups2JSONString()
	want := `{"only":"one"}`
	if got != want {
		t.Errorf("UserUsableGroups2JSONString() = %q, want %q", got, want)
	}
}

func TestUpdateUserUsableGroupsByJSONString(t *testing.T) {
	orig := userUsableGroups
	t.Cleanup(func() { userUsableGroups = orig })

	if err := UpdateUserUsableGroupsByJSONString(`{"a":"b"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if userUsableGroups["a"] != "b" {
		t.Errorf("userUsableGroups[a] = %q, want b", userUsableGroups["a"])
	}

	if err := UpdateUserUsableGroupsByJSONString(`not json`); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestGetUsableGroupDescription(t *testing.T) {
	orig := userUsableGroups
	t.Cleanup(func() { userUsableGroups = orig })

	userUsableGroups = map[string]string{"vip": "vip分组"}

	if got := GetUsableGroupDescription("vip"); got != "vip分组" {
		t.Errorf("GetUsableGroupDescription(vip) = %q, want vip分组", got)
	}
	// not-found branch falls back to the group name itself
	if got := GetUsableGroupDescription("unknown"); got != "unknown" {
		t.Errorf("GetUsableGroupDescription(unknown) = %q, want unknown", got)
	}
}
