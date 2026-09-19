package common

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// hungRedisAddr starts a TCP listener that ACCEPTS connections and then never
// writes a byte back. That is the shape of the outage this file is about: the
// socket is up (so dialling succeeds and no connection error is raised), but no
// reply ever arrives. A client that does not bound the read — or that ignores
// the caller's context deadline — sits there until its own read timeout fires,
// which is what made every Redis-touching request cost 3s during the 2026-09-19
// audit (plan §1.2 item 9).
//
// Accepted connections are parked in a slice so the kernel does not reset them,
// and closed on cleanup.
func hungRedisAddr(t *testing.T) string {
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

// TestRedisOptions_CallerDeadlineBoundsCommand is the L7 Redis oracle: a command
// issued with 200ms of context budget against a server that accepts but never
// answers must come back inside that budget, not inside go-redis' own read
// timeout. go-redis only consults the context deadline when
// ContextTimeoutEnabled is set, so before this lane the caller's ctx was
// decoration and every command paid the full read timeout.
func TestRedisOptions_CallerDeadlineBoundsCommand(t *testing.T) {
	addr := hungRedisAddr(t)
	t.Setenv("REDIS_CONN_STRING", "redis://"+addr+"/0")

	opt := ParseRedisOption()
	if opt == nil {
		t.Fatal("ParseRedisOption returned nil")
	}
	client := redis.NewClient(opt)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.Get(ctx, "l7-deadline-probe").Result()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a server that never answers must not produce a successful GET")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("GET with a 200ms context budget took %v (want <=500ms): the caller's "+
			"deadline is not bounding the Redis command, err=%v", elapsed, err)
	}
}

// TestApplyRedisTimeouts_Defaults pins the numbers cycle 12 §2 decided on, so a
// later edit that drops one of them fails here instead of on a wedged replica.
func TestApplyRedisTimeouts_Defaults(t *testing.T) {
	for _, k := range []string{"REDIS_OP_TIMEOUT_MS", "REDIS_DIAL_TIMEOUT_MS"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Skipf("%s is set to %q in the ambient env; default case not applicable", k, v)
		}
	}
	opt := &redis.Options{}
	applyRedisTimeouts(opt)

	if opt.ReadTimeout != time.Second {
		t.Errorf("ReadTimeout = %v, want 1s", opt.ReadTimeout)
	}
	if opt.WriteTimeout != time.Second {
		t.Errorf("WriteTimeout = %v, want 1s", opt.WriteTimeout)
	}
	if opt.DialTimeout != 2*time.Second {
		t.Errorf("DialTimeout = %v, want 2s", opt.DialTimeout)
	}
	if opt.PoolTimeout != 2*time.Second {
		t.Errorf("PoolTimeout = %v, want read timeout + 1s = 2s", opt.PoolTimeout)
	}
	if opt.MaxRetries != 1 {
		t.Errorf("MaxRetries = %d, want 1", opt.MaxRetries)
	}
	if !opt.ContextTimeoutEnabled {
		t.Error("ContextTimeoutEnabled = false: caller deadlines would be ignored for every command")
	}
}

// TestApplyRedisTimeouts_EnvOverrides covers the two operator knobs.
func TestApplyRedisTimeouts_EnvOverrides(t *testing.T) {
	t.Setenv("REDIS_OP_TIMEOUT_MS", "250")
	t.Setenv("REDIS_DIAL_TIMEOUT_MS", "400")

	opt := &redis.Options{}
	applyRedisTimeouts(opt)

	if opt.ReadTimeout != 250*time.Millisecond || opt.WriteTimeout != 250*time.Millisecond {
		t.Errorf("read/write = %v/%v, want 250ms both", opt.ReadTimeout, opt.WriteTimeout)
	}
	if opt.DialTimeout != 400*time.Millisecond {
		t.Errorf("DialTimeout = %v, want 400ms", opt.DialTimeout)
	}
	if opt.PoolTimeout != 1250*time.Millisecond {
		t.Errorf("PoolTimeout = %v, want 1.25s", opt.PoolTimeout)
	}
}

// TestRedisOptionBuildersShareOneSource guards the seam this file's timing
// oracle relies on. The oracle drives ParseRedisOption, but the client the
// process actually serves traffic with is the one InitRedisClient builds; if
// the two stopped sharing buildRedisOptions, the oracle would keep passing
// while production went back to unbounded commands. Textual check on this one
// file — the two function bodies are a dozen lines apart.
func TestRedisOptionBuildersShareOneSource(t *testing.T) {
	src, err := os.ReadFile("redis.go")
	if err != nil {
		t.Fatalf("read redis.go: %v", err)
	}
	// Line endings normalised: a Windows checkout of this repo produces CRLF
	// working copies for text-attributed sources, and the needle below spans a
	// line break.
	body := strings.ReplaceAll(string(src), "\r\n", "\n")
	for _, fn := range []string{"func InitRedisClient() (err error) {", "func ParseRedisOption() *redis.Options {"} {
		i := strings.Index(body, fn)
		if i < 0 {
			t.Fatalf("could not find %q in redis.go — this check is measuring nothing", fn)
		}
		rest := body[i:]
		end := strings.Index(rest, "\n}\n")
		if end < 0 {
			t.Fatalf("could not find the end of %q", fn)
		}
		if !strings.Contains(rest[:end], "buildRedisOptions(") {
			t.Errorf("%s does not go through buildRedisOptions(): the bounded timeout set "+
				"would apply to only one of the two construction paths", fn)
		}
	}
}
