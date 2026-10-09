package contentpolicy

import (
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"
)

const (
	testPhone = "13800138000"
	testID    = "11010519491231002X"
	testCard  = "4111111111111111"
)

func rule(id int64, kind, mode, builtin string) Rule {
	return Rule{Id: id, Scope: ScopeTenant, TenantId: "t1", Ordinal: id, RoleScope: RoleAny,
		Kind: kind, PatternType: PatternBuiltin, Builtin: builtin, Mode: mode, Enabled: true}
}

func compile(t *testing.T, rules ...Rule) *Ruleset {
	t.Helper()
	rs, errs := Compile(rules)
	if len(errs) != 0 {
		t.Fatalf("compile: %v", errs)
	}
	return rs
}

// ---- retention -------------------------------------------------------

func TestResolveOnlyTightens(t *testing.T) {
	cases := []struct{ platform, tenant, token, want RetentionMode }{
		{"", "", "", RetentionFull},
		{RetentionFull, "", "", RetentionFull},
		{"", RetentionMetadataOnly, "", RetentionMetadataOnly},
		{"", "", RetentionNone, RetentionNone},
		// A token can never relax its tenant (the bifrost "key-level decision
		// overridden" class of defect, inverted).
		{"", RetentionNone, RetentionFull, RetentionNone},
		{"", RetentionMetadataOnly, RetentionFull, RetentionMetadataOnly},
		// A tenant can never relax the platform floor.
		{RetentionMetadataOnly, RetentionFull, RetentionFull, RetentionMetadataOnly},
		{RetentionNone, RetentionMetadataOnly, "", RetentionNone},
		// The token layer can tighten past the tenant.
		{"", RetentionMetadataOnly, RetentionNone, RetentionNone},
		// Garbage values never loosen.
		{"", RetentionMetadataOnly, "bogus", RetentionMetadataOnly},
	}
	for _, c := range cases {
		if got := Resolve(c.platform, c.tenant, c.token); got != c.want {
			t.Errorf("Resolve(%q,%q,%q) = %q, want %q", c.platform, c.tenant, c.token, got, c.want)
		}
	}
}

func TestLooser(t *testing.T) {
	if !Looser(RetentionFull, RetentionMetadataOnly) {
		t.Error("full under metadata_only floor must be looser")
	}
	if Looser(RetentionNone, RetentionMetadataOnly) || Looser(RetentionInherit, RetentionNone) {
		t.Error("tightening / inherit must not count as loosening")
	}
}

func sampleRecord() *LogRecord {
	return &LogRecord{
		Content:            "user said: hello",
		Ip:                 "10.1.2.3",
		RequestFingerprint: "abcd",
		Other: map[string]interface{}{
			"request_id":     "r-1",
			"frt":            120.0,
			"model_ratio":    1.5,
			"is_stream":      true,
			"reject_reason":  "free text with a secret",
			"source_product": "hub",
			"admin_info": map[string]interface{}{
				"use_channel":     []string{"1", "2"},
				"multi_key_index": 3,
				"note":            "free text",
			},
		},
	}
}

func TestApplyRetentionFullIsNoop(t *testing.T) {
	r := sampleRecord()
	ApplyRetention(RetentionFull, r)
	if r.Content == "" || r.Ip == "" || r.RequestFingerprint == "" || r.Other["reject_reason"] == nil {
		t.Fatalf("full must not trim: %+v", r)
	}
}

func TestApplyRetentionMetadataOnly(t *testing.T) {
	r := sampleRecord()
	ApplyRetention(RetentionMetadataOnly, r)
	if r.Content != "" {
		t.Errorf("content kept: %q", r.Content)
	}
	if _, ok := r.Other["reject_reason"]; ok {
		t.Error("free-text Other key survived")
	}
	for _, k := range []string{"frt", "model_ratio", "is_stream", "request_id", "source_product"} {
		if _, ok := r.Other[k]; !ok {
			t.Errorf("metadata key %q dropped", k)
		}
	}
	admin := r.Other["admin_info"].(map[string]interface{})
	if _, ok := admin["note"]; ok {
		t.Error("nested free text survived")
	}
	if admin["multi_key_index"] != 3 || admin["use_channel"] == nil {
		t.Errorf("nested metadata dropped: %v", admin)
	}
	if r.Ip == "" || r.RequestFingerprint == "" {
		t.Error("metadata_only keeps ip/fingerprint (they are not content)")
	}
}

func TestApplyRetentionNone(t *testing.T) {
	r := sampleRecord()
	ApplyRetention(RetentionNone, r)
	if r.Content != "" || r.Ip != "" || r.RequestFingerprint != "" {
		t.Errorf("none must also drop ip/fingerprint: %+v", r)
	}
	if _, ok := r.Other["source_product"]; !ok {
		t.Error("none keeps the product attribution")
	}
	if _, ok := r.Other["frt"]; !ok {
		t.Error("none keeps numeric usage metadata")
	}
	// the caller's map must not be mutated in place (it may be shared).
	src := sampleRecord()
	orig := src.Other
	ApplyRetention(RetentionNone, src)
	if orig["reject_reason"] == nil {
		t.Error("original Other map mutated")
	}
}

func TestPlatformDefaultEnvAndOverride(t *testing.T) {
	t.Setenv("CONTENT_RETENTION_DEFAULT", "metadata_only")
	if PlatformDefault() != RetentionMetadataOnly {
		t.Fatal("env default not honoured")
	}
	t.Setenv("CONTENT_RETENTION_DEFAULT", "garbage")
	if PlatformDefault() != RetentionFull {
		t.Fatal("invalid env must fall back to full")
	}
	m := RetentionNone
	SetPlatformDefault(&m)
	defer SetPlatformDefault(nil)
	if PlatformDefault() != RetentionNone {
		t.Fatal("override ignored")
	}
}

// ---- builtin detectors -----------------------------------------------

func TestBuiltinChecksums(t *testing.T) {
	if !ValidIDCardCN(testID) {
		t.Fatal("known-valid resident id rejected")
	}
	bad := testID[:17] + "3"
	if ValidIDCardCN(bad) {
		t.Fatal("bad check digit accepted")
	}
	if ValidIDCardCN("110105194913310021") {
		t.Fatal("impossible month accepted")
	}
	if !LuhnValid(testCard) || LuhnValid("4111111111111112") {
		t.Fatal("luhn wrong")
	}
}

func TestBuiltinMatching(t *testing.T) {
	cases := []struct {
		builtin, in string
		want        int
	}{
		{BuiltinPhoneCN, "call " + testPhone + " now", 1},
		{BuiltinPhoneCN, "id 1" + testPhone, 0}, // glued to a longer digit run
		{BuiltinPhoneCN, "12800138000", 0},      // not a mobile prefix
		{BuiltinIDCardCN, "id=" + testID, 1},
		{BuiltinIDCardCN, "id=" + testID[:17] + "3", 0}, // wrong check digit
		{BuiltinBankCard, "card " + testCard, 1},
		{BuiltinBankCard, "card 4111 1111 1111 1111", 1},
		{BuiltinBankCard, "card 4111111111111112", 0}, // fails Luhn
		{BuiltinEmail, "mail a.b+c@example.com please", 1},
		{BuiltinSecretKey, "key sk-" + strings.Repeat("a", 24), 1},
		{BuiltinSecretKey, "aws AKIA" + strings.Repeat("A", 16), 1},
		{BuiltinSecretKey, "sk-short", 0},
	}
	for _, c := range cases {
		if got := len(builtinDetectors[c.builtin].find(c.in)); got != c.want {
			t.Errorf("%s on %q: %d matches, want %d", c.builtin, c.in, got, c.want)
		}
	}
}

// ---- validation ------------------------------------------------------

func TestValidateRuleLimits(t *testing.T) {
	ok := rule(1, KindMask, ModeEnforce, BuiltinPhoneCN)
	if err := ValidateRule(&ok); err != nil {
		t.Fatalf("valid rule refused: %v", err)
	}
	re := Rule{Scope: ScopeTenant, TenantId: "t", RoleScope: RoleAny, Kind: KindMask, PatternType: PatternRegex, Mode: ModeObserve}
	bad := map[string]string{
		"empty":       "",
		"too long":    strings.Repeat("a", MaxPatternLen+1),
		"bad syntax":  "(unclosed",
		"empty match": "a*",
		"too complex": "(?:" + strings.Repeat("[a-z]{1,1000}", 5) + ")",
	}
	for name, p := range bad {
		r := re
		r.Pattern = p
		if err := ValidateRule(&r); err == nil {
			t.Errorf("%s pattern accepted", name)
		}
	}
	good := re
	good.Pattern = `proj-[0-9]{4}`
	if err := ValidateRule(&good); err != nil {
		t.Errorf("good regex refused: %v", err)
	}
	for name, mut := range map[string]func(*Rule){
		"scope":   func(r *Rule) { r.Scope = "x" },
		"role":    func(r *Rule) { r.RoleScope = "x" },
		"kind":    func(r *Rule) { r.Kind = "x" },
		"mode":    func(r *Rule) { r.Mode = "x" },
		"builtin": func(r *Rule) { r.Builtin = "nope" },
		"tenant":  func(r *Rule) { r.TenantId = "" },
		"plat":    func(r *Rule) { r.Scope = ScopePlatform },
	} {
		r := ok
		mut(&r)
		if err := ValidateRule(&r); err == nil {
			t.Errorf("%s: invalid rule accepted", name)
		}
	}
}

func TestCompileSkipsBadRuleKeepsOthers(t *testing.T) {
	good := rule(1, KindMask, ModeEnforce, BuiltinPhoneCN)
	bad := Rule{Id: 2, Scope: ScopeTenant, TenantId: "t", RoleScope: RoleAny, Kind: KindMask,
		PatternType: PatternRegex, Pattern: "(", Mode: ModeEnforce, Enabled: true}
	off := rule(3, KindMask, ModeEnforce, BuiltinEmail)
	off.Enabled = false
	rs, errs := Compile([]Rule{good, bad, off})
	if len(errs) != 1 || rs.Len() != 1 {
		t.Fatalf("errs=%v len=%d", errs, rs.Len())
	}
}

func TestCompileOrdersPlatformFirst(t *testing.T) {
	tenant := rule(1, KindMask, ModeEnforce, BuiltinPhoneCN)
	tenant.Ordinal = 0
	plat := rule(2, KindMask, ModeEnforce, BuiltinEmail)
	plat.Scope, plat.TenantId, plat.Ordinal = ScopePlatform, "", 9
	rs := compile(t, tenant, plat)
	if rs.rules[0].Id != 2 {
		t.Fatalf("platform rule must run first: %v", rs.rules[0].Id)
	}
}

// ---- four request formats --------------------------------------------

var formatBodies = map[Format]struct {
	body  string
	paths []string // where the phone must be gone after masking
	other string   // a field that must survive byte-for-byte
}{
	FormatOpenAIChat: {
		body:  `{"model":"model-a","temperature":0.5,"messages":[{"role":"system","content":"you are ok"},{"role":"user","content":"tel ` + testPhone + `"},{"role":"user","content":[{"type":"text","text":"also ` + testPhone + `"},{"type":"image_url","image_url":{"url":"http://x/y.png"}}]}]}`,
		paths: []string{"messages.1.content", "messages.2.content.0.text"},
		other: `"temperature":0.5`,
	},
	FormatResponses: {
		body:  `{"model":"model-a","instructions":"sys","input":[{"role":"user","content":[{"type":"input_text","text":"tel ` + testPhone + `"}]},{"role":"assistant","content":"prev ` + testPhone + `"}],"store":false}`,
		paths: []string{"input.0.content.0.text", "input.1.content"},
		other: `"store":false`,
	},
	FormatClaude: {
		body:  `{"model":"model-a","max_tokens":9,"system":"sys","messages":[{"role":"user","content":"tel ` + testPhone + `"},{"role":"assistant","content":[{"type":"text","text":"echo ` + testPhone + `"}]}]}`,
		paths: []string{"messages.0.content", "messages.1.content.0.text"},
		other: `"max_tokens":9`,
	},
	FormatGemini: {
		body:  `{"systemInstruction":{"parts":[{"text":"sys"}]},"contents":[{"role":"user","parts":[{"text":"tel ` + testPhone + `"}]},{"role":"model","parts":[{"text":"echo ` + testPhone + `"}]}],"generationConfig":{"topK":3}}`,
		paths: []string{"contents.0.parts.0.text", "contents.1.parts.0.text"},
		other: `"generationConfig":{"topK":3}`,
	},
}

func TestMaskEveryFormat(t *testing.T) {
	rs := compile(t, rule(1, KindMask, ModeEnforce, BuiltinPhoneCN))
	for f, c := range formatBodies {
		res := rs.Apply(f, []byte(c.body))
		if res.Rejected != nil || !res.Changed {
			t.Fatalf("%s: rejected=%v changed=%v", f, res.Rejected, res.Changed)
		}
		if strings.Contains(string(res.Body), testPhone) {
			t.Errorf("%s: phone survived: %s", f, res.Body)
		}
		for _, p := range c.paths {
			if v := gjson.GetBytes(res.Body, p).String(); !strings.Contains(v, "[PHONE]") {
				t.Errorf("%s: %s = %q lacks placeholder", f, p, v)
			}
		}
		if !strings.Contains(string(res.Body), c.other) {
			t.Errorf("%s: unrelated field %s not preserved: %s", f, c.other, res.Body)
		}
		if len(res.Hits) != 1 || res.Hits[0].Count != 2 {
			t.Errorf("%s: hits=%+v", f, res.Hits)
		}
	}
}

func TestRejectEveryFormat(t *testing.T) {
	rs := compile(t, rule(7, KindReject, ModeEnforce, BuiltinPhoneCN))
	for f, c := range formatBodies {
		res := rs.Apply(f, []byte(c.body))
		if res.Rejected == nil || res.Rejected.RuleId != 7 {
			t.Errorf("%s: not rejected: %+v", f, res)
		}
	}
}

func TestObserveNeverChangesRequest(t *testing.T) {
	rs := compile(t,
		rule(1, KindMask, ModeObserve, BuiltinPhoneCN),
		rule(2, KindReject, ModeObserve, BuiltinPhoneCN))
	for f, c := range formatBodies {
		res := rs.Apply(f, []byte(c.body))
		if res.Changed || res.Rejected != nil || string(res.Body) != c.body {
			t.Errorf("%s: observe altered the request", f)
		}
		if len(res.Hits) != 2 {
			t.Errorf("%s: observe must still count hits: %+v", f, res.Hits)
		}
	}
}

func TestRoleScope(t *testing.T) {
	r := rule(1, KindMask, ModeEnforce, BuiltinPhoneCN)
	r.RoleScope = RoleAssistant
	rs := compile(t, r)
	body := formatBodies[FormatClaude].body
	res := rs.Apply(FormatClaude, []byte(body))
	if gjson.GetBytes(res.Body, "messages.0.content").String() != "tel "+testPhone {
		t.Error("user text masked by an assistant-only rule")
	}
	if !strings.Contains(gjson.GetBytes(res.Body, "messages.1.content.0.text").String(), "[PHONE]") {
		t.Error("assistant text not masked")
	}
}

func TestRulesApplyInOrder(t *testing.T) {
	// rule 1 masks the phone, so a later reject on the phone must not fire.
	rs := compile(t, rule(1, KindMask, ModeEnforce, BuiltinPhoneCN), rule(2, KindReject, ModeEnforce, BuiltinPhoneCN))
	res := rs.Apply(FormatOpenAIChat, []byte(formatBodies[FormatOpenAIChat].body))
	if res.Rejected != nil {
		t.Fatal("reject saw text that an earlier mask already removed")
	}
}

func TestCustomRegexAndPlaceholder(t *testing.T) {
	r := Rule{Id: 5, Scope: ScopeTenant, TenantId: "t", RoleScope: RoleAny, Kind: KindMask,
		PatternType: PatternRegex, Pattern: `proj-[0-9]{4}`, Replacement: "<P>", Mode: ModeEnforce, Enabled: true}
	rs := compile(t, r)
	res := rs.Apply(FormatOpenAIChat, []byte(`{"messages":[{"role":"user","content":"see proj-1234 and proj-9999"}]}`))
	if got := gjson.GetBytes(res.Body, "messages.0.content").String(); got != "see <P> and <P>" {
		t.Fatalf("got %q", got)
	}
}

func TestMaskHandlesJSONEscapingAndUnicode(t *testing.T) {
	rs := compile(t, rule(1, KindMask, ModeEnforce, BuiltinPhoneCN))
	body := `{"messages":[{"role":"user","content":"\"quoted\"\n手机 ` + testPhone + ` 结束 é"}]}`
	res := rs.Apply(FormatOpenAIChat, []byte(body))
	got := gjson.GetBytes(res.Body, "messages.0.content").String()
	if got != "\"quoted\"\n手机 [PHONE] 结束 é" {
		t.Fatalf("got %q", got)
	}
}

func TestInvalidOrEmptyBodyPassesThrough(t *testing.T) {
	rs := compile(t, rule(1, KindReject, ModeEnforce, BuiltinPhoneCN))
	for _, b := range []string{"", "not json " + testPhone, "{}"} {
		res := rs.Apply(FormatOpenAIChat, []byte(b))
		if res.Rejected != nil || res.Changed || string(res.Body) != b {
			t.Errorf("body %q was touched", b)
		}
	}
	var nilSet *Ruleset
	if res := nilSet.Apply(FormatOpenAIChat, []byte("x")); string(res.Body) != "x" {
		t.Error("nil ruleset altered body")
	}
}

// ---- metrics hook ----------------------------------------------------

type recSink struct {
	mu      sync.Mutex
	hits    map[int64]int
	rejects map[int64]int
}

func (s *recSink) RuleHit(id int64, _, _, _ string, n int) {
	s.mu.Lock()
	s.hits[id] += n
	s.mu.Unlock()
}
func (s *recSink) RuleReject(id int64) {
	s.mu.Lock()
	s.rejects[id]++
	s.mu.Unlock()
}

func TestReportFeedsSink(t *testing.T) {
	s := &recSink{hits: map[int64]int{}, rejects: map[int64]int{}}
	SetMetricsSink(s)
	defer SetMetricsSink(nil)
	rs := compile(t, rule(1, KindMask, ModeEnforce, BuiltinPhoneCN), rule(2, KindReject, ModeEnforce, BuiltinEmail))
	Report(rs.Apply(FormatOpenAIChat, []byte(formatBodies[FormatOpenAIChat].body)))
	if s.hits[1] != 2 || len(s.rejects) != 0 {
		t.Fatalf("hits=%v rejects=%v", s.hits, s.rejects)
	}
	Report(rs.Apply(FormatOpenAIChat, []byte(`{"messages":[{"role":"user","content":"a@b.com"}]}`)))
	if s.rejects[2] != 1 {
		t.Fatalf("rejects=%v", s.rejects)
	}
	SetMetricsSink(nil)
	Report(Result{Hits: []Hit{{RuleId: 1, Count: 1}}}) // must not panic
}
