package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestRelayOutcomeWiring_BothSitesPassRelayInfo pins the two call sites in
// relay.go that feed relay_total_duration_seconds and relay_requests_total.
// No hermetic test can drive Relay() past channel selection to the second
// site, so this lock reads the source instead: both calls must pass the
// live relayInfo (never nil, never a literal) so product and client_gone
// keep flowing into the series. Deleting a site or passing nil goes red.
func TestRelayOutcomeWiring_BothSitesPassRelayInfo(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "relay.go", nil, 0)
	if err != nil {
		t.Fatalf("parse relay.go: %v", err)
	}
	var calls []*ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "observeRelayOutcome" {
			calls = append(calls, call)
		}
		return true
	})
	if len(calls) != 2 {
		t.Fatalf("observeRelayOutcome call sites in relay.go = %d, want 2 (end-to-end defer + post-channel recorder)", len(calls))
	}
	for _, call := range calls {
		if len(call.Args) != 6 {
			t.Fatalf("%s: observeRelayOutcome called with %d args, want 6", fset.Position(call.Pos()), len(call.Args))
		}
		id, ok := call.Args[2].(*ast.Ident)
		if !ok || id.Name != "relayInfo" {
			t.Errorf("%s: third argument must be the live relayInfo, got %T %v", fset.Position(call.Pos()), call.Args[2], call.Args[2])
		}
	}
}
