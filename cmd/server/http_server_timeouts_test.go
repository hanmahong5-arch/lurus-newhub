package main

// http_server_timeouts_test.go — cycle-12 W wiring oracle for L7's server
// bounds. The config package's own test proves HTTP_READ_HEADER_TIMEOUT /
// HTTP_IDLE_TIMEOUT parse; this file proves the values actually reach the
// http.Server the process listens on, which is the only place they do anything.
//
// Mutation: dropping either field from buildHTTPServer turns
// TestBuildHTTPServer_BoundsTheSlowPhases red; adding a ReadTimeout or
// WriteTimeout turns TestBuildHTTPServer_LeavesWholeRequestTimeoutsUnset red
// (those two would cut SSE relay streams — cycle 12 do-not-regress list).

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBuildHTTPServer_BoundsTheSlowPhases(t *testing.T) {
	srv := buildHTTPServer("3000", nil)

	if srv.Addr != ":3000" {
		t.Errorf("Addr = %q, want \":3000\"", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want > 0 — a zero value lets a peer hold a connection open by never finishing its header block", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want > 0 — a zero value means idle keep-alive connections are never reaped", srv.IdleTimeout)
	}
}

func TestBuildHTTPServer_LeavesWholeRequestTimeoutsUnset(t *testing.T) {
	srv := buildHTTPServer("3000", nil)

	if srv.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %v, want 0: it bounds a whole request, which would cut long uploads mid-flight", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0: it bounds a whole response, which would cut SSE relay streams mid-flight", srv.WriteTimeout)
	}
}

func TestBuildHTTPServer_CarriesTheHandlerThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	srv := buildHTTPServer("8850", engine)

	if srv.Handler != http.Handler(engine) {
		t.Errorf("Handler = %v, want the engine passed in — a server that bounds timeouts but serves the wrong handler is worse than no change", srv.Handler)
	}
}
