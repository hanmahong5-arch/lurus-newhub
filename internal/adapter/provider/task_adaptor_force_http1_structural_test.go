package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// taskAdaptorForceHTTP1BypassAllowlist lists provider/task/<vendor>/adaptor.go
// FetchTask implementations that are allowed to bypass app.GetHttpClientFor
// (cycle7 L5 finding item 1). It is empty today: every provider/task/*
// FetchTask was converted to call app.GetHttpClientFor in the L5 repair
// round so a channel's __lurus_force_http1 override also pins its task
// poll, not just its main relay call. A vendor whose FetchTask cannot make
// that call (e.g. it never resolves a *http.Client of its own) must be
// added here explicitly, with a comment saying why, instead of the bypass
// going unnoticed.
var taskAdaptorForceHTTP1BypassAllowlist = map[string]bool{}

// TestProviderTaskAdaptors_FetchTaskUsesGetHttpClientFor enumerates every
// provider/task/<vendor>/adaptor.go file that defines a TaskAdaptor
// FetchTask method and asserts the method's source calls
// app.GetHttpClientFor, unless the vendor is listed in
// taskAdaptorForceHTTP1BypassAllowlist. This is a source-text check, not a
// build/AST one, so it only catches the call disappearing from the method
// body — it exists so doc/product-integration-guide.md §G's "task FetchTask
// polls are covered" line cannot rot silently the way the pre-repair
// comment did (it claimed a bypass that this test would have caught).
func TestProviderTaskAdaptors_FetchTaskUsesGetHttpClientFor(t *testing.T) {
	entries, err := os.ReadDir("task")
	if err != nil {
		t.Fatalf("read provider/task dir: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		vendor := e.Name()
		path := filepath.Join("task", vendor, "adaptor.go")
		data, err := os.ReadFile(path)
		if err != nil {
			// Not every provider/task/<vendor> directory need have an
			// adaptor.go defining FetchTask; skip ones that don't.
			continue
		}
		src := string(data)
		const marker = "func (a *TaskAdaptor) FetchTask("
		idx := strings.Index(src, marker)
		if idx == -1 {
			continue
		}
		checked++

		// Isolate the FetchTask method body: from the marker to the next
		// top-level "\nfunc " (or EOF).
		rest := src[idx+len(marker):]
		body := rest
		if end := strings.Index(rest, "\nfunc "); end != -1 {
			body = rest[:end]
		}

		if taskAdaptorForceHTTP1BypassAllowlist[vendor] {
			continue
		}
		if !strings.Contains(body, "GetHttpClientFor") {
			t.Errorf("provider/task/%s/adaptor.go FetchTask does not call app.GetHttpClientFor and %q is not in taskAdaptorForceHTTP1BypassAllowlist", vendor, vendor)
		}
	}
	if checked == 0 {
		t.Fatal("no provider/task/*/adaptor.go FetchTask implementations were found; the enumeration itself is broken, not proving coverage")
	}
}
