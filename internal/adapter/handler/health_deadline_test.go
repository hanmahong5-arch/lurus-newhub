package handler

// health_deadline_test.go — cycle 12 L7 oracle for /api/health.
//
// The Redis check in health.go wraps its ping in a 2s context. That number was
// aspirational: go-redis ignores the caller's context deadline unless
// ContextTimeoutEnabled is set, so a Redis that accepts the connection and then
// stops answering made the probe pay go-redis' own read timeout (3s measured)
// instead of the 2s the code asks for. readinessProbe timeoutSeconds is 2 on
// both manifests, so the probe was being cut off by kubelet before the handler
// could answer — a hung cache read as a rolling restart of every replica.
//
// This test drives the real construction path (common.ParseRedisOption, the
// same option builder InitRedisClient uses) against a listener that accepts and
// never replies, and pins three things: the handler returns inside the 2s it
// promises, it labels Redis "unreachable", and it still answers 200 — Redis is
// a soft dependency, only the database moves the status code.

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// hungTCPAddr starts a listener that accepts connections and never writes to
// them, parking the accepted conns so the kernel does not reset them.
func hungTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				close(done)
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})
	return ln.Addr().String()
}

func TestGetHealthDetailed_HungRedisStaysInsideProbeBudget(t *testing.T) {
	addr := hungTCPAddr(t)
	t.Setenv("REDIS_CONN_STRING", "redis://"+addr+"/0")

	client := redis.NewClient(common.ParseRedisOption())
	prevRDB, prevEnabled, prevDB := common.RDB, common.RedisEnabled, repo.DB
	common.RDB = client
	common.RedisEnabled = true
	repo.DB = nil
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled, repo.DB = prevRDB, prevEnabled, prevDB
		_ = client.Close()
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/health", nil)

	start := time.Now()
	GetHealthDetailed(c)
	elapsed := time.Since(start)

	if elapsed > 2500*time.Millisecond {
		t.Errorf("GetHealthDetailed took %v against a hung Redis; the handler's own 2s "+
			"context is not bounding the ping", elapsed)
	}

	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v raw=%s", err, w.Body.String())
	}
	if got := body.Checks["redis"]; got != "unreachable" {
		t.Errorf("checks.redis = %q, want \"unreachable\"", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200: Redis is a soft dependency and must not fail readiness", w.Code)
	}
	if body.Status != "degraded" {
		t.Errorf("status word = %q, want \"degraded\"", body.Status)
	}
}
