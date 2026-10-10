package routingdecision

import (
	"strings"
	"testing"
)

// The eligibility table: one row per documented exclusion plus the shapes that
// must stay eligible. A row that flips is a routing decision taken under an
// in-flight tool loop, a stored conversation or an audited run.
func TestCheck_EligibilityTable(t *testing.T) {
	const chat = "/v1/chat/completions"
	const resp = "/v1/responses"
	cases := []struct {
		name  string
		path  string
		body  string
		flags Flags
		ok    bool
		why   string
		text  string
	}{
		{"chat first turn string", chat, `{"model":"m","messages":[{"role":"user","content":"hello there"}]}`, Flags{}, true, "", "hello there"},
		{"chat system then user", chat, `{"model":"m","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"q1"}]}`, Flags{}, true, "", "q1"},
		{"chat text parts", chat, `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`, Flags{}, true, "", "a\nb"},
		{"chat empty tools array is no tools", chat, `{"model":"m","tools":[],"messages":[{"role":"user","content":"x"}]}`, Flags{}, true, "", "x"},
		{"responses string input", resp, `{"model":"m","input":"plan a trip"}`, Flags{}, true, "", "plan a trip"},
		{"responses message items", resp, `{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, Flags{}, true, "", "hi"},

		{"assistant history", chat, `{"model":"m","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},{"role":"user","content":"c"}]}`, Flags{}, false, WhyMultiTurn, ""},
		{"tool message", chat, `{"model":"m","messages":[{"role":"user","content":"a"},{"role":"tool","content":"b"}]}`, Flags{}, false, WhyMultiTurn, ""},
		{"tool_calls on a message", chat, `{"model":"m","messages":[{"role":"user","content":"a","tool_calls":[{"id":"1"}]}]}`, Flags{}, false, WhyMultiTurn, ""},
		{"tools present", chat, `{"model":"m","tools":[{"type":"function"}],"messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyTools, ""},
		{"tool_choice present", chat, `{"model":"m","tool_choice":"auto","messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyTools, ""},
		{"legacy functions", chat, `{"model":"m","functions":[{"name":"f"}],"messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyTools, ""},
		{"previous_response_id", resp, `{"model":"m","previous_response_id":"resp_1","input":"a"}`, Flags{}, false, WhyPrevResponse, ""},
		{"session affinity flag", chat, `{"model":"m","messages":[{"role":"user","content":"a"}]}`, Flags{SessionAffinity: true}, false, WhyAffinity, ""},
		{"reasoning_effort", chat, `{"model":"m","reasoning_effort":"high","messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyReasoning, ""},
		{"responses reasoning object", resp, `{"model":"m","reasoning":{"effort":"low"},"input":"a"}`, Flags{}, false, WhyReasoning, ""},
		{"reasoning model name o3", chat, `{"model":"o7-mini","messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyReasoning, ""},
		{"reasoning model name r-series", chat, `{"model":"model-r2","messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyReasoning, ""},
		{"reasoning model name thinking", chat, `{"model":"model-a-thinking","messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyReasoning, ""},
		{"metadata pin key", chat, `{"model":"m","metadata":{"pin:route":"x"},"messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyMetadataPin, ""},
		{"metadata pin value", chat, `{"model":"m","metadata":{"route":"pin:model-a"},"messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyMetadataPin, ""},
		{"metadata audit string", chat, `{"model":"m","metadata":{"audit":"true"},"messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyMetadataAudit, ""},
		{"metadata audit bool", chat, `{"model":"m","metadata":{"audit":true},"messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyMetadataAudit, ""},
		{"metadata audit colon", chat, `{"model":"m","metadata":{"audit:true":"1"},"messages":[{"role":"user","content":"a"}]}`, Flags{}, false, WhyMetadataAudit, ""},
		{"image part is not plain text", chat, `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"u"}}]}]}`, Flags{}, false, WhyNonText, ""},
		{"responses function_call item", resp, `{"model":"m","input":[{"type":"function_call","name":"f"}]}`, Flags{}, false, WhyMultiTurn, ""},
		{"responses assistant item", resp, `{"model":"m","input":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}]}`, Flags{}, false, WhyMultiTurn, ""},
		{"no user message", chat, `{"model":"m","messages":[{"role":"system","content":"s"}]}`, Flags{}, false, WhyNoUserText, ""},
		{"blank user text", chat, `{"model":"m","messages":[{"role":"user","content":"   "}]}`, Flags{}, false, WhyNoUserText, ""},
		{"not json", chat, `{`, Flags{}, false, WhyBody, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(tc.path, []byte(tc.body), tc.flags)
			if got.OK != tc.ok || got.Why != tc.why {
				t.Fatalf("Check = {ok:%v why:%q}, want {ok:%v why:%q}", got.OK, got.Why, tc.ok, tc.why)
			}
			if tc.ok && got.UserText != tc.text {
				t.Fatalf("user text = %q, want %q", got.UserText, tc.text)
			}
		})
	}
}

func TestIsReasoningModelName(t *testing.T) {
	for name, want := range map[string]bool{
		"o7":               true,
		"o7-mini":          true,
		"vendor/o9-x":      true,
		"model-r3":         true,
		"model-a-thinking": true,
		"model-reasoner":   true,
		"model-a":          false,
		"model-b":          false,
		"smart":            false,
		"gamma-4o":         false,
		"router-2":         false,
	} {
		if got := IsReasoningModelName(name); got != want {
			t.Errorf("IsReasoningModelName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCheck_TruncatesUserTextOnARuneBoundary(t *testing.T) {
	long := strings.Repeat("中", MaxUserTextBytes) // 3 bytes per rune
	body := `{"model":"m","messages":[{"role":"user","content":"` + long + `"}]}`
	got := Check("/v1/chat/completions", []byte(body), Flags{})
	if !got.OK {
		t.Fatalf("not eligible: %+v", got)
	}
	if len(got.UserText) > MaxUserTextBytes {
		t.Fatalf("user text is %d bytes, cap is %d", len(got.UserText), MaxUserTextBytes)
	}
	if len(got.UserText)%3 != 0 {
		t.Fatalf("truncation split a rune: %d bytes", len(got.UserText))
	}
}

func TestSupportedPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/v1/chat/completions":  true,
		"/v1/chat/completions/": true,
		"/v1/responses":         true,
		"/v1/messages":          false,
		"/v1/embeddings":        false,
		"/v1/responses/compact": false,
		"/pg/chat/completions":  false,
	} {
		if got := SupportedPath(p); got != want {
			t.Errorf("SupportedPath(%q) = %v, want %v", p, got, want)
		}
	}
}
