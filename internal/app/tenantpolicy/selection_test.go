package tenantpolicy

import (
	"reflect"
	"testing"
)

func TestCovered(t *testing.T) {
	plat := []string{"gpt-4o", "claude-*"}
	cases := map[string]bool{
		"gpt-4o": true, "gpt-3.5": false, "claude-3": true, "claude-*": true,
		"claude-3*": true, "gpt-*": false, "gpt-4o*": false, "*": false,
	}
	for e, want := range cases {
		if got := Covered(plat, e); got != want {
			t.Errorf("Covered(%q)=%v want %v", e, got, want)
		}
	}
	if !Covered([]string{"*"}, "anything*") {
		t.Error("star ceiling covers all")
	}
}

func TestEffective_NeverWiderThanPlatform(t *testing.T) {
	plat := []string{"gpt-4o", "claude-*"}
	got := Effective(plat, true, []string{"gpt-4o", "gemini-pro", "claude-3*"}, true)
	want := []string{"gpt-4o", "claude-3*"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// platform later narrowed below a wildcard selection
	got = Effective([]string{"gpt-4o"}, true, []string{"gpt-*"}, true)
	if !reflect.DeepEqual(got, []string{"gpt-4o"}) {
		t.Fatalf("got %v", got)
	}
	if g := Effective(nil, false, nil, false); !reflect.DeepEqual(g, []string{"*"}) {
		t.Fatalf("got %v", g)
	}
	if g := Effective(plat, true, nil, false); !reflect.DeepEqual(g, plat) {
		t.Fatalf("got %v", g)
	}
	if g := Effective(nil, false, []string{"a"}, true); !reflect.DeepEqual(g, []string{"a"}) {
		t.Fatalf("got %v", g)
	}
	if g := Effective(plat, true, []string{}, true); len(g) != 0 {
		t.Fatalf("empty selection = deny-all, got %v", g)
	}
}
