package contentpolicy

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// A question key containing '.' and '*' must not be read as a path operator:
// an unescaped key would address the wrong node (or none) and leave PII behind.
const sysOneBody = `{"state":"call ` + testPhone + ` now","model":"model-a","lang":"en",` +
	`"questions":{` +
	`"a.b*":{"type":"noul","instructions":"is ` + testPhone + ` urgent?"},` +
	`"c":{"type":"score","instructions":["x","y ` + testPhone + `"],"criteria":{"1":"bad ` + testPhone + `","2":"fine"},"custom":"keep ` + testPhone + `"}}}`

func TestSystemOneMaskCoversStateInstructionsCriteria(t *testing.T) {
	rs := compile(t, rule(1, KindMask, ModeEnforce, BuiltinPhoneCN))
	res := rs.Apply(FormatSystemOne, []byte(sysOneBody))
	if res.Rejected != nil || !res.Changed {
		t.Fatalf("rejected=%v changed=%v", res.Rejected, res.Changed)
	}
	b := res.Body
	for p, want := range map[string]string{
		"state":                         "call [PHONE] now",
		`questions.a\.b\*.instructions`: "is [PHONE] urgent?",
		"questions.c.instructions.1":    "y [PHONE]",
		"questions.c.criteria.1":        "bad [PHONE]",
		"questions.c.criteria.2":        "fine",
		"questions.c.instructions.0":    "x",
		`questions.a\.b\*.type`:         "noul",
		"model":                         "model-a",
	} {
		if got := gjson.GetBytes(b, p).String(); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	// Fields outside state/questions.*.(instructions|criteria) are not
	// rewritten, unknown ones included.
	if got := gjson.GetBytes(b, "questions.c.custom").String(); !strings.Contains(got, testPhone) {
		t.Errorf("unknown field was rewritten: %q", got)
	}
	if res.Hits[0].Count != 4 {
		t.Errorf("hits=%+v", res.Hits)
	}
}

func TestSystemOneStructuredStateStringsMasked(t *testing.T) {
	rs := compile(t, rule(1, KindMask, ModeEnforce, BuiltinPhoneCN))
	res := rs.Apply(FormatSystemOne, []byte(`{"state":{"msg":"tel `+testPhone+`","n":3},"questions":{}}`))
	if !res.Changed || strings.Contains(string(res.Body), testPhone) {
		t.Fatalf("%s", res.Body)
	}
	if gjson.GetBytes(res.Body, "state.n").Int() != 3 {
		t.Errorf("non-string value changed: %s", res.Body)
	}
}

func TestSystemOneRejectAndObserve(t *testing.T) {
	rj := compile(t, rule(7, KindReject, ModeEnforce, BuiltinPhoneCN))
	if res := rj.Apply(FormatSystemOne, []byte(sysOneBody)); res.Rejected == nil || res.Rejected.RuleId != 7 {
		t.Fatalf("not rejected: %+v", res)
	}
	// A match only in a question's criteria must also reject.
	if res := rj.Apply(FormatSystemOne, []byte(`{"state":"ok","questions":{"q":{"type":"score","criteria":{"1":"`+testPhone+`"}}}}`)); res.Rejected == nil {
		t.Fatal("criteria match not rejected")
	}
	ob := compile(t, rule(1, KindMask, ModeObserve, BuiltinPhoneCN), rule(2, KindReject, ModeObserve, BuiltinPhoneCN))
	res := ob.Apply(FormatSystemOne, []byte(sysOneBody))
	if res.Changed || res.Rejected != nil || string(res.Body) != sysOneBody || len(res.Hits) != 2 {
		t.Fatalf("observe altered the request: %+v", res)
	}
}

func TestSystemOneMalformedPassesThrough(t *testing.T) {
	rs := compile(t, rule(1, KindReject, ModeEnforce, BuiltinPhoneCN))
	for _, b := range []string{`not json`, `{"state":`, `{"questions":"x"}`, `{"state":null,"questions":{"q":5}}`} {
		if res := rs.Apply(FormatSystemOne, []byte(b)); res.Rejected != nil || res.Changed {
			t.Errorf("%q: %+v", b, res)
		}
	}
}
