package middleware

// r6b_access_log_wiring_test.go — the接线锁 D3 asked for: r5b_access_log_test.go
// builds its own gin.Engine and calls SetUpLogger directly, which is a
// hand-copy of cmd/server/main.go's real registration and cannot observe
// whether production still wires things in the order the whole format branch
// depends on. Rather than hand-copy cmd/server/main.go's engine construction
// a second time (importing package main from this package's _test.go is not
// possible, and adding a test file under cmd/server was outside the file list
// this change was scoped to — not a language or repo restriction), this reads
// the actual cmd/server/main.go source and asserts on it directly — the same
// "enumerate what's really registered" approach the router package already
// uses for its own wiring locks (see
// internal/adapter/handler/router/security_headers_wiring_test.go).
//
// What this proves: (a) cmd/server/main.go's run() still calls
// middleware.SetUpLogger on the real *gin.Engine, and (b)
// common.InitSlog(common.SlogConfigFromEnv()) in InitResources appears, in
// source order, before that registration. main() calls InitResources(ctx) as
// the first statement of run(), then continues in the same function with no
// intervening goroutine before the engine block, so source order here is
// execution order — that is what closes the "首次 InitSlog 之前恒 false" gap:
// SetUpLogger snapshots common.IsJSONLogFormat() at registration time, so the
// format decision must already have been made when it runs.
//
// Correction 2026-08-27: an earlier draft also asserted that
// engine.Use(middleware.RequestId()) must appear before SetUpLogger, on the
// stated grounds that jsonAccessLogger needs RequestIdKey in the request
// context. That rationale is false and the assertion was removed. Measured
// with a throwaway probe (SetUpLogger registered FIRST, RequestId second):
//
//	{"time":...,"msg":"http_request",...,"request_id":"20260827094728209910300wpnmIuNv"}
//
// request_id is still present, because jsonAccessLogger reads
// c.Request.Context() AFTER c.Next(), and RequestId (request-id.go) writes its
// new context back onto the same *http.Request via c.Request =
// c.Request.WithContext(ctx). Registration order between those two is not
// load-bearing, and asserting it would have sent the next person debugging a
// failure down a path that does not exist.
//
// What this does NOT prove: it does not exercise runtime behavior (record
// shape, key collisions) — that is r5b_access_log_test.go's job — and it does
// not prove InitResources itself always reaches the InitSlog line (a return
// before it would skip engine setup entirely too, so the ordering guarantee
// this test cares about is not affected by early returns, only by a caller
// putting SetUpLogger before InitSlog in that shared source order). Nor does
// it prove the registered middleware is ever reached at runtime: a build that
// kept the call but wrapped it in a disabled branch would still pass. It also
// cannot detect a build that swaps in a different cmd/server/main.go at deploy
// time; it only guards the copy of that file living in this repo.
//
// Mutation proof: commenting out `middleware.SetUpLogger(engine)` in
// cmd/server/main.go, or moving `common.InitSlog(common.SlogConfigFromEnv())`
// to after the SetUpLogger call, makes this test fail.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mainGoSource reads the repo's real cmd/server/main.go relative to this
// test file's own location, so it tracks the file wherever the module is
// checked out rather than assuming a fixed absolute path.
func mainGoSource(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed — cannot locate this test file to find cmd/server/main.go relative to it")
	}
	// this file: internal/adapter/middleware/r6b_access_log_wiring_test.go
	// repo root: three levels up (middleware -> adapter -> internal -> root)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	mainGoPath := filepath.Join(repoRoot, "cmd", "server", "main.go")
	data, err := os.ReadFile(mainGoPath)
	if err != nil {
		t.Fatalf("reading %s: %v", mainGoPath, err)
	}
	return string(data)
}

func TestMainGo_AccessLoggerWiredAfterInitSlog(t *testing.T) {
	src := mainGoSource(t)

	// Go doesn't require function bodies to appear in call order, so this
	// deliberately does NOT compare the text offset of InitResources' func
	// body against run()'s engine setup — InitResources is defined further
	// down the file than run() even though run() calls it first. Instead it
	// compares the CALL SITE of InitResources(ctx) (which sits inside run(),
	// sequentially before the engine.Use/SetUpLogger lines in that same
	// function) — for statements within one function with no intervening
	// goroutine, source order is execution order.
	const initResourcesCall = "InitResources(ctx)"
	const requestIdCall = "engine.Use(middleware.RequestId())"
	const setUpLoggerCall = "middleware.SetUpLogger(engine)"

	initResourcesIdx := strings.Index(src, initResourcesCall)
	if initResourcesIdx < 0 {
		t.Fatalf("cmd/server/main.go's run() no longer calls %q — the boot sequence this access logger depends on has moved or been removed; update this lock alongside it", initResourcesCall)
	}

	// RequestId's presence is still worth pinning (its absence would drop
	// request_id from every access-log record), but NOT its position relative
	// to SetUpLogger — see the ordering correction in this file's header.
	if !strings.Contains(src, requestIdCall) {
		t.Fatalf("cmd/server/main.go no longer contains %q — RequestId is no longer registered on the real engine, so http_request records lose their request_id correlation key", requestIdCall)
	}

	setUpLoggerIdx := strings.Index(src, setUpLoggerCall)
	if setUpLoggerIdx < 0 {
		t.Fatalf("cmd/server/main.go no longer contains %q — middleware.SetUpLogger is no longer wired onto the real engine; production requests would fall back to gin's unstructured default logger", setUpLoggerCall)
	}

	if initResourcesIdx >= setUpLoggerIdx {
		t.Fatalf("wiring order regression: run()'s InitResources(ctx) call (offset %d) must appear before middleware.SetUpLogger(engine) (offset %d) in cmd/server/main.go — SetUpLogger's format branch is decided at registration time by whatever InitSlog InitResources last applied, and that must have already run", initResourcesIdx, setUpLoggerIdx)
	}

	// Now confirm InitResources' own body actually performs the format
	// decision this whole thing depends on — checked as existence within
	// that function's body (not compared by absolute file offset, since the
	// function is defined after run() in this file).
	const initResourcesFuncSig = "func InitResources(ctx context.Context) error {"
	funcIdx := strings.Index(src, initResourcesFuncSig)
	if funcIdx < 0 {
		t.Fatalf("cmd/server/main.go no longer defines %q — cannot verify its body calls common.InitSlog", initResourcesFuncSig)
	}
	body := src[funcIdx:]
	if end := strings.Index(body, "\nfunc "); end >= 0 {
		body = body[:end]
	}
	const initSlogCall = "common.InitSlog(common.SlogConfigFromEnv())"
	if !strings.Contains(body, initSlogCall) {
		t.Fatalf("cmd/server/main.go's InitResources no longer calls %q — the boot-time format decision SetUpLogger depends on has moved or been removed", initSlogCall)
	}
}
