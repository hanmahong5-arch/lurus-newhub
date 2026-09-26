package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestGate_EveryBreakerAdmittedPathReportsAnOutcome is a STRUCTURAL gate over
// relay.go's retry loop.
//
// WHAT IT SCANS: it parses relay.go, finds the for-loop in Relay() that
// contains the `channelBreakers.Allow(...)` admission check, and walks the
// statements that follow that check inside the loop body. Every `break` and
// every `return` reachable from there must be preceded, in its own block or an
// enclosing one, by a call to reportBreakerOutcome(...).
//
// WHY: the breaker hands out exactly one probe slot in HalfOpen. A path that
// leaves the iteration without reporting keeps that slot forever, and the
// channel is dropped from routing until the process restarts — the cycle 14
// L1-A defect. Behavioural tests can only cover the paths someone thought to
// write a fixture for (a 400 from the upstream, today); this gate covers the
// paths nobody has written a fixture for yet.
//
// BLIND SPOTS — what this gate structurally CANNOT see:
//   - whether the reported outcome is the RIGHT one. Reporting success for a
//     failed probe satisfies this gate; TestRelay_UserErrorDoesNotTripHealthy
//     Channel and TestRelay_AdmittedRequestAlwaysReportsAnOutcome are what
//     cover that.
//   - a report made indirectly (inside a helper this loop calls, or via a
//     different registry method). It matches the literal identifier
//     reportBreakerOutcome only.
//   - exits that are not `break`/`return`: a panic, an os.Exit, a goroutine
//     that outlives the iteration. Function literals are skipped entirely, so
//     a `return` inside a closure is neither required to report nor accepted
//     as one.
//   - anything in the OTHER relay loop (RelayTask), which does not consult the
//     breaker at all today. If it ever does, this gate will not notice.
//   - correctness of the admission check itself; it only anchors the region.
func TestGate_EveryBreakerAdmittedPathReportsAnOutcome(t *testing.T) {
	const (
		file          = "relay.go"
		admissionCall = "channelBreakers.Allow"
		reportCall    = "reportBreakerOutcome"
	)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	loop := findBreakerRetryLoop(f, admissionCall)
	if loop == nil {
		t.Fatalf("no for-loop containing %s(...) found in %s — the gate's anchor is gone; "+
			"a gate that matches nothing is not evidence, so fix the anchor rather than deleting this test",
			admissionCall, file)
	}

	admissionIdx := -1
	for i, stmt := range loop.Body.List {
		if stmtContainsCall(stmt, admissionCall) {
			admissionIdx = i
			break
		}
	}
	if admissionIdx < 0 {
		t.Fatalf("the %s(...) check is no longer a direct statement of the loop body", admissionCall)
	}

	region := loop.Body.List[admissionIdx+1:]
	if len(region) == 0 {
		t.Fatal("nothing follows the admission check — the anchor is in the wrong place")
	}

	var exits int
	var unreported []string
	walkExits(region, false, reportCall, &exits, func(pos token.Pos) {
		unreported = append(unreported, fset.Position(pos).String())
	})

	if exits == 0 {
		t.Fatal("found no break/return after the admission check — the gate scanned nothing, " +
			"which is not the same as finding nothing wrong")
	}
	for _, where := range unreported {
		t.Errorf("%s: this path leaves the retry loop after the breaker admitted the request "+
			"without calling %s(...). In HalfOpen that strands the single probe slot and the "+
			"channel is excluded from routing until the process restarts.", where, reportCall)
	}
	t.Logf("gate checked %d loop exits after the breaker admission point", exits)
}

// findBreakerRetryLoop returns the for-loop whose body mentions callName.
func findBreakerRetryLoop(f *ast.File, callName string) *ast.ForStmt {
	var found *ast.ForStmt
	ast.Inspect(f, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		loop, ok := n.(*ast.ForStmt)
		if !ok || loop.Body == nil {
			return true
		}
		for _, stmt := range loop.Body.List {
			if stmtContainsCall(stmt, callName) {
				found = loop
				return false
			}
		}
		return true
	})
	return found
}

// stmtContainsCall reports whether stmt contains a call to the dotted name
// callName ("pkg.Fn" or "fn"), ignoring function literals.
func stmtContainsCall(stmt ast.Stmt, callName string) bool {
	hit := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		if hit {
			return false
		}
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if calleeName(call.Fun) == callName {
			hit = true
			return false
		}
		return true
	})
	return hit
}

func calleeName(e ast.Expr) string {
	switch fn := e.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name + "." + fn.Sel.Name
		}
	}
	return ""
}

// walkExits walks stmts in order. reported carries "an outcome has already
// been reported on the way here". Every break/return it meets is counted, and
// onUnreported is called for those reached with reported == false.
func walkExits(stmts []ast.Stmt, reported bool, reportCall string, exits *int, onUnreported func(token.Pos)) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.BranchStmt:
			if s.Tok == token.BREAK {
				*exits++
				if !reported {
					onUnreported(s.Pos())
				}
			}
			continue
		case *ast.ReturnStmt:
			*exits++
			if !reported {
				onUnreported(s.Pos())
			}
			continue
		}

		if !reported && stmtContainsCall(stmt, reportCall) {
			// A report that IS this statement (or its initialiser) counts for
			// everything after it in this block. A report nested inside an
			// if-body only counts inside that body, which the recursion below
			// handles, so check for a top-level expression statement first.
			if expr, ok := stmt.(*ast.ExprStmt); ok && stmtContainsCall(expr, reportCall) {
				reported = true
				continue
			}
		}

		// Recurse into nested blocks with the flag as it stands here.
		switch s := stmt.(type) {
		case *ast.IfStmt:
			walkExits(s.Body.List, reported, reportCall, exits, onUnreported)
			if s.Else != nil {
				switch e := s.Else.(type) {
				case *ast.BlockStmt:
					walkExits(e.List, reported, reportCall, exits, onUnreported)
				case *ast.IfStmt:
					walkExits([]ast.Stmt{e}, reported, reportCall, exits, onUnreported)
				}
			}
		case *ast.BlockStmt:
			walkExits(s.List, reported, reportCall, exits, onUnreported)
		case *ast.SwitchStmt:
			for _, cc := range s.Body.List {
				if clause, ok := cc.(*ast.CaseClause); ok {
					// A `break` inside a switch leaves the switch, not the
					// loop, so those are not loop exits — skip the clause
					// bodies' top-level breaks by walking with a counter we
					// discard for BranchStmt at this level.
					walkSwitchClause(clause.Body, reported, reportCall, exits, onUnreported)
				}
			}
		case *ast.TypeSwitchStmt:
			for _, cc := range s.Body.List {
				if clause, ok := cc.(*ast.CaseClause); ok {
					walkSwitchClause(clause.Body, reported, reportCall, exits, onUnreported)
				}
			}
		}
	}
}

// walkSwitchClause is walkExits for a switch case body: a bare `break` there
// belongs to the switch, so only `return` counts as a loop exit.
func walkSwitchClause(stmts []ast.Stmt, reported bool, reportCall string, exits *int, onUnreported func(token.Pos)) {
	filtered := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		if br, ok := stmt.(*ast.BranchStmt); ok && br.Tok == token.BREAK {
			continue
		}
		filtered = append(filtered, stmt)
	}
	walkExits(filtered, reported, reportCall, exits, onUnreported)
}
