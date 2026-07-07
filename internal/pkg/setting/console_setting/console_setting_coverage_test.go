package console_setting

import (
	"encoding/json"
	"testing"
)

// snapshotConsoleSetting saves and restores the global consoleSetting so tests
// remain -count=1 safe and don't leak state across tests.
func snapshotConsoleSetting(t *testing.T) {
	t.Helper()
	orig := consoleSetting
	t.Cleanup(func() {
		consoleSetting = orig
	})
}

func TestGetConsoleSetting(t *testing.T) {
	snapshotConsoleSetting(t)
	got := GetConsoleSetting()
	if got == nil {
		t.Fatal("expected non-nil ConsoleSetting")
	}
	if got != &consoleSetting {
		t.Fatal("expected pointer to package-level consoleSetting")
	}
	// default values
	if !got.ApiInfoEnabled || !got.UptimeKumaEnabled || !got.AnnouncementsEnabled || !got.FAQEnabled {
		t.Fatalf("expected all enabled flags true by default, got %+v", got)
	}
	if got.ApiInfo != "" || got.UptimeKumaGroups != "" || got.Announcements != "" || got.FAQ != "" {
		t.Fatalf("expected all string fields empty by default, got %+v", got)
	}
}

func TestConsoleSettingJSONRoundTrip(t *testing.T) {
	cs := ConsoleSetting{
		ApiInfo:              `[{"url":"https://a.com"}]`,
		UptimeKumaGroups:     `[]`,
		Announcements:        `[]`,
		FAQ:                  `[]`,
		ApiInfoEnabled:       true,
		UptimeKumaEnabled:    false,
		AnnouncementsEnabled: true,
		FAQEnabled:           false,
	}
	b, err := json.Marshal(cs)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	expected := `{"api_info":"[{\"url\":\"https://a.com\"}]","uptime_kuma_groups":"[]","announcements":"[]","faq":"[]","api_info_enabled":true,"uptime_kuma_enabled":false,"announcements_enabled":true,"faq_enabled":false}`
	if string(b) != expected {
		t.Fatalf("marshal mismatch:\ngot:  %s\nwant: %s", string(b), expected)
	}
	var back ConsoleSetting
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if back != cs {
		t.Fatalf("round-trip mismatch: got %+v want %+v", back, cs)
	}
}

func TestValidateConsoleSettings_EmptyAndUnknownType(t *testing.T) {
	if err := ValidateConsoleSettings("", "ApiInfo"); err != nil {
		t.Fatalf("expected nil error for empty string, got %v", err)
	}
	err := ValidateConsoleSettings("[]", "Bogus")
	if err == nil {
		t.Fatal("expected error for unknown setting type")
	}
	want := "未知的设置类型：Bogus"
	if err.Error() != want {
		t.Fatalf("got %q want %q", err.Error(), want)
	}
}

func TestValidateApiInfo(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr string // "" means nil
	}{
		{
			name:    "invalid json",
			json:    `not-json`,
			wantErr: "API信息格式错误：",
		},
		{
			name: "too many entries",
			json: func() string {
				b, _ := json.Marshal(makeApiInfoList(51))
				return string(b)
			}(),
			wantErr: "API信息数量不能超过50个",
		},
		{
			name:    "missing url",
			json:    `[{"route":"r","description":"d","color":"blue"}]`,
			wantErr: "第1个API信息缺少URL字段",
		},
		{
			name:    "empty url",
			json:    `[{"url":"","route":"r","description":"d","color":"blue"}]`,
			wantErr: "第1个API信息缺少URL字段",
		},
		{
			name:    "missing route",
			json:    `[{"url":"https://a.com","description":"d","color":"blue"}]`,
			wantErr: "第1个API信息缺少线路描述字段",
		},
		{
			name:    "missing description",
			json:    `[{"url":"https://a.com","route":"r","color":"blue"}]`,
			wantErr: "第1个API信息缺少说明字段",
		},
		{
			name:    "missing color",
			json:    `[{"url":"https://a.com","route":"r","description":"d"}]`,
			wantErr: "第1个API信息缺少颜色字段",
		},
		{
			name:    "bad url format",
			json:    `[{"url":"not-a-url","route":"r","description":"d","color":"blue"}]`,
			wantErr: "第1个API信息的URL格式不正确",
		},
		{
			name:    "url too long",
			json:    urlLenPayload(),
			wantErr: "第1个API信息的URL长度不能超过500字符",
		},
		{
			name:    "route too long",
			json:    `[{"url":"https://a.com","route":"` + repeatStr("r", 101) + `","description":"d","color":"blue"}]`,
			wantErr: "第1个API信息的线路描述长度不能超过100字符",
		},
		{
			name:    "description too long",
			json:    `[{"url":"https://a.com","route":"r","description":"` + repeatStr("d", 201) + `","color":"blue"}]`,
			wantErr: "第1个API信息的说明长度不能超过200字符",
		},
		{
			name:    "invalid color",
			json:    `[{"url":"https://a.com","route":"r","description":"d","color":"notacolor"}]`,
			wantErr: "第1个API信息的颜色值不合法",
		},
		{
			name:    "dangerous description",
			json:    `[{"url":"https://a.com","route":"r","description":"<script>alert(1)</script>","color":"blue"}]`,
			wantErr: "第1个API信息包含不允许的内容",
		},
		{
			name:    "dangerous route",
			json:    `[{"url":"https://a.com","route":"javascript:alert(1)","description":"d","color":"blue"}]`,
			wantErr: "第1个API信息包含不允许的内容",
		},
		{
			name:    "valid",
			json:    `[{"url":"https://a.com","route":"r","description":"d","color":"blue"}]`,
			wantErr: "",
		},
		{
			name:    "empty list valid",
			json:    `[]`,
			wantErr: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConsoleSettings(tc.json, "ApiInfo")
			assertErr(t, err, tc.wantErr)
		})
	}
}

func TestValidateAnnouncements(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"invalid json", `not-json`, "系统公告格式错误："},
		{
			"too many",
			func() string {
				b, _ := json.Marshal(makeAnnouncementList(101))
				return string(b)
			}(),
			"系统公告数量不能超过100个",
		},
		{"missing content", `[{"publishDate":"2026-01-01T00:00:00Z"}]`, "第1个公告缺少内容字段"},
		{"empty content", `[{"content":"","publishDate":"2026-01-01T00:00:00Z"}]`, "第1个公告缺少内容字段"},
		{"missing publishDate", `[{"content":"c"}]`, "第1个公告缺少发布日期字段"},
		{"empty publishDate", `[{"content":"c","publishDate":""}]`, "第1个公告的发布日期不能为空"},
		{"non-string publishDate", `[{"content":"c","publishDate":123}]`, "第1个公告的发布日期不能为空"},
		{"bad publishDate format", `[{"content":"c","publishDate":"2026-01-01"}]`, "第1个公告的发布日期格式错误"},
		{"invalid type", `[{"content":"c","publishDate":"2026-01-01T00:00:00Z","type":"bogus"}]`, "第1个公告的类型值不合法"},
		{"non-string type ignored", `[{"content":"c","publishDate":"2026-01-01T00:00:00Z","type":123}]`, ""},
		{"valid type", `[{"content":"c","publishDate":"2026-01-01T00:00:00Z","type":"warning"}]`, ""},
		{
			"content too long",
			`[{"content":"` + repeatStr("c", 501) + `","publishDate":"2026-01-01T00:00:00Z"}]`,
			"第1个公告的内容长度不能超过500字符",
		},
		{
			"extra too long",
			`[{"content":"c","publishDate":"2026-01-01T00:00:00Z","extra":"` + repeatStr("e", 201) + `"}]`,
			"第1个公告的说明长度不能超过200字符",
		},
		{
			"extra non-string ignored",
			`[{"content":"c","publishDate":"2026-01-01T00:00:00Z","extra":123}]`,
			"",
		},
		{"valid minimal", `[{"content":"c","publishDate":"2026-01-01T00:00:00Z"}]`, ""},
		{"empty list", `[]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConsoleSettings(tc.json, "Announcements")
			assertErr(t, err, tc.wantErr)
		})
	}
}

func TestValidateFAQ(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"invalid json", `not-json`, "FAQ信息格式错误："},
		{
			"too many",
			func() string {
				b, _ := json.Marshal(makeFAQList(101))
				return string(b)
			}(),
			"FAQ数量不能超过100个",
		},
		{"missing question", `[{"answer":"a"}]`, "第1个FAQ缺少问题字段"},
		{"empty question", `[{"question":"","answer":"a"}]`, "第1个FAQ缺少问题字段"},
		{"missing answer", `[{"question":"q"}]`, "第1个FAQ缺少答案字段"},
		{"empty answer", `[{"question":"q","answer":""}]`, "第1个FAQ缺少答案字段"},
		{
			"question too long",
			`[{"question":"` + repeatStr("q", 201) + `","answer":"a"}]`,
			"第1个FAQ的问题长度不能超过200字符",
		},
		{
			"answer too long",
			`[{"question":"q","answer":"` + repeatStr("a", 1001) + `"}]`,
			"第1个FAQ的答案长度不能超过1000字符",
		},
		{"valid", `[{"question":"q","answer":"a"}]`, ""},
		{"empty list", `[]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConsoleSettings(tc.json, "FAQ")
			assertErr(t, err, tc.wantErr)
		})
	}
}

func TestValidateUptimeKumaGroups(t *testing.T) {
	cases := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"invalid json", `not-json`, "Uptime Kuma分组配置格式错误："},
		{
			"too many",
			func() string {
				b, _ := json.Marshal(makeGroupList(21))
				return string(b)
			}(),
			"Uptime Kuma分组数量不能超过20个",
		},
		{"missing categoryName", `[{"url":"https://a.com","slug":"s"}]`, "第1个分组缺少分类名称字段"},
		{"empty categoryName", `[{"categoryName":"","url":"https://a.com","slug":"s"}]`, "第1个分组缺少分类名称字段"},
		{
			"duplicate categoryName",
			`[{"categoryName":"cat","url":"https://a.com","slug":"s1"},{"categoryName":"cat","url":"https://b.com","slug":"s2"}]`,
			"第2个分组的分类名称与其他分组重复",
		},
		{"missing url", `[{"categoryName":"cat","slug":"s"}]`, "第1个分组缺少URL字段"},
		{"empty url", `[{"categoryName":"cat","url":"","slug":"s"}]`, "第1个分组缺少URL字段"},
		{"missing slug", `[{"categoryName":"cat","url":"https://a.com"}]`, "第1个分组缺少Slug字段"},
		{"empty slug", `[{"categoryName":"cat","url":"https://a.com","slug":""}]`, "第1个分组缺少Slug字段"},
		{"missing description ok", `[{"categoryName":"cat","url":"https://a.com","slug":"s"}]`, ""},
		{"bad url format", `[{"categoryName":"cat","url":"not-a-url","slug":"s"}]`, "第1个分组的URL格式不正确"},
		{
			"categoryName too long",
			`[{"categoryName":"` + repeatStr("c", 51) + `","url":"https://a.com","slug":"s"}]`,
			"第1个分组的分类名称长度不能超过50字符",
		},
		{
			"url too long",
			`[{"categoryName":"cat","url":"https://a.com/` + repeatStr("x", 500) + `","slug":"s"}]`,
			"第1个分组的URL长度不能超过500字符",
		},
		{
			"slug too long",
			`[{"categoryName":"cat","url":"https://a.com","slug":"` + repeatStr("s", 101) + `"}]`,
			"第1个分组的Slug长度不能超过100字符",
		},
		{
			"description too long",
			`[{"categoryName":"cat","url":"https://a.com","slug":"s","description":"` + repeatStr("d", 201) + `"}]`,
			"第1个分组的描述长度不能超过200字符",
		},
		{
			"invalid slug chars",
			`[{"categoryName":"cat","url":"https://a.com","slug":"bad slug!"}]`,
			"第1个分组的Slug只能包含字母、数字、下划线和连字符",
		},
		{
			"dangerous description",
			`[{"categoryName":"cat","url":"https://a.com","slug":"s","description":"<iframe src=x>"}]`,
			"第1个分组包含不允许的内容",
		},
		{
			"dangerous categoryName",
			`[{"categoryName":"onerror=alert(1)","url":"https://a.com","slug":"s"}]`,
			"第1个分组包含不允许的内容",
		},
		{"valid", `[{"categoryName":"cat","url":"https://a.com","slug":"s-1_2","description":"d"}]`, ""},
		{"empty list", `[]`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConsoleSettings(tc.json, "UptimeKumaGroups")
			assertErr(t, err, tc.wantErr)
		})
	}
}

func TestGetApiInfo(t *testing.T) {
	snapshotConsoleSetting(t)

	consoleSetting.ApiInfo = ""
	if got := GetApiInfo(); len(got) != 0 {
		t.Fatalf("expected empty list for empty ApiInfo, got %v", got)
	}

	consoleSetting.ApiInfo = `[{"url":"https://a.com","route":"r","description":"d","color":"blue"}]`
	got := GetApiInfo()
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0]["url"] != "https://a.com" {
		t.Fatalf("expected url https://a.com, got %v", got[0]["url"])
	}
}

func TestGetFAQ(t *testing.T) {
	snapshotConsoleSetting(t)

	consoleSetting.FAQ = ""
	if got := GetFAQ(); len(got) != 0 {
		t.Fatalf("expected empty list for empty FAQ, got %v", got)
	}

	consoleSetting.FAQ = `[{"question":"q","answer":"a"}]`
	got := GetFAQ()
	if len(got) != 1 || got[0]["question"] != "q" {
		t.Fatalf("unexpected FAQ result: %v", got)
	}
}

func TestGetUptimeKumaGroups(t *testing.T) {
	snapshotConsoleSetting(t)

	consoleSetting.UptimeKumaGroups = ""
	if got := GetUptimeKumaGroups(); len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}

	consoleSetting.UptimeKumaGroups = `[{"categoryName":"cat","url":"https://a.com","slug":"s"}]`
	got := GetUptimeKumaGroups()
	if len(got) != 1 || got[0]["categoryName"] != "cat" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestGetAnnouncements_SortedNewestFirst(t *testing.T) {
	snapshotConsoleSetting(t)

	consoleSetting.Announcements = ""
	if got := GetAnnouncements(); len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}

	consoleSetting.Announcements = `[
		{"content":"old","publishDate":"2026-01-01T00:00:00Z"},
		{"content":"new","publishDate":"2026-06-01T00:00:00Z"},
		{"content":"invalid-date","publishDate":"not-a-date"},
		{"content":"missing-date"}
	]`
	got := GetAnnouncements()
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(got))
	}
	// "new" (2026-06) sorts before "old" (2026-01); entries with unparsable
	// or missing publishDate fall back to zero time.Time and sort last.
	if got[0]["content"] != "new" {
		t.Fatalf("expected first entry to be 'new', got %v", got[0]["content"])
	}
	if got[1]["content"] != "old" {
		t.Fatalf("expected second entry to be 'old', got %v", got[1]["content"])
	}
}

// --- helpers ---

func assertErr(t *testing.T, err error, wantPrefix string) {
	t.Helper()
	if wantPrefix == "" {
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected error with prefix %q, got nil", wantPrefix)
	}
	if len(err.Error()) < len(wantPrefix) || err.Error()[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("expected error to start with %q, got %q", wantPrefix, err.Error())
	}
}

func repeatStr(s string, n int) string {
	b := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		b = append(b, s...)
	}
	return string(b)
}

func urlLenPayload() string {
	longURL := "https://a.com/" + repeatStr("x", 500)
	return `[{"url":"` + longURL + `","route":"r","description":"d","color":"blue"}]`
}

func makeApiInfoList(n int) []map[string]interface{} {
	list := make([]map[string]interface{}, n)
	for i := range list {
		list[i] = map[string]interface{}{
			"url": "https://a.com", "route": "r", "description": "d", "color": "blue",
		}
	}
	return list
}

func makeAnnouncementList(n int) []map[string]interface{} {
	list := make([]map[string]interface{}, n)
	for i := range list {
		list[i] = map[string]interface{}{
			"content": "c", "publishDate": "2026-01-01T00:00:00Z",
		}
	}
	return list
}

func makeFAQList(n int) []map[string]interface{} {
	list := make([]map[string]interface{}, n)
	for i := range list {
		list[i] = map[string]interface{}{
			"question": "q", "answer": "a",
		}
	}
	return list
}

func makeGroupList(n int) []map[string]interface{} {
	list := make([]map[string]interface{}, n)
	for i := range list {
		list[i] = map[string]interface{}{
			"categoryName": "cat", "url": "https://a.com", "slug": "s",
		}
	}
	return list
}
