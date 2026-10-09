// Command fakeupstream runs the fake LLM vendor (internal/testkit/fakeupstream)
// and the fake platform wallet (internal/testkit/fakeplatform) on one port,
// for the local acceptance stack (scripts/acceptance-stack.sh).
//
// Point a channel's base_url at http://<addr> and IDENTITY_SERVICE_URL at the
// same address: the platform API lives under /internal/, the vendor wires
// everywhere else, the controls under /_fake/.
//
// It is a test fixture. It has no TLS, keeps everything in memory and must
// never be reachable from outside the machine running the stack — the default
// address is loopback.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/LurusTech/lurus-hub/internal/testkit/fakeplatform"
	"github.com/LurusTech/lurus-hub/internal/testkit/fakeupstream"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18090", "listen address")
	key := flag.String("key", "", "API key the vendor routes require (empty = any)")
	platformKey := flag.String("platform-key", "", "internal key the platform routes require (empty = any)")
	prompt := flag.Int("prompt-tokens", fakeupstream.DefaultUsage.PromptTokens, "prompt tokens reported per answer")
	completion := flag.Int("completion-tokens", fakeupstream.DefaultUsage.CompletionTokens, "completion tokens reported per answer")
	cached := flag.Int("cached-tokens", 0, "cached prompt tokens reported per answer (part of prompt-tokens)")
	flag.Parse()

	vendor := fakeupstream.New(fakeupstream.Config{
		Key:   *key,
		Usage: fakeupstream.Usage{PromptTokens: *prompt, CompletionTokens: *completion, CachedTokens: *cached},
	})
	platform := fakeplatform.New(*platformKey)
	vendorH, platformH := vendor.Handler(), platform.Handler()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") || strings.HasPrefix(r.URL.Path, "/_fake/platform/") {
			platformH.ServeHTTP(w, r)
			return
		}
		vendorH.ServeHTTP(w, r)
	})

	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("fakeupstream listening on %s (vendor + platform)", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
