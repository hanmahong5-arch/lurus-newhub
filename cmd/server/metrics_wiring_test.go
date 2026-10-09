package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The content-rule metrics sink and the channel gauge loop are only reachable
// through run(); deleting either call leaves every unit test green while the
// series go dark. This reads main.go's real AST (like drain_wiring_test.go).
func TestRunInstallsChannelAndContentMetrics(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var installSink, channelLoop bool
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "handler" && sel.Sel.Name == "InstallContentMetricsSink" {
				installSink = true
			}
		}
		for _, a := range call.Args {
			if sel, ok := a.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "handler" && sel.Sel.Name == "RunChannelMetricsLoop" {
					channelLoop = true
				}
			}
		}
		return true
	})
	if !installSink {
		t.Error("main.go no longer calls handler.InstallContentMetricsSink: lurus_content_* would stay at zero")
	}
	if !channelLoop {
		t.Error("main.go no longer starts handler.RunChannelMetricsLoop: channel gauges would go dark")
	}
}
