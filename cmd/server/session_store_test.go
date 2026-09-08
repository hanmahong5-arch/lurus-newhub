package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// TestParseRedisSessionTarget pins the REDIS_CONN_STRING -> (addr, password,
// db) parse used to build the session store. Before the SESS-DB fix, the
// db path segment was parsed off and discarded, so every deployment's
// browser sessions silently landed on Redis DB 0 regardless of what
// REDIS_CONN_STRING said (colliding with whatever else lived on DB 0, while
// channel cache / quota sync correctly followed the configured DB).
func TestParseRedisSessionTarget(t *testing.T) {
	cases := []struct {
		name         string
		url          string
		wantAddr     string
		wantPassword string
		wantDB       int
	}{
		{"host_port_with_db", "redis://h:6379/2", "h:6379", "", 2},
		{"password_host_port_no_db", "redis://p@h:6379", "h:6379", "p", 0},
		{"host_port_no_db_segment", "redis://redis.lurus-system.svc:6379", "redis.lurus-system.svc:6379", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, password, db := parseRedisSessionTarget(tc.url)
			if addr != tc.wantAddr {
				t.Errorf("addr = %q, want %q", addr, tc.wantAddr)
			}
			if password != tc.wantPassword {
				t.Errorf("password = %q, want %q", password, tc.wantPassword)
			}
			if db != tc.wantDB {
				t.Errorf("db = %d, want %d", db, tc.wantDB)
			}
		})
	}
}

// TestNewRedisSessionStore_UsesConfiguredDB is a real end-to-end proof (not
// just parseRedisSessionTarget's pure-function unit test above) that a
// session actually written through the store newRedisSessionStore builds
// lands on the Redis DB the redis:// URL specified — not DB 0. It runs an
// in-process miniredis, mounts a gin handler that sets a session value and
// saves it, fires one request, then inspects miniredis's per-DB key sets
// directly (bypassing the store entirely) so the assertion cannot be
// satisfied by the store simply echoing back its own config.
func TestNewRedisSessionStore_UsesConfiguredDB(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	store, addr, db, err := newRedisSessionStore("redis://"+mr.Addr()+"/2", []byte("test-secret"))
	if err != nil {
		t.Fatalf("newRedisSessionStore: %v", err)
	}
	if addr != mr.Addr() {
		t.Fatalf("resolved addr = %q, want %q", addr, mr.Addr())
	}
	if db != 2 {
		t.Fatalf("resolved db = %d, want 2", db)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", store))
	engine.GET("/set", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("probe", "value")
		if err := s.Save(); err != nil {
			c.String(http.StatusInternalServerError, "save: %v", err)
			return
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/set", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("request failed: %d body=%s", w.Code, w.Body.String())
	}

	if keys := mr.DB(2).Keys(); len(keys) == 0 {
		t.Errorf("expected the saved session to land on Redis DB 2 (the DB the redis:// URL specified), found no keys there")
	}
	if keys := mr.DB(0).Keys(); len(keys) != 0 {
		t.Errorf("expected Redis DB 0 to stay empty (session store must not silently default to DB 0), found keys: %v", keys)
	}
}
