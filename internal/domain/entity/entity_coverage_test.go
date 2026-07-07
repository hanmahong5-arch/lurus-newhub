package entity

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/types"
)

// ---------------- Channel ----------------

func TestChannelInfoValueScan(t *testing.T) {
	ci := ChannelInfo{
		IsMultiKey:           true,
		MultiKeySize:         2,
		MultiKeyStatusList:   map[int]int{0: 1},
		MultiKeyPollingIndex: 1,
	}
	v, err := ci.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	b, ok := v.([]byte)
	if !ok {
		t.Fatalf("Value() returned %T, want []byte", v)
	}

	var out ChannelInfo
	if err := out.Scan(b); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if !out.IsMultiKey || out.MultiKeySize != 2 || out.MultiKeyPollingIndex != 1 {
		t.Errorf("Scan() roundtrip mismatch: got %+v", out)
	}
}

func TestChannelGetKeys(t *testing.T) {
	tests := []struct {
		name string
		ch   Channel
		want []string
	}{
		{"empty key", Channel{Key: ""}, []string{}},
		{"cached keys short-circuit", Channel{Key: "x", Keys: []string{"cached"}}, []string{"cached"}},
		{"json array", Channel{Key: `["a","b"]`}, []string{`"a"`, `"b"`}},
		{"newline split", Channel{Key: "k1\nk2"}, []string{"k1", "k2"}},
		{"single key", Channel{Key: "onlykey"}, []string{"onlykey"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ch.GetKeys()
			if len(got) != len(tt.want) {
				t.Fatalf("GetKeys() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("GetKeys()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestChannelGetModels(t *testing.T) {
	if got := (&Channel{Models: ""}).GetModels(); len(got) != 0 {
		t.Errorf("empty Models -> %v, want empty slice", got)
	}
	got := (&Channel{Models: "gpt-4,gpt-3.5"}).GetModels()
	want := []string{"gpt-4", "gpt-3.5"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("GetModels() = %v, want %v", got, want)
	}
}

func TestChannelManagedModelsBySync(t *testing.T) {
	ch := &Channel{}
	if got := ch.GetManagedModelsBySync(); len(got) != 0 {
		t.Errorf("empty -> %v, want empty", got)
	}

	if err := ch.SetManagedModelsBySync([]string{"m1", "m2"}); err != nil {
		t.Fatalf("SetManagedModelsBySync error = %v", err)
	}
	if ch.ManagedModelsBySync != `["m1","m2"]` {
		t.Errorf("ManagedModelsBySync = %q, want %q", ch.ManagedModelsBySync, `["m1","m2"]`)
	}
	got := ch.GetManagedModelsBySync()
	if len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
		t.Errorf("GetManagedModelsBySync() = %v, want [m1 m2]", got)
	}

	// nil input normalizes to empty array
	ch2 := &Channel{}
	if err := ch2.SetManagedModelsBySync(nil); err != nil {
		t.Fatalf("SetManagedModelsBySync(nil) error = %v", err)
	}
	if ch2.ManagedModelsBySync != "[]" {
		t.Errorf("ManagedModelsBySync = %q, want []", ch2.ManagedModelsBySync)
	}

	// malformed JSON -> empty slice + logged, not error
	ch3 := &Channel{ManagedModelsBySync: "not-json"}
	if got := ch3.GetManagedModelsBySync(); len(got) != 0 {
		t.Errorf("malformed JSON -> %v, want empty", got)
	}
}

func TestChannelGetGroups(t *testing.T) {
	if got := (&Channel{Group: ""}).GetGroups(); len(got) != 0 {
		t.Errorf("empty Group -> %v, want empty", got)
	}
	got := (&Channel{Group: "a, b ,c"}).GetGroups()
	want := []string{"a", "b", "c"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("GetGroups()[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestChannelOtherInfo(t *testing.T) {
	ch := &Channel{Name: "c1"}
	if got := ch.GetOtherInfo(); len(got) != 0 {
		t.Errorf("empty OtherInfo -> %v, want empty map", got)
	}
	ch.SetOtherInfo(map[string]interface{}{"k": "v"})
	got := ch.GetOtherInfo()
	if got["k"] != "v" {
		t.Errorf("GetOtherInfo() = %v, want map[k:v]", got)
	}

	// malformed JSON -> falls back to empty map, no panic
	ch2 := &Channel{OtherInfo: "{bad json", Name: "c2"}
	if got := ch2.GetOtherInfo(); len(got) != 0 {
		t.Errorf("malformed OtherInfo -> %v, want empty map", got)
	}
}

func TestChannelTagAutoBanPriorityWeight(t *testing.T) {
	ch := &Channel{}
	if got := ch.GetTag(); got != "" {
		t.Errorf("nil Tag -> %q, want empty", got)
	}
	ch.SetTag("prod")
	if got := ch.GetTag(); got != "prod" {
		t.Errorf("GetTag() = %q, want prod", got)
	}

	if got := ch.GetAutoBan(); got != false {
		t.Errorf("nil AutoBan -> %v, want false", got)
	}
	one := 1
	ch.AutoBan = &one
	if got := ch.GetAutoBan(); got != true {
		t.Errorf("AutoBan=1 -> %v, want true", got)
	}
	zero := 0
	ch.AutoBan = &zero
	if got := ch.GetAutoBan(); got != false {
		t.Errorf("AutoBan=0 -> %v, want false", got)
	}

	if got := ch.GetPriority(); got != 0 {
		t.Errorf("nil Priority -> %d, want 0", got)
	}
	p := int64(42)
	ch.Priority = &p
	if got := ch.GetPriority(); got != 42 {
		t.Errorf("GetPriority() = %d, want 42", got)
	}

	if got := ch.GetWeight(); got != 0 {
		t.Errorf("nil Weight -> %d, want 0", got)
	}
	w := uint(7)
	ch.Weight = &w
	if got := ch.GetWeight(); got != 7 {
		t.Errorf("GetWeight() = %d, want 7", got)
	}
}

func TestChannelGetBaseURL(t *testing.T) {
	ch := &Channel{}
	if got := ch.GetBaseURL(); got != "" {
		t.Errorf("nil BaseURL, type 0 -> %q, want empty", got)
	}
	empty := ""
	ch.BaseURL = &empty
	ch.Type = 1 // openai
	if got := ch.GetBaseURL(); got != "https://api.openai.com" {
		t.Errorf("empty BaseURL falls back to constant.ChannelBaseURLs[1] = %q, want https://api.openai.com", got)
	}
	custom := "https://custom.example.com"
	ch.BaseURL = &custom
	if got := ch.GetBaseURL(); got != custom {
		t.Errorf("GetBaseURL() = %q, want %q", got, custom)
	}
}

func TestChannelModelMappingAndStatusCodeMapping(t *testing.T) {
	ch := &Channel{}
	if got := ch.GetModelMapping(); got != "" {
		t.Errorf("nil ModelMapping -> %q, want empty", got)
	}
	mm := `{"a":"b"}`
	ch.ModelMapping = &mm
	if got := ch.GetModelMapping(); got != mm {
		t.Errorf("GetModelMapping() = %q, want %q", got, mm)
	}

	if got := ch.GetStatusCodeMapping(); got != "" {
		t.Errorf("nil StatusCodeMapping -> %q, want empty", got)
	}
	scm := `{"429":"500"}`
	ch.StatusCodeMapping = &scm
	if got := ch.GetStatusCodeMapping(); got != scm {
		t.Errorf("GetStatusCodeMapping() = %q, want %q", got, scm)
	}
}

func TestChannelValidateSettings(t *testing.T) {
	ch := &Channel{}
	if err := ch.ValidateSettings(); err != nil {
		t.Errorf("nil Setting -> error %v, want nil", err)
	}

	emptyStr := ""
	ch.Setting = &emptyStr
	if err := ch.ValidateSettings(); err != nil {
		t.Errorf("empty Setting -> error %v, want nil", err)
	}

	bad := "{not json"
	ch.Setting = &bad
	if err := ch.ValidateSettings(); err == nil {
		t.Error("malformed Setting JSON -> want error, got nil")
	}
}

func TestChannelSetSettingAndOtherSettings(t *testing.T) {
	ch := &Channel{}
	ch.SetSetting(dto.ChannelSettings{})
	if ch.Setting == nil {
		t.Fatal("SetSetting did not populate Setting field")
	}
	// Round trip through ValidateSettings should succeed since it's valid JSON.
	if err := ch.ValidateSettings(); err != nil {
		t.Errorf("ValidateSettings() after SetSetting error = %v, want nil", err)
	}

	ch.SetOtherSettings(dto.ChannelOtherSettings{})
	if ch.OtherSettings == "" {
		t.Error("SetOtherSettings did not populate OtherSettings field")
	}
}

func TestChannelGetParamOverrideAndHeaderOverride(t *testing.T) {
	ch := &Channel{}
	if got := ch.GetParamOverride(); len(got) != 0 {
		t.Errorf("nil ParamOverride -> %v, want empty map", got)
	}
	po := `{"temperature":0.5}`
	ch.ParamOverride = &po
	got := ch.GetParamOverride()
	if got["temperature"] != 0.5 {
		t.Errorf("GetParamOverride() = %v, want temperature=0.5", got)
	}

	badPo := "{bad"
	ch.ParamOverride = &badPo
	if got := ch.GetParamOverride(); len(got) != 0 {
		t.Errorf("malformed ParamOverride -> %v, want empty map", got)
	}

	if got := ch.GetHeaderOverride(); len(got) != 0 {
		t.Errorf("nil HeaderOverride -> %v, want empty map", got)
	}
	ho := `{"X-Foo":"bar"}`
	ch.HeaderOverride = &ho
	got2 := ch.GetHeaderOverride()
	if got2["X-Foo"] != "bar" {
		t.Errorf("GetHeaderOverride() = %v, want X-Foo=bar", got2)
	}

	badHo := "{bad"
	ch.HeaderOverride = &badHo
	if got := ch.GetHeaderOverride(); len(got) != 0 {
		t.Errorf("malformed HeaderOverride -> %v, want empty map", got)
	}
}

func TestChannelGetNextEnabledKeyIndex(t *testing.T) {
	t.Run("non multi-key returns raw Key", func(t *testing.T) {
		ch := &Channel{Key: "singlekey"}
		key, idx, err := ch.GetNextEnabledKeyIndex(nil)
		if key != "singlekey" || idx != 0 || err != nil {
			t.Errorf("got (%q,%d,%v), want (singlekey,0,nil)", key, idx, err)
		}
	})

	t.Run("multi-key no keys -> error", func(t *testing.T) {
		ch := &Channel{Key: "", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		_, _, err := ch.GetNextEnabledKeyIndex(nil)
		if err == nil || err.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Errorf("got err=%v, want ErrorCodeChannelNoAvailableKey", err)
		}
	})

	t.Run("multi-key nil statusList treats all enabled, returns first", func(t *testing.T) {
		ch := &Channel{Key: "k1\nk2", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		key, idx, err := ch.GetNextEnabledKeyIndex(nil)
		if err != nil || key != "k1" || idx != 0 {
			t.Errorf("got (%q,%d,%v), want (k1,0,nil)", key, idx, err)
		}
	})

	t.Run("multi-key skips disabled to find enabled", func(t *testing.T) {
		ch := &Channel{Key: "k1\nk2", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		statusList := map[int]int{0: 2} // 0 disabled (not Enabled==1), 1 falls back to enabled
		key, idx, err := ch.GetNextEnabledKeyIndex(statusList)
		if err != nil || key != "k2" || idx != 1 {
			t.Errorf("got (%q,%d,%v), want (k2,1,nil)", key, idx, err)
		}
	})

	t.Run("all disabled, no cooldowns -> permanent error", func(t *testing.T) {
		ch := &Channel{Key: "k1\nk2", ChannelInfo: ChannelInfo{IsMultiKey: true}}
		statusList := map[int]int{0: 2, 1: 2}
		_, _, err := ch.GetNextEnabledKeyIndex(statusList)
		if err == nil || err.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Errorf("got err=%v, want ErrorCodeChannelNoAvailableKey", err)
		}
	})

	t.Run("all disabled, all cooling -> all_keys_cooling with earliest deadline", func(t *testing.T) {
		ch := &Channel{
			Key: "k1\nk2",
			ChannelInfo: ChannelInfo{
				IsMultiKey:            true,
				MultiKeyCooldownUntil: map[int]int64{0: 200, 1: 100},
			},
		}
		statusList := map[int]int{0: 2, 1: 2}
		_, _, err := ch.GetNextEnabledKeyIndex(statusList)
		if err == nil || err.GetErrorCode() != types.ErrorCodeChannelAllKeysCooling {
			t.Fatalf("got err=%v, want ErrorCodeChannelAllKeysCooling", err)
		}
		if err.RetryAfterUnix != 100 {
			t.Errorf("RetryAfterUnix = %d, want 100 (earliest)", err.RetryAfterUnix)
		}
	})

	t.Run("one disabled key has no cooldown entry -> permanent error, not cooling", func(t *testing.T) {
		ch := &Channel{
			Key: "k1\nk2",
			ChannelInfo: ChannelInfo{
				IsMultiKey:            true,
				MultiKeyCooldownUntil: map[int]int64{0: 200}, // key 1 has none
			},
		}
		statusList := map[int]int{0: 2, 1: 2}
		_, _, err := ch.GetNextEnabledKeyIndex(statusList)
		if err == nil || err.GetErrorCode() != types.ErrorCodeChannelNoAvailableKey {
			t.Errorf("got err=%v, want ErrorCodeChannelNoAvailableKey (mixed permanent+cooling)", err)
		}
	})
}

// ---------------- Token ----------------

func TestTokenClean(t *testing.T) {
	tok := &Token{Key: "secret"}
	tok.Clean()
	if tok.Key != "" {
		t.Errorf("Clean() left Key = %q, want empty", tok.Key)
	}
}

func TestTokenGetIpLimits(t *testing.T) {
	if got := (&Token{}).GetIpLimits(); len(got) != 0 {
		t.Errorf("nil AllowIps -> %v, want empty", got)
	}
	empty := ""
	if got := (&Token{AllowIps: &empty}).GetIpLimits(); len(got) != 0 {
		t.Errorf("empty AllowIps -> %v, want empty", got)
	}
	raw := " 1.2.3.4 ,\n5.6.7.8, \n \n9.9.9.9"
	got := (&Token{AllowIps: &raw}).GetIpLimits()
	want := []string{"1.2.3.4", "5.6.7.8", "9.9.9.9"}
	if len(got) != len(want) {
		t.Fatalf("GetIpLimits() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GetIpLimits()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTokenModelLimits(t *testing.T) {
	tok := &Token{ModelLimitsEnabled: true}
	if !tok.IsModelLimitsEnabled() {
		t.Error("IsModelLimitsEnabled() = false, want true")
	}
	if got := (&Token{}).GetModelLimits(); len(got) != 0 {
		t.Errorf("empty ModelLimits -> %v, want empty", got)
	}
	tok.ModelLimits = "gpt-4,claude-3"
	got := tok.GetModelLimits()
	if len(got) != 2 || got[0] != "gpt-4" || got[1] != "claude-3" {
		t.Errorf("GetModelLimits() = %v, want [gpt-4 claude-3]", got)
	}
	m := tok.GetModelLimitsMap()
	if !m["gpt-4"] || !m["claude-3"] || len(m) != 2 {
		t.Errorf("GetModelLimitsMap() = %v, want map with gpt-4,claude-3", m)
	}
}

func TestTokenScopes(t *testing.T) {
	if got := (&Token{}).GetScopes(); got != nil {
		t.Errorf("empty Scopes -> %v, want nil", got)
	}
	tok := &Token{Scopes: " a , ,b "}
	got := tok.GetScopes()
	want := []string{"a", "b"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("GetScopes() = %v, want %v", got, want)
	}

	if !(&Token{}).HasScope("anything") {
		t.Error("HasScope() on empty Scopes -> false, want true (no restriction)")
	}
	tok2 := &Token{Scopes: "read,write"}
	if !tok2.HasScope("read") {
		t.Error("HasScope(read) = false, want true")
	}
	if tok2.HasScope("delete") {
		t.Error("HasScope(delete) = true, want false")
	}
}

// ---------------- User ----------------

func TestUserToBaseUser(t *testing.T) {
	u := &User{
		Id: 1, Group: "g", Quota: 10, Status: 1, Username: "u", Setting: "s",
		Email: "e@x.com", DailyQuota: 5, DailyUsed: 2, LastDailyReset: 99,
		BaseGroup: "bg", FallbackGroup: "fg",
	}
	b := u.ToBaseUser()
	if b.Id != 1 || b.Group != "g" || b.Quota != 10 || b.Status != 1 || b.Username != "u" ||
		b.Setting != "s" || b.Email != "e@x.com" || b.DailyQuota != 5 || b.DailyUsed != 2 ||
		b.LastDailyReset != 99 || b.BaseGroup != "bg" || b.FallbackGroup != "fg" {
		t.Errorf("ToBaseUser() = %+v, mismatch", b)
	}
}

func TestUserAccessToken(t *testing.T) {
	u := &User{}
	if got := u.GetAccessToken(); got != "" {
		t.Errorf("nil AccessToken -> %q, want empty", got)
	}
	u.SetAccessToken("tok123")
	if got := u.GetAccessToken(); got != "tok123" {
		t.Errorf("GetAccessToken() = %q, want tok123", got)
	}
}

func TestUserSetting(t *testing.T) {
	u := &User{}
	s := u.GetSetting()
	if s != (dto.UserSetting{}) {
		t.Errorf("empty Setting -> %+v, want zero value", s)
	}
	u.Setting = "{bad json"
	s2 := u.GetSetting()
	if s2 != (dto.UserSetting{}) {
		t.Errorf("malformed Setting -> %+v, want zero value", s2)
	}
}

func TestUserBaseGetSetting(t *testing.T) {
	ub := &UserBase{}
	if s := ub.GetSetting(); s != (dto.UserSetting{}) {
		t.Errorf("empty Setting -> %+v, want zero value", s)
	}
	ub.Setting = "{bad"
	if s := ub.GetSetting(); s != (dto.UserSetting{}) {
		t.Errorf("malformed Setting -> %+v, want zero value", s)
	}
}

func TestUserIsSubscriber(t *testing.T) {
	tests := []struct {
		role int
		want bool
	}{
		{common.RoleSubscriberUser - 1, false},
		{common.RoleSubscriberUser, true},
		{common.RoleSubscriberUser + 1, true},
	}
	for _, tt := range tests {
		u := &User{Role: tt.role}
		if got := u.IsSubscriber(); got != tt.want {
			t.Errorf("IsSubscriber() role=%d = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestNeedsDailyResetAt(t *testing.T) {
	tests := []struct {
		name      string
		lastReset int64
		now       int64
		want      bool
	}{
		{"never reset -> always needs reset", 0, 12345, true},
		{"same day -> no reset needed", 86400 * 10, 86400*10 + 100, false},
		{"next day -> needs reset", 86400 * 10, 86400 * 11, true},
		{"earlier same-day boundary just before midnight -> no reset", 86400*10 + 86399, 86400*10 + 86399, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsDailyResetAt(tt.lastReset, tt.now); got != tt.want {
				t.Errorf("NeedsDailyResetAt(%d,%d) = %v, want %v", tt.lastReset, tt.now, got, tt.want)
			}
		})
	}
}

func TestNeedsDailyReset(t *testing.T) {
	// lastResetTimestamp=0 always triggers reset regardless of current clock.
	if !NeedsDailyReset(0) {
		t.Error("NeedsDailyReset(0) = false, want true")
	}
}

// ---------------- Task ----------------

func TestTaskStatusToVideoStatus(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   string
	}{
		{TaskStatusQueued, dto.VideoStatusQueued},
		{TaskStatusSubmitted, dto.VideoStatusQueued},
		{TaskStatusInProgress, dto.VideoStatusInProgress},
		{TaskStatusSuccess, dto.VideoStatusCompleted},
		{TaskStatusFailure, dto.VideoStatusFailed},
		{TaskStatusNotStart, dto.VideoStatusUnknown},
		{TaskStatus("garbage"), dto.VideoStatusUnknown},
	}
	for _, tt := range tests {
		if got := tt.status.ToVideoStatus(); got != tt.want {
			t.Errorf("ToVideoStatus(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestTaskSetGetData(t *testing.T) {
	tk := &Task{}
	tk.SetData(map[string]string{"a": "b"})
	var out map[string]string
	if err := tk.GetData(&out); err != nil {
		t.Fatalf("GetData() error = %v", err)
	}
	if out["a"] != "b" {
		t.Errorf("GetData() = %v, want map[a:b]", out)
	}
}

func TestTaskToOpenAIVideo(t *testing.T) {
	tk := &Task{
		TaskID:     "task-1",
		Status:     TaskStatusSuccess,
		Properties: Properties{OriginModelName: "model-x"},
		Progress:   "100%",
		CreatedAt:  1000,
		UpdatedAt:  2000,
		FailReason: "oops",
	}
	v := tk.ToOpenAIVideo()
	if v.ID != "task-1" || v.Status != dto.VideoStatusCompleted || v.Model != "model-x" ||
		v.CreatedAt != 1000 || v.CompletedAt != 2000 {
		t.Errorf("ToOpenAIVideo() = %+v, mismatch", v)
	}
}

func TestPropertiesValueScan(t *testing.T) {
	// zero value -> nil driver.Value (skip persisting empty JSON)
	zero := Properties{}
	v, err := zero.Value()
	if err != nil || v != nil {
		t.Errorf("zero Properties.Value() = (%v,%v), want (nil,nil)", v, err)
	}

	p := Properties{Input: "in", OriginModelName: "m"}
	v2, err := p.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	b, ok := v2.([]byte)
	if !ok {
		t.Fatalf("Value() = %T, want []byte", v2)
	}
	var out Properties
	if err := out.Scan(b); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if out != p {
		t.Errorf("Scan() roundtrip = %+v, want %+v", out, p)
	}

	// empty bytes -> zero value, no error
	var out2 Properties
	out2.OriginModelName = "stale"
	if err := out2.Scan([]byte{}); err != nil {
		t.Fatalf("Scan(empty) error = %v", err)
	}
	if out2 != (Properties{}) {
		t.Errorf("Scan(empty) = %+v, want zero value", out2)
	}
}

func TestTaskPrivateDataValueScan(t *testing.T) {
	zero := TaskPrivateData{}
	v, err := zero.Value()
	if err != nil || v != nil {
		t.Errorf("zero TaskPrivateData.Value() = (%v,%v), want (nil,nil)", v, err)
	}

	p := TaskPrivateData{Key: "secret-key"}
	v2, err := p.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	b, ok := v2.([]byte)
	if !ok {
		t.Fatalf("Value() = %T, want []byte", v2)
	}
	var out TaskPrivateData
	if err := out.Scan(b); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if out != p {
		t.Errorf("Scan() roundtrip = %+v, want %+v", out, p)
	}

	var out2 TaskPrivateData
	if err := out2.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) error = %v", err)
	}
	if out2 != (TaskPrivateData{}) {
		t.Errorf("Scan(nil) = %+v, want zero value", out2)
	}
}

// ---------------- PrefillGroup / JSONValue ----------------

func TestJSONValueValueScan(t *testing.T) {
	var empty JSONValue
	v, err := empty.Value()
	if err != nil || v != nil {
		t.Errorf("empty JSONValue.Value() = (%v,%v), want (nil,nil)", v, err)
	}

	j := JSONValue(`{"a":1}`)
	v2, err := j.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if b, ok := v2.([]byte); !ok || string(b) != `{"a":1}` {
		t.Errorf("Value() = %v, want bytes {\"a\":1}", v2)
	}

	var out JSONValue
	if err := out.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) error = %v", err)
	}
	if string(out) != "null" {
		t.Errorf("Scan(nil) = %q, want null", string(out))
	}

	var out2 JSONValue
	if err := out2.Scan([]byte(`{"x":2}`)); err != nil {
		t.Fatalf("Scan([]byte) error = %v", err)
	}
	if string(out2) != `{"x":2}` {
		t.Errorf("Scan([]byte) = %q, want {\"x\":2}", string(out2))
	}

	var out3 JSONValue
	if err := out3.Scan("string-val"); err != nil {
		t.Fatalf("Scan(string) error = %v", err)
	}
	if string(out3) != "string-val" {
		t.Errorf("Scan(string) = %q, want string-val", string(out3))
	}

	// unsupported type: left untouched
	var out4 JSONValue
	if err := out4.Scan(42); err != nil {
		t.Fatalf("Scan(int) error = %v", err)
	}
	if len(out4) != 0 {
		t.Errorf("Scan(int) = %q, want unchanged empty", string(out4))
	}
}

func TestJSONValueMarshalUnmarshalJSON(t *testing.T) {
	var empty JSONValue
	b, err := empty.MarshalJSON()
	if err != nil || string(b) != "null" {
		t.Errorf("empty MarshalJSON() = (%q,%v), want (null,nil)", string(b), err)
	}

	j := JSONValue(`{"a":1}`)
	b2, err := j.MarshalJSON()
	if err != nil || string(b2) != `{"a":1}` {
		t.Errorf("MarshalJSON() = (%q,%v), want ({\"a\":1},nil)", string(b2), err)
	}

	var out JSONValue
	if err := out.UnmarshalJSON([]byte(`{"y":3}`)); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if string(out) != `{"y":3}` {
		t.Errorf("UnmarshalJSON() = %q, want {\"y\":3}", string(out))
	}

	// nil receiver -> no panic, returns nil
	var nilPtr *JSONValue
	if err := nilPtr.UnmarshalJSON([]byte("x")); err != nil {
		t.Errorf("UnmarshalJSON() on nil receiver error = %v, want nil", err)
	}

	// Full round trip through encoding/json to exercise the interface wiring.
	type wrapper struct {
		V JSONValue `json:"v"`
	}
	w := wrapper{V: JSONValue(`{"nested":true}`)}
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("json.Marshal(wrapper) error = %v", err)
	}
	var w2 wrapper
	if err := json.Unmarshal(raw, &w2); err != nil {
		t.Fatalf("json.Unmarshal(wrapper) error = %v", err)
	}
	if string(w2.V) != `{"nested":true}` {
		t.Errorf("round-trip wrapper.V = %q, want {\"nested\":true}", string(w2.V))
	}
}

// ---------------- OpenRouterSyncJob ----------------

func TestOpenRouterSyncJobTableName(t *testing.T) {
	if got := (OpenRouterSyncJob{}).TableName(); got != "openrouter_sync_jobs" {
		t.Errorf("TableName() = %q, want openrouter_sync_jobs", got)
	}
}

func TestOpenRouterSyncJobCategories(t *testing.T) {
	j := &OpenRouterSyncJob{}
	if got := j.GetCategories(); len(got) != 0 {
		t.Errorf("empty Categories -> %v, want empty", got)
	}

	if err := j.SetCategories([]string{OpenRouterCategoryVision, OpenRouterCategoryASR}); err != nil {
		t.Fatalf("SetCategories() error = %v", err)
	}
	got := j.GetCategories()
	if len(got) != 2 || got[0] != "vision" || got[1] != "asr" {
		t.Errorf("GetCategories() = %v, want [vision asr]", got)
	}

	// malformed JSON -> empty, no error surfaced
	j2 := &OpenRouterSyncJob{Categories: "not json"}
	if got := j2.GetCategories(); len(got) != 0 {
		t.Errorf("malformed Categories -> %v, want empty", got)
	}
}

func TestOpenRouterSyncJobShouldRun(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	dayAgo := now.Add(-25 * time.Hour)
	recentDay := now.Add(-1 * time.Hour)
	weekAgo := now.Add(-8 * 24 * time.Hour)
	recentWeek := now.Add(-1 * 24 * time.Hour)

	tests := []struct {
		name string
		job  OpenRouterSyncJob
		want bool
	}{
		{"disabled never runs", OpenRouterSyncJob{Enabled: false, Schedule: OpenRouterScheduleDaily}, false},
		{"daily, never run -> runs", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleDaily}, true},
		{"daily, ran >24h ago -> runs", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleDaily, LastRunAt: &dayAgo}, true},
		{"daily, ran <24h ago -> waits", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleDaily, LastRunAt: &recentDay}, false},
		{"weekly, never run -> runs", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleWeekly}, true},
		{"weekly, ran >7d ago -> runs", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleWeekly, LastRunAt: &weekAgo}, true},
		{"weekly, ran <7d ago -> waits", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleWeekly, LastRunAt: &recentWeek}, false},
		{"manual never auto-runs", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleManual}, false},
		{"unknown schedule never auto-runs", OpenRouterSyncJob{Enabled: true, Schedule: "bogus"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.job.ShouldRun(now); got != tt.want {
				t.Errorf("ShouldRun() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------- InternalApiKey ----------------

func TestInternalApiKeyTableName(t *testing.T) {
	if got := (InternalApiKey{}).TableName(); got != "internal_api_keys" {
		t.Errorf("TableName() = %q, want internal_api_keys", got)
	}
}

func TestInternalApiKeyGetScopes(t *testing.T) {
	if got := (&InternalApiKey{}).GetScopes(); len(got) != 0 {
		t.Errorf("empty Scopes -> %v, want empty", got)
	}
	k := &InternalApiKey{Scopes: `["user:read","admin"]`}
	got := k.GetScopes()
	if len(got) != 2 || got[0] != "user:read" || got[1] != "admin" {
		t.Errorf("GetScopes() = %v, want [user:read admin]", got)
	}

	k2 := &InternalApiKey{Scopes: "not json"}
	if got := k2.GetScopes(); len(got) != 0 {
		t.Errorf("malformed Scopes -> %v, want empty", got)
	}
}

func TestInternalApiKeyHasScope(t *testing.T) {
	k := &InternalApiKey{Scopes: `["user:read"]`}
	if !k.HasScope("user:read") {
		t.Error("HasScope(user:read) = false, want true")
	}
	if k.HasScope("user:write") {
		t.Error("HasScope(user:write) = true, want false")
	}

	all := &InternalApiKey{Scopes: `["*"]`}
	if !all.HasScope("anything:goes") {
		t.Error("HasScope() with wildcard scope = false, want true")
	}
}

// ---------------- Tenant ----------------

func TestTenantTableName(t *testing.T) {
	if got := (Tenant{}).TableName(); got != "tenants" {
		t.Errorf("TableName() = %q, want tenants", got)
	}
}

func TestTenantIsEnabledIsDisabled(t *testing.T) {
	tests := []struct {
		status       int
		wantEnabled  bool
		wantDisabled bool
	}{
		{TenantStatusEnabled, true, false},
		{TenantStatusDisabled, false, true},
		{TenantStatusSuspended, false, true},
		{999, false, false},
	}
	for _, tt := range tests {
		tn := &Tenant{Status: tt.status}
		if got := tn.IsEnabled(); got != tt.wantEnabled {
			t.Errorf("status=%d IsEnabled() = %v, want %v", tt.status, got, tt.wantEnabled)
		}
		if got := tn.IsDisabled(); got != tt.wantDisabled {
			t.Errorf("status=%d IsDisabled() = %v, want %v", tt.status, got, tt.wantDisabled)
		}
	}
}

// ---------------- Trivial TableName() getters ----------------

func TestTableNameGetters(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"BillingOutbox", (BillingOutbox{}).TableName(), "billing_outbox"},
		{"CurrencyExchange", (CurrencyExchange{}).TableName(), "currency_exchanges"},
		{"LeaderElection", (LeaderElection{}).TableName(), "leader_elections"},
		{"ModelUsageStat", (ModelUsageStat{}).TableName(), "model_usage_stats"},
		{"PrivacyErasureRequest", (PrivacyErasureRequest{}).TableName(), "privacy_erasure_requests"},
		{"Release", (Release{}).TableName(), "releases"},
		{"ReleaseArtifact", (ReleaseArtifact{}).TableName(), "release_artifacts"},
		{"DownloadLog", (DownloadLog{}).TableName(), "download_logs"},
		{"UserIdentityMapping", (UserIdentityMapping{}).TableName(), "user_identity_mapping"},
		{"TenantConfig", (TenantConfig{}).TableName(), "tenant_configs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s.TableName() = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

// Sanity check that constant.EndpointType is still usable in this package
// (Pricing embeds it) — guards against silent type drift.
func TestPricingStructFieldsAssignable(t *testing.T) {
	p := Pricing{
		ModelName:              "gpt-4",
		ModelRatio:             1.5,
		SupportedEndpointTypes: []constant.EndpointType{},
	}
	if p.ModelName != "gpt-4" || p.ModelRatio != 1.5 {
		t.Errorf("Pricing struct fields not assignable as expected: %+v", p)
	}
}
