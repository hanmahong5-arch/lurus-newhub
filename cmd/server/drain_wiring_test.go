package main

// drain_wiring_test.go — cycle-13 W wiring oracle for L11's graceful drain.
//
// internal/lifecycle/drain_test.go already proves the Drainer itself: it
// marks draining, counts what a blown budget cut, and returns nil in every
// one of its three outcomes. What that cannot prove is that the process
// actually shuts down THROUGH it. Before cycle 13 the shutdown goroutine in
// run() read
//
//	if err := httpServer.Shutdown(shutdownCtx); err != nil {
//	    return fmt.Errorf("HTTP server shutdown error: %w", err)
//	}
//
// and errgroup propagated that error to main()'s FatalLog + os.Exit(1). A
// pod that was still finishing in-flight relay streams when
// GRACEFUL_SHUTDOWN_TIMEOUT expired therefore crash-exited instead of
// exiting 0 — the busiest replica in a rollout was the one that looked like
// it had failed.
//
// So this file checks both halves of the claim:
//
//   - TestRunShutdownGoroutineRoutesThroughTheDrainer reads run()'s real
//     source (go/ast over cmd/server/main.go, not a copy of the body) and
//     asserts the graceful-shutdown closure calls lifecycle.Default().Shutdown
//     and has no return statement that yields anything but nil.
//   - TestDrainerShutdownOnBudgetExceededReturnsNil drives the very seam
//     that closure calls, on a real listening server with a request still in
//     flight and a budget far too small for it, and asserts the value run()
//     would have propagated is nil.
//
// Mutation (both verified red): put the old
// `if err := httpServer.Shutdown(shutdownCtx); err != nil { return
// fmt.Errorf(...) }` back in place of the Drainer call.

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/lifecycle"
)

// gracefulShutdownClosure returns the body of the g.Go(func() error { ... })
// closure in run() that performs the graceful shutdown — identified by the
// two things only it does: wait on <-ctx.Done() and build a context from
// config.Get().Server.GracefulShutdownTimeout.
func gracefulShutdownClosure(t *testing.T) (*ast.FuncLit, string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse cmd/server/main.go: %v", err)
	}

	var found *ast.FuncLit
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		var buf strings.Builder
		ast.Inspect(lit.Body, func(inner ast.Node) bool {
			switch v := inner.(type) {
			case *ast.SelectorExpr:
				buf.WriteString(v.Sel.Name + " ")
			case *ast.Ident:
				buf.WriteString(v.Name + " ")
			}
			return true
		})
		text := buf.String()
		if strings.Contains(text, "GracefulShutdownTimeout") && strings.Contains(text, "ctx Done") {
			found = lit
			return false
		}
		return true
	})
	if found == nil {
		t.Fatal("no graceful-shutdown closure found in run(): it is identified by <-ctx.Done() plus config.Get().Server.GracefulShutdownTimeout — if the shutdown path moved, move this gate with it rather than deleting it")
	}

	var rendered strings.Builder
	ast.Inspect(found.Body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				rendered.WriteString(id.Name + "." + sel.Sel.Name + " ")
			} else {
				rendered.WriteString("." + sel.Sel.Name + " ")
			}
		}
		return true
	})
	return found, rendered.String()
}

func TestRunShutdownGoroutineRoutesThroughTheDrainer(t *testing.T) {
	body, selectors := gracefulShutdownClosure(t)

	if !strings.Contains(selectors, "lifecycle.Default") || !strings.Contains(selectors, ".Shutdown") {
		t.Errorf("run()'s graceful-shutdown closure does not call lifecycle.Default().Shutdown; selectors seen: %s", selectors)
	}

	// Every return in that closure must be `return nil`. A returned error
	// reaches errgroup, and from there main()'s FatalLog + os.Exit(1) — the
	// crash-on-timeout cycle 13 L11 removed.
	ast.Inspect(body.Body, func(n ast.Node) bool {
		// Do not walk into nested function literals: their returns belong to
		// someone else's signature.
		if _, ok := n.(*ast.FuncLit); ok && n != ast.Node(body) {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, res := range ret.Results {
			id, ok := res.(*ast.Ident)
			if !ok || id.Name != "nil" {
				t.Errorf("run()'s graceful-shutdown closure returns a non-nil value (%T) — a shutdown that ran out of budget must not reach FatalLog/os.Exit(1); see doc/runbook/graceful-drain.md", res)
			}
		}
		return true
	})
}

func TestDrainerShutdownOnBudgetExceededReturnsNil(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(listener) }()

	go func() {
		resp, err := http.Get(fmt.Sprintf("http://%s/slow", listener.Addr().String()))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the request never reached the handler, so nothing was in flight and a nil return here would prove nothing")
	}

	drainer := lifecycle.NewDrainer()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	cut, shutdownErr := drainer.Shutdown(shutdownCtx, srv, func() int { return 1 })
	close(release)

	if shutdownErr != nil {
		t.Errorf("Shutdown returned %v; run() propagates whatever this is into errgroup, and a non-nil value there is os.Exit(1)", shutdownErr)
	}
	if cut != 1 {
		t.Errorf("cut = %d, want 1 — the budget expired with one request still in flight; a 0 here would mean the outcome was mis-classified as a clean shutdown", cut)
	}
	if !drainer.IsDraining() {
		t.Error("Shutdown did not mark the drainer draining, so the probes would have kept reporting ready for the whole window")
	}
}
