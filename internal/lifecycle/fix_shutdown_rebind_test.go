package lifecycle

// fix_shutdown_rebind_test.go — 修掉 graceful_shutdown_test.go 里的
// listen → Close → 按地址重新 bind 写法：端口在这段空窗里可能被别人占走，
// 或者重新 bind 慢于固定的 50ms sleep，测试就会对着一个根本没起来的服务断言
// （“no requests were handled” / “expected DeadlineExceeded, got: <nil>”）。
// 这里提供两个共用辅助函数：一直持有 listener，并以真实可连通性代替 sleep。

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

// fixShutdownListen 绑定一个回环端口并在整个测试期间持有该 listener。
func fixShutdownListen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on an ephemeral port: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

// fixShutdownWaitReady 轮询到端口真的可以建连为止，取代固定 sleep。
// 只做 TCP 建连，不发 HTTP 请求，避免影响被测的请求计数。
func fixShutdownWaitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server at %s never accepted a connection: %v", addr, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestFixShutdownRebindWindowIsUnsafe 证明旧写法的前提不成立：端口被 Close 之后
// 到重新 bind 之前是可以被别人抢走的，此时 ListenAndServe 内部的
// net.Listen(addr) 会直接失败，服务根本没起来，而旧测试把这个错误丢弃了。
func TestFixShutdownRebindWindowIsUnsafe(t *testing.T) {
	t.Parallel()

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	// 模拟空窗期里别的进程抢占了同一个端口
	squatter, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("port %s could not be re-bound, takeover not reproducible here: %v", addr, err)
	}
	defer func() { _ = squatter.Close() }()

	// ListenAndServe 就是这么 bind 的：端口被占用时它只会返回错误
	if again, err := net.Listen("tcp", addr); err == nil {
		_ = again.Close()
		t.Error("re-binding a port that another listener already holds should fail")
	}
}

// TestFixShutdownServeOnHeldListener 锁定修复后的写法：始终使用已经持有的
// listener 启动服务，请求必然被处理，Shutdown 之后 Serve 以 ErrServerClosed 返回。
func TestFixShutdownServeOnHeldListener(t *testing.T) {
	t.Parallel()

	listener := fixShutdownListen(t)
	addr := listener.Addr().String()

	handled := make(chan struct{}, 1)
	server := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case handled <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusOK)
		}),
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	fixShutdownWaitReady(t, addr)

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("request never reached the handler")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve returned %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("serve loop did not stop after shutdown")
	}
}
