package common

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestFirstResponseTimeHasASingleWriter is the prerequisite for promoting time
// to first byte to an SLI.
//
// doc/slo-relay.md claims TTFB "is not measured" and would need changes across
// ~20 providers. That is already out of date: other.frt is written for every
// request. But the instrumentation point it implies — put a histogram
// observation inside RelayInfo.SetFirstResponseTime and get every provider for
// free — is quietly false today, because two providers assign the field
// directly instead of calling the setter:
//
//	provider/cloudflare/relay_cloudflare.go: info.FirstResponseTime = time.Now()
//	provider/cohere/relay-cohere.go:         info.FirstResponseTime = time.Now()
//
// Their first-token timings would simply be absent from the histogram, and
// nothing would say so — a silently incomplete SLI is worse than an absent
// one, because it looks like coverage. Fixing the two call sites first, and
// keeping them fixed with this gate, is what makes the setter a real seam.
//
// The setter is not merely a wrapper: it is guarded by isFirstResponse, so it
// records the FIRST frame only. A direct assignment inside a per-frame loop
// depends on the caller remembering its own isFirst flag — which both sites do
// today, but that is a coincidence of their current shape, not a property the
// type enforces.
func TestFirstResponseTimeHasASingleWriter(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve internal/ root: %v", err)
	}
	providerDir := filepath.Join(root, "adapter", "provider")

	// The setter itself is the one legitimate writer.
	const setterFile = "adapter/provider/common/relay_info.go"

	var offenders []string
	inspected := 0

	err = filepath.WalkDir(providerDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relSlash := filepath.ToSlash(rel)

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", relSlash, parseErr)
		}
		inspected++

		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil || sel.Sel.Name != "FirstResponseTime" {
					continue
				}
				if relSlash == setterFile {
					// SetFirstResponseTime and the constructor's "never
					// happened" sentinel both live here, by design.
					continue
				}
				offenders = append(offenders, fmt.Sprintf(
					"%s:%d", relSlash, fset.Position(sel.Pos()).Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk provider tree: %v", err)
	}

	// Guard against the scan silently measuring nothing (a moved directory, a
	// changed layout): 20+ providers live under this tree.
	if inspected < 20 {
		t.Fatalf("only inspected %d provider files — the scan is not looking where it thinks", inspected)
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("FirstResponseTime assigned outside SetFirstResponseTime at:\n  %s\n\n"+
			"Call info.SetFirstResponseTime() instead. Any instrumentation added to the "+
			"setter (a TTFB histogram, a trace span) silently skips these providers, "+
			"producing an SLI that looks complete and is not. The setter also carries the "+
			"isFirstResponse guard, so it records the first frame rather than trusting each "+
			"caller to keep that state correctly.",
			strings.Join(offenders, "\n  "))
	}
}

// TestSetFirstResponseTime_RecordsOnlyTheFirstCall pins the behaviour the two
// migrated providers now depend on. They previously assigned the field
// directly, guarded by their own local isFirst flag; the setter has to actually
// record on the first call (isFirstResponse starts true) and ignore later ones.
// If the guard defaulted the other way, switching them to the setter would have
// silently stopped recording frt for those providers — the change would compile,
// the gate above would pass, and time-to-first-token would just quietly vanish.
func TestSetFirstResponseTime_RecordsOnlyTheFirstCall(t *testing.T) {
	start := time.Now()
	info := &RelayInfo{
		StartTime: start,
		// Exactly how GenRelayInfo seeds it: "never happened".
		FirstResponseTime: start.Add(-time.Second),
		isFirstResponse:   true,
	}

	if info.HasSendResponse() {
		t.Fatal("sentinel state must report no response sent yet")
	}

	// A real first token never arrives in zero elapsed time, and this has to be
	// longer than the platform clock's granularity: HasSendResponse is a strict
	// After(), and on Windows two back-to-back time.Now() calls can return the
	// same instant, which made this assertion fail for a reason that has
	// nothing to do with the code under test.
	time.Sleep(20 * time.Millisecond)

	info.SetFirstResponseTime()
	if !info.HasSendResponse() {
		t.Fatal("first call must record the time — otherwise frt is silently lost " +
			"for every provider that routes through the setter")
	}
	first := info.FirstResponseTime

	time.Sleep(20 * time.Millisecond)
	info.SetFirstResponseTime()
	if !info.FirstResponseTime.Equal(first) {
		t.Errorf("a later call moved the timestamp to %v (was %v) — this is time to "+
			"FIRST token, so a per-frame call site must not keep overwriting it",
			info.FirstResponseTime, first)
	}
}

// The constructor must seed isFirstResponse true, which is what makes the
// setter record anything at all.
func TestGenRelayInfoSeedsFirstResponseGuard(t *testing.T) {
	info := &RelayInfo{}
	if info.isFirstResponse {
		t.Fatal("zero value should be false — this test would prove nothing otherwise")
	}
	// Read the real seed from the constructor's literal rather than trusting a
	// hand-built struct: relay_info.go:461.
	src, err := os.ReadFile("relay_info.go")
	if err != nil {
		t.Fatalf("read relay_info.go: %v", err)
	}
	if !strings.Contains(string(src), "isFirstResponse: true") {
		t.Error("GenRelayInfo no longer seeds isFirstResponse:true — SetFirstResponseTime " +
			"becomes a no-op and every provider silently stops recording frt")
	}
}
