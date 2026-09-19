package config

// server_timeouts_test.go — cycle 12 L7.
//
// cmd/server/main.go built its http.Server with only Addr and Handler, so the
// two bounds that protect a Go HTTP server from a client that opens a socket
// and then goes slow or silent — ReadHeaderTimeout and IdleTimeout — were both
// zero, i.e. unlimited. These are the config values the wiring reads.
//
// ReadTimeout and WriteTimeout stay absent on purpose: they would cut SSE
// relay streams and large uploads mid-flight. That asymmetry is the whole point
// of adding only these two, so it is pinned here as well.

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// hasField reports whether v's struct type declares a field of the given name.
func hasField(v any, name string) bool {
	_, ok := reflect.TypeOf(v).FieldByName(name)
	return ok
}

func TestGet_ServerDefaults_ReadHeaderTimeout(t *testing.T) {
	if v, ok := os.LookupEnv("HTTP_READ_HEADER_TIMEOUT"); ok {
		t.Skipf("HTTP_READ_HEADER_TIMEOUT is set to %q in the ambient env", v)
	}
	if got := Get().Server.ReadHeaderTimeout; got != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %s, want 10s", got)
	}
}

func TestGet_ServerDefaults_IdleTimeout(t *testing.T) {
	if v, ok := os.LookupEnv("HTTP_IDLE_TIMEOUT"); ok {
		t.Skipf("HTTP_IDLE_TIMEOUT is set to %q in the ambient env", v)
	}
	if got := Get().Server.IdleTimeout; got != 120*time.Second {
		t.Errorf("IdleTimeout = %s, want 120s", got)
	}
}

// TestServerConfig_HasNoWholeRequestTimeouts is the do-not-regress half: adding
// a ReadTimeout/WriteTimeout field here would be the first step toward wiring
// one into the server, which truncates streaming responses.
func TestServerConfig_HasNoWholeRequestTimeouts(t *testing.T) {
	cfg := ServerConfig{}
	// A compile-time check would be ideal but Go has no "field must not exist"
	// assertion; reflect gives the same guarantee at test time.
	for _, banned := range []string{"ReadTimeout", "WriteTimeout"} {
		if hasField(cfg, banned) {
			t.Errorf("ServerConfig grew a %s field: a whole-request timeout on this server "+
				"cuts SSE relay streams and large uploads (cycle 12 do-not-regress list)", banned)
		}
	}
}
