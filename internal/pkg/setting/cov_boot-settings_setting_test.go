package setting

import (
	"encoding/json"
	"testing"
)

// boot_settings_restoreAutoGroups snapshots and restores the package-level
// autoGroups slice so tests don't leak state into other tests in this package
// (autoGroups is mutated in-place by UpdateAutoGroupsByJsonString).
func boot_settings_restoreAutoGroups(t *testing.T) {
	t.Helper()
	orig := autoGroups
	t.Cleanup(func() { autoGroups = orig })
}

func TestContainsAutoGroup(t *testing.T) {
	boot_settings_restoreAutoGroups(t)
	autoGroups = []string{"default", "vip"}

	if !ContainsAutoGroup("default") {
		t.Fatalf("expected 'default' to be a recognized auto group")
	}
	if ContainsAutoGroup("nonexistent") {
		t.Fatalf("expected 'nonexistent' to NOT be a recognized auto group")
	}
	if ContainsAutoGroup("") {
		t.Fatalf("expected empty string to NOT be a recognized auto group")
	}
}

func TestUpdateAutoGroupsByJsonString(t *testing.T) {
	boot_settings_restoreAutoGroups(t)

	// Business case: admin pushes a new auto-group list via JSON config.
	if err := UpdateAutoGroupsByJsonString(`["default","enterprise"]`); err != nil {
		t.Fatalf("unexpected error updating valid group list: %v", err)
	}
	got := GetAutoGroups()
	if len(got) != 2 || got[0] != "default" || got[1] != "enterprise" {
		t.Fatalf("expected [default enterprise], got %v", got)
	}
	if !ContainsAutoGroup("enterprise") {
		t.Fatalf("expected newly added group 'enterprise' to be recognized")
	}

	// Edge case: malformed JSON must be rejected, not silently accepted.
	if err := UpdateAutoGroupsByJsonString(`not-json`); err == nil {
		t.Fatalf("expected error for malformed JSON group list")
	}
	// The malformed update still resets to an empty slice per current code path
	// (autoGroups = make([]string, 0) runs before Unmarshal fails).
	if len(GetAutoGroups()) != 0 {
		t.Fatalf("expected autoGroups cleared to empty after failed unmarshal, got %v", GetAutoGroups())
	}
}

func TestAutoGroups2JsonString_RoundTrip(t *testing.T) {
	boot_settings_restoreAutoGroups(t)
	autoGroups = []string{"default", "team-a"}

	jsonStr := AutoGroups2JsonString()
	var roundTripped []string
	if err := json.Unmarshal([]byte(jsonStr), &roundTripped); err != nil {
		t.Fatalf("AutoGroups2JsonString produced invalid JSON: %v", err)
	}
	if len(roundTripped) != 2 || roundTripped[0] != "default" || roundTripped[1] != "team-a" {
		t.Fatalf("round-trip mismatch: got %v", roundTripped)
	}
}

// --- Chats ---

func boot_settings_restoreChats(t *testing.T) {
	t.Helper()
	orig := Chats
	t.Cleanup(func() { Chats = orig })
}

func TestUpdateChatsByJsonString(t *testing.T) {
	boot_settings_restoreChats(t)

	if err := UpdateChatsByJsonString(`[{"Custom Client":"custom://open?key={key}"}]`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(Chats) != 1 || Chats[0]["Custom Client"] != "custom://open?key={key}" {
		t.Fatalf("expected custom chat entry to be applied, got %v", Chats)
	}

	if err := UpdateChatsByJsonString(`{invalid`); err == nil {
		t.Fatalf("expected error for malformed chats JSON")
	}
}

func TestChats2JsonString_RoundTrip(t *testing.T) {
	boot_settings_restoreChats(t)
	Chats = []map[string]string{{"A": "a://x"}, {"B": "b://y"}}

	s := Chats2JsonString()
	var back []map[string]string
	if err := json.Unmarshal([]byte(s), &back); err != nil {
		t.Fatalf("Chats2JsonString produced invalid JSON: %v", err)
	}
	if len(back) != 2 || back[0]["A"] != "a://x" || back[1]["B"] != "b://y" {
		t.Fatalf("round trip mismatch: %v", back)
	}
}

// --- Sensitive words ---

func boot_settings_restoreSensitive(t *testing.T) {
	t.Helper()
	origWords := SensitiveWords
	origCheck := CheckSensitiveEnabled
	origPrompt := CheckSensitiveOnPromptEnabled
	t.Cleanup(func() {
		SensitiveWords = origWords
		CheckSensitiveEnabled = origCheck
		CheckSensitiveOnPromptEnabled = origPrompt
	})
}

func TestSensitiveWordsToString(t *testing.T) {
	boot_settings_restoreSensitive(t)
	SensitiveWords = []string{"badword1", "badword2"}

	got := SensitiveWordsToString()
	want := "badword1\nbadword2"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSensitiveWordsFromString_EdgeCases(t *testing.T) {
	boot_settings_restoreSensitive(t)

	// Business rule: blank lines and pure-whitespace lines must be dropped,
	// and surrounding whitespace on real entries trimmed.
	SensitiveWordsFromString("foo\n\n  bar  \n\t\nbaz")
	want := []string{"foo", "bar", "baz"}
	if len(SensitiveWords) != len(want) {
		t.Fatalf("expected %v, got %v", want, SensitiveWords)
	}
	for i := range want {
		if SensitiveWords[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, SensitiveWords)
		}
	}

	// Edge case: an entirely empty/whitespace input produces zero words, not
	// a single empty-string entry.
	SensitiveWordsFromString("   \n  \n")
	if len(SensitiveWords) != 0 {
		t.Fatalf("expected zero sensitive words for blank input, got %v", SensitiveWords)
	}
}

func TestShouldCheckPromptSensitive(t *testing.T) {
	boot_settings_restoreSensitive(t)

	cases := []struct {
		name          string
		checkEnabled  bool
		promptEnabled bool
		want          bool
	}{
		{"both enabled", true, true, true},
		{"master switch off", false, true, false},
		{"prompt switch off", true, false, false},
		{"both off", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			CheckSensitiveEnabled = tc.checkEnabled
			CheckSensitiveOnPromptEnabled = tc.promptEnabled
			if got := ShouldCheckPromptSensitive(); got != tc.want {
				t.Fatalf("ShouldCheckPromptSensitive() = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- User usable groups ---

func boot_settings_restoreUserUsableGroups(t *testing.T) {
	t.Helper()
	userUsableGroupsMutex.Lock()
	orig := userUsableGroups
	userUsableGroupsMutex.Unlock()
	t.Cleanup(func() {
		userUsableGroupsMutex.Lock()
		userUsableGroups = orig
		userUsableGroupsMutex.Unlock()
	})
}

func TestGetUserUsableGroupsCopy_IsDefensiveCopy(t *testing.T) {
	boot_settings_restoreUserUsableGroups(t)
	userUsableGroupsMutex.Lock()
	userUsableGroups = map[string]string{"default": "默认分组"}
	userUsableGroupsMutex.Unlock()

	c := GetUserUsableGroupsCopy()
	c["default"] = "mutated"
	c["new"] = "injected"

	// Mutating the returned copy must not leak back into package state —
	// otherwise callers could corrupt shared config through the getter.
	if desc := GetUsableGroupDescription("default"); desc != "默认分组" {
		t.Fatalf("expected original description preserved, got %q (copy leaked into source map)", desc)
	}
	if _, found := GetUserUsableGroupsCopy()["new"]; found {
		t.Fatalf("expected mutation of copy to not add 'new' to source map")
	}
}

func TestUpdateUserUsableGroupsByJSONString(t *testing.T) {
	boot_settings_restoreUserUsableGroups(t)

	if err := UpdateUserUsableGroupsByJSONString(`{"vip":"VIP分组","gold":"黄金分组"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc := GetUsableGroupDescription("vip"); desc != "VIP分组" {
		t.Fatalf("expected 'VIP分组', got %q", desc)
	}

	if err := UpdateUserUsableGroupsByJSONString(`not-json`); err == nil {
		t.Fatalf("expected error for malformed JSON")
	}
}

func TestGetUsableGroupDescription_FallsBackToGroupName(t *testing.T) {
	boot_settings_restoreUserUsableGroups(t)
	userUsableGroupsMutex.Lock()
	userUsableGroups = map[string]string{"default": "默认分组"}
	userUsableGroupsMutex.Unlock()

	// Edge case: unknown group name should echo back the raw name rather than
	// erroring or returning an empty string — callers use this for display.
	if got := GetUsableGroupDescription("unregistered-group"); got != "unregistered-group" {
		t.Fatalf("expected fallback to raw group name, got %q", got)
	}
}

// --- Model request rate limit ---

func boot_settings_restoreRateLimitGroup(t *testing.T) {
	t.Helper()
	ModelRequestRateLimitMutex.Lock()
	orig := ModelRequestRateLimitGroup
	ModelRequestRateLimitMutex.Unlock()
	t.Cleanup(func() {
		ModelRequestRateLimitMutex.Lock()
		ModelRequestRateLimitGroup = orig
		ModelRequestRateLimitMutex.Unlock()
	})
}

func TestModelRequestRateLimitGroup2JSONString(t *testing.T) {
	boot_settings_restoreRateLimitGroup(t)
	if err := UpdateModelRequestRateLimitGroupByJSONString(`{"default":[100,50]}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := ModelRequestRateLimitGroup2JSONString()
	var back map[string][2]int
	if err := json.Unmarshal([]byte(s), &back); err != nil {
		t.Fatalf("produced invalid JSON: %v", err)
	}
	if back["default"][0] != 100 || back["default"][1] != 50 {
		t.Fatalf("expected [100 50], got %v", back["default"])
	}
}

func TestCheckModelRequestRateLimitGroup(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid limits", `{"default":[100,50]}`, false},
		{"negative total count rejected", `{"default":[-1,50]}`, true},
		{"success count below 1 rejected", `{"default":[100,0]}`, true},
		{"malformed json rejected", `not-json`, true},
		{"zero total count is allowed (unlimited semantics)", `{"default":[0,1]}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckModelRequestRateLimitGroup(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for input %q", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
		})
	}
}

func TestCheckModelRequestRateLimitGroup_MaxInt32Boundary(t *testing.T) {
	// FINDING: values exactly at math.MaxInt32 are accepted (only strictly
	// greater is rejected); this test locks the current boundary behavior.
	okAtBoundary := `{"g":[2147483647,2147483647]}`
	if err := CheckModelRequestRateLimitGroup(okAtBoundary); err != nil {
		t.Fatalf("expected MaxInt32 boundary to be accepted, got error: %v", err)
	}

	overBoundary := `{"g":[2147483648,1]}`
	if err := CheckModelRequestRateLimitGroup(overBoundary); err == nil {
		t.Fatalf("expected value above MaxInt32 to be rejected")
	}
}

func TestGetGroupRateLimit_NilMapReturnsNotFound(t *testing.T) {
	boot_settings_restoreRateLimitGroup(t)
	ModelRequestRateLimitMutex.Lock()
	ModelRequestRateLimitGroup = nil
	ModelRequestRateLimitMutex.Unlock()

	total, success, found := GetGroupRateLimit("anything")
	if found || total != 0 || success != 0 {
		t.Fatalf("expected (0,0,false) for nil group map, got (%d,%d,%v)", total, success, found)
	}
}
