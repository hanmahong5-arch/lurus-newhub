package routingdecision

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	pkgcommon "github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

func TestC23_DefaultIndexWithoutDatabaseYieldsNoPolicy(t *testing.T) {
	prev := repo.DB
	repo.DB = nil
	t.Cleanup(func() { repo.DB = prev; Invalidate() })
	Invalidate()

	if p := Lookup("tenant-a", "public-model"); p != nil {
		t.Fatalf("no DB means no policies, got %+v", p)
	}
	// Empty tenant or model short-circuits before touching the snapshot.
	if p := Lookup("", "public-model"); p != nil {
		t.Errorf("empty tenant must not match, got %+v", p)
	}
	if p := Lookup("tenant-a", ""); p != nil {
		t.Errorf("empty model must not match, got %+v", p)
	}
	// Invalidate is idempotent and leaves the index usable.
	Invalidate()
	Invalidate()
	if p := Lookup("tenant-a", "public-model"); p != nil {
		t.Errorf("lookup after invalidate must still be empty, got %+v", p)
	}
}

func TestC23_ValidationErrorCarriesMessageAndCode(t *testing.T) {
	var err error = invalid("bad_thing", "value %q is %d too long", "x", 3)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("invalid() must return a *ValidationError, got %T", err)
	}
	if ve.Code != "bad_thing" {
		t.Errorf("code = %q", ve.Code)
	}
	if err.Error() != `value "x" is 3 too long` || ve.Message != err.Error() {
		t.Errorf("message not formatted: %q / %q", err.Error(), ve.Message)
	}
}

func TestC23_CaptureWriterCollectsWhatDoResponseWrites(t *testing.T) {
	w := &captureWriter{h: http.Header{}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	n, err := w.Write([]byte("hello "))
	if err != nil || n != 6 {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	_, _ = w.Write([]byte("world"))
	if w.code != 202 {
		t.Errorf("status = %d", w.code)
	}
	if got := w.body.String(); got != "hello world" {
		t.Errorf("body = %q", got)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Error("headers must be retained on the capture writer")
	}
}

func TestC23_ScratchContextCopiesOnlyTheGroupKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	caller, _ := gin.CreateTestContext(httptest.NewRecorder())
	pkgcommon.SetContextKey(caller, constant.ContextKeyUserGroup, "vip")
	pkgcommon.SetContextKey(caller, constant.ContextKeyUsingGroup, "team-a")
	caller.Set("some_request_scoped_key", "must-not-leak")

	type ctxKey struct{}
	sc, w := scratchContext(contextWith(ctxKey{}, "v"), caller)
	if w == nil || sc == nil {
		t.Fatal("scratchContext must return a context and a writer")
	}
	if g, _ := pkgcommon.GetContextKey(sc, constant.ContextKeyUserGroup); g != "vip" {
		t.Errorf("user group not carried over: %v", g)
	}
	if g, _ := pkgcommon.GetContextKey(sc, constant.ContextKeyUsingGroup); g != "team-a" {
		t.Errorf("using group not carried over: %v", g)
	}
	if _, leaked := sc.Get("some_request_scoped_key"); leaked {
		t.Error("per-request keys of the caller must not reach the scratch context")
	}
	if sc.Request == nil || sc.Request.Method != http.MethodPost || !strings.HasSuffix(sc.Request.URL.Path, "/v1/systemone") {
		t.Errorf("scratch request wrong: %+v", sc.Request)
	}
	if sc.Request.Context().Value(ctxKey{}) != "v" {
		t.Error("the scratch request must inherit the caller's context for cancellation")
	}

	// A caller without group keys yields a scratch context without them.
	bare, _ := gin.CreateTestContext(httptest.NewRecorder())
	sc2, _ := scratchContext(contextWith(ctxKey{}, "v"), bare)
	if _, has := pkgcommon.GetContextKey(sc2, constant.ContextKeyUsingGroup); has {
		t.Error("missing caller keys must not be invented")
	}
}

func contextWith(k, v any) context.Context {
	return context.WithValue(context.Background(), k, v)
}
