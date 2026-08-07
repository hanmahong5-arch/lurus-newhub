package console_setting

import (
	"strings"
	"testing"
)

func boot_settings_resetConsoleSetting(t *testing.T) {
	t.Helper()
	orig := consoleSetting
	t.Cleanup(func() { consoleSetting = orig })
}

func TestGetConsoleSetting_Defaults(t *testing.T) {
	boot_settings_resetConsoleSetting(t)
	consoleSetting = defaultConsoleSetting

	s := GetConsoleSetting()
	// These defaults gate whether admin console panels render at all; a
	// regression here silently hides UI panels for every fresh deployment.
	if !s.ApiInfoEnabled || !s.UptimeKumaEnabled || !s.AnnouncementsEnabled || !s.FAQEnabled {
		t.Fatalf("expected all console panels enabled by default, got %+v", s)
	}
	if s.ApiInfo != "" || s.Announcements != "" || s.FAQ != "" || s.UptimeKumaGroups != "" {
		t.Fatalf("expected empty content defaults, got %+v", s)
	}
}

func TestValidateConsoleSettings_EmptyStringAlwaysValid(t *testing.T) {
	if err := ValidateConsoleSettings("", "ApiInfo"); err != nil {
		t.Fatalf("expected empty settings string to short-circuit as valid, got %v", err)
	}
}

func TestValidateConsoleSettings_UnknownType(t *testing.T) {
	err := ValidateConsoleSettings(`[{}]`, "NotARealType")
	if err == nil || !strings.Contains(err.Error(), "未知的设置类型") {
		t.Fatalf("expected unknown-type error, got %v", err)
	}
}

func TestValidateConsoleSettings_ApiInfo(t *testing.T) {
	valid := `[{"url":"https://api.example.com","route":"主线路","description":"desc","color":"blue"}]`
	if err := ValidateConsoleSettings(valid, "ApiInfo"); err != nil {
		t.Fatalf("expected valid ApiInfo to pass, got %v", err)
	}

	cases := []struct {
		name string
		json string
	}{
		{"malformed json", `not-json`},
		{"missing url", `[{"route":"r","description":"d","color":"blue"}]`},
		{"missing route", `[{"url":"https://a.com","description":"d","color":"blue"}]`},
		{"missing description", `[{"url":"https://a.com","route":"r","color":"blue"}]`},
		{"missing color", `[{"url":"https://a.com","route":"r","description":"d"}]`},
		{"bad url format", `[{"url":"not-a-url","route":"r","description":"d","color":"blue"}]`},
		{"invalid color", `[{"url":"https://a.com","route":"r","description":"d","color":"mauve"}]`},
		{"dangerous content in description", `[{"url":"https://a.com","route":"r","description":"<script>alert(1)</script>","color":"blue"}]`},
		{"dangerous content in route", `[{"url":"https://a.com","route":"javascript:alert(1)","description":"d","color":"blue"}]`},
		{"url too long", `[{"url":"https://a.com/` + strings.Repeat("x", 500) + `","route":"r","description":"d","color":"blue"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateConsoleSettings(tc.json, "ApiInfo"); err == nil {
				t.Fatalf("expected validation error for case %q", tc.name)
			}
		})
	}

	// Boundary: more than 50 entries must be rejected.
	items := make([]string, 51)
	for i := range items {
		items[i] = `{"url":"https://a.com","route":"r","description":"d","color":"blue"}`
	}
	tooMany := "[" + strings.Join(items, ",") + "]"
	if err := ValidateConsoleSettings(tooMany, "ApiInfo"); err == nil {
		t.Fatalf("expected rejection of 51 ApiInfo entries (limit is 50)")
	}
}

func TestValidateConsoleSettings_Announcements(t *testing.T) {
	valid := `[{"content":"hello","publishDate":"2026-01-01T00:00:00Z","type":"success"}]`
	if err := ValidateConsoleSettings(valid, "Announcements"); err != nil {
		t.Fatalf("expected valid announcement to pass, got %v", err)
	}

	cases := []struct {
		name string
		json string
	}{
		{"missing content", `[{"publishDate":"2026-01-01T00:00:00Z"}]`},
		{"missing publishDate field", `[{"content":"c"}]`},
		{"empty publishDate", `[{"content":"c","publishDate":""}]`},
		{"non-RFC3339 date", `[{"content":"c","publishDate":"2026-01-01"}]`},
		{"invalid type", `[{"content":"c","publishDate":"2026-01-01T00:00:00Z","type":"bogus"}]`},
		{"content too long", `[{"content":"` + strings.Repeat("c", 501) + `","publishDate":"2026-01-01T00:00:00Z"}]`},
		{"extra too long", `[{"content":"c","publishDate":"2026-01-01T00:00:00Z","extra":"` + strings.Repeat("e", 201) + `"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateConsoleSettings(tc.json, "Announcements"); err == nil {
				t.Fatalf("expected validation error for case %q", tc.name)
			}
		})
	}

	// type field is optional — omitting it entirely must still validate.
	noType := `[{"content":"hi","publishDate":"2026-01-01T00:00:00Z"}]`
	if err := ValidateConsoleSettings(noType, "Announcements"); err != nil {
		t.Fatalf("expected announcement without type field to pass, got %v", err)
	}
}

func TestValidateConsoleSettings_FAQ(t *testing.T) {
	valid := `[{"question":"q?","answer":"a."}]`
	if err := ValidateConsoleSettings(valid, "FAQ"); err != nil {
		t.Fatalf("expected valid FAQ to pass, got %v", err)
	}
	cases := []struct {
		name string
		json string
	}{
		{"missing question", `[{"answer":"a"}]`},
		{"missing answer", `[{"question":"q"}]`},
		{"question too long", `[{"question":"` + strings.Repeat("q", 201) + `","answer":"a"}]`},
		{"answer too long", `[{"question":"q","answer":"` + strings.Repeat("a", 1001) + `"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateConsoleSettings(tc.json, "FAQ"); err == nil {
				t.Fatalf("expected validation error for case %q", tc.name)
			}
		})
	}
}

func TestValidateConsoleSettings_UptimeKumaGroups(t *testing.T) {
	valid := `[{"categoryName":"Core","url":"https://status.example.com","slug":"core-status","description":"d"}]`
	if err := ValidateConsoleSettings(valid, "UptimeKumaGroups"); err != nil {
		t.Fatalf("expected valid group to pass, got %v", err)
	}

	cases := []struct {
		name string
		json string
	}{
		{"missing categoryName", `[{"url":"https://a.com","slug":"s"}]`},
		{"missing url", `[{"categoryName":"c","slug":"s"}]`},
		{"missing slug", `[{"categoryName":"c","url":"https://a.com"}]`},
		{"invalid slug chars", `[{"categoryName":"c","url":"https://a.com","slug":"bad slug!"}]`},
		{"duplicate categoryName", `[{"categoryName":"dup","url":"https://a.com","slug":"s1"},{"categoryName":"dup","url":"https://b.com","slug":"s2"}]`},
		{"dangerous categoryName", `[{"categoryName":"<script>x</script>","url":"https://a.com","slug":"s"}]`},
		{"bad url", `[{"categoryName":"c","url":"ftp://not-http","slug":"s"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateConsoleSettings(tc.json, "UptimeKumaGroups"); err == nil {
				t.Fatalf("expected validation error for case %q", tc.name)
			}
		})
	}

	// description is optional — a group lacking it should still validate.
	noDesc := `[{"categoryName":"NoDesc","url":"https://a.com","slug":"nodesc"}]`
	if err := ValidateConsoleSettings(noDesc, "UptimeKumaGroups"); err != nil {
		t.Fatalf("expected group without description to pass, got %v", err)
	}

	// Boundary: more than 20 groups must be rejected.
	items := make([]string, 21)
	for i := range items {
		items[i] = `{"categoryName":"c` + string(rune('a'+i%26)) + `","url":"https://a.com","slug":"s` + string(rune('a'+i%26)) + `"}`
	}
	tooMany := "[" + strings.Join(items, ",") + "]"
	if err := ValidateConsoleSettings(tooMany, "UptimeKumaGroups"); err == nil {
		t.Fatalf("expected rejection of 21 UptimeKumaGroups entries (limit is 20)")
	}
}

func TestGetApiInfo_ParsesConfiguredJSON(t *testing.T) {
	boot_settings_resetConsoleSetting(t)
	consoleSetting.ApiInfo = `[{"url":"https://a.com","route":"r","description":"d","color":"blue"}]`

	list := GetApiInfo()
	if len(list) != 1 || list[0]["route"] != "r" {
		t.Fatalf("expected one api info entry with route=r, got %v", list)
	}
}

func TestGetApiInfo_EmptyStringReturnsEmptySlice(t *testing.T) {
	boot_settings_resetConsoleSetting(t)
	consoleSetting.ApiInfo = ""

	list := GetApiInfo()
	if len(list) != 0 {
		t.Fatalf("expected empty slice for unset ApiInfo, got %v", list)
	}
}

func TestGetAnnouncements_SortedNewestFirst(t *testing.T) {
	boot_settings_resetConsoleSetting(t)
	consoleSetting.Announcements = `[
		{"content":"older","publishDate":"2026-01-01T00:00:00Z"},
		{"content":"newest","publishDate":"2026-06-01T00:00:00Z"},
		{"content":"middle","publishDate":"2026-03-01T00:00:00Z"}
	]`

	list := GetAnnouncements()
	if len(list) != 3 {
		t.Fatalf("expected 3 announcements, got %d", len(list))
	}
	// Business requirement: announcements display newest-first on the console.
	order := []string{list[0]["content"].(string), list[1]["content"].(string), list[2]["content"].(string)}
	want := []string{"newest", "middle", "older"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected announcement order %v, got %v", want, order)
		}
	}
}

func TestGetFAQ_ParsesConfiguredJSON(t *testing.T) {
	boot_settings_resetConsoleSetting(t)
	consoleSetting.FAQ = `[{"question":"q?","answer":"a."}]`

	list := GetFAQ()
	if len(list) != 1 || list[0]["question"] != "q?" {
		t.Fatalf("expected one FAQ entry, got %v", list)
	}
}

func TestGetUptimeKumaGroups_ParsesConfiguredJSON(t *testing.T) {
	boot_settings_resetConsoleSetting(t)
	consoleSetting.UptimeKumaGroups = `[{"categoryName":"Core","url":"https://a.com","slug":"core"}]`

	list := GetUptimeKumaGroups()
	if len(list) != 1 || list[0]["categoryName"] != "Core" {
		t.Fatalf("expected one uptime-kuma group, got %v", list)
	}
}
