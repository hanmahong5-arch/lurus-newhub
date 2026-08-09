package openai

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// deadTTSStreamTarget is the helper that used to sit in relay-openai.go with no
// caller anywhere in the module: the TTS response is written by OpenaiTTSHandler
// (audio.go), so this streaming copy loop was unreachable at runtime.
const deadTTSStreamTarget = "streamTTSResponse"

// deadTTSStreamScanSymbol reports how many times the symbol is declared as a
// function and how many times it is referenced from production (non-test) files
// of this package. The declaration's own name identifier is not counted as a
// reference.
func deadTTSStreamScanSymbol(t *testing.T, symbol string) (decls int, refs int) {
	t.Helper()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package files: %v", err)
	}

	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, file, nil, 0)
		if parseErr != nil {
			// A file may be unparsable while it is being edited; it cannot
			// contain a compiled reference in that state anyway.
			t.Logf("skipping unparsable file %s: %v", file, parseErr)
			continue
		}

		declNames := make(map[*ast.Ident]bool)
		ast.Inspect(parsed, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if ok && fn.Name != nil && fn.Name.Name == symbol {
				decls++
				declNames[fn.Name] = true
			}
			return true
		})

		ast.Inspect(parsed, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && id.Name == symbol && !declNames[id] {
				refs++
			}
			return true
		})
	}

	return decls, refs
}

// TestFixDeadTTSStream_NoUnreachableStreamTTSHelper locks in the removal of the
// unreachable streaming-TTS helper: it may only exist in this package if some
// production code actually calls it.
func TestFixDeadTTSStream_NoUnreachableStreamTTSHelper(t *testing.T) {
	decls, refs := deadTTSStreamScanSymbol(t, deadTTSStreamTarget)

	if decls > 0 && refs == 0 {
		t.Fatalf("%s is declared %d time(s) in package openai but never referenced by production code; "+
			"either wire it into the TTS response path or delete it", deadTTSStreamTarget, decls)
	}
}

// TestFixDeadTTSStream_ScannerDetectsLiveSymbol guards the scanner itself: a
// symbol that is declared and called must be reported as referenced, so the
// check above cannot pass vacuously.
func TestFixDeadTTSStream_ScannerDetectsLiveSymbol(t *testing.T) {
	decls, refs := deadTTSStreamScanSymbol(t, "OpenaiTTSHandler")

	if decls == 0 {
		t.Fatalf("expected OpenaiTTSHandler to be declared in package openai, got %d declarations", decls)
	}
	if refs == 0 {
		t.Fatal("expected OpenaiTTSHandler to be referenced by production code, got 0 references")
	}
}
