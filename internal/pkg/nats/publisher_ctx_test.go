package nats

// publisher_ctx_test.go — cycle 12 L7 oracle for the NATS publisher.
//
// Publisher.Publish took a context and threw it away (the parameter was named
// `_`), so a caller's deadline bounded nothing: a broker that accepts the
// request and never acks pinned the calling goroutine for as long as the
// JetStream client felt like waiting. These tests pin the two halves of the
// fix — an in-flight publish stops at the caller's deadline, and an already
// expired context never reaches the wire at all.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
)

// blockingJS is a JetStreamContext whose Publish parks until released or until
// its own 3s ceiling — long enough to be indistinguishable from "hung" at the
// 200ms budget the tests below hand out, short enough that a regression fails
// the test instead of wedging the package for the whole 10m test timeout.
type blockingJS struct {
	natsgo.JetStreamContext // nil embed: satisfies the interface, panics if used

	release chan struct{}

	mu    sync.Mutex
	calls int
}

func (b *blockingJS) Publish(subj string, data []byte, _ ...natsgo.PubOpt) (*natsgo.PubAck, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-time.After(3 * time.Second):
	}
	return &natsgo.PubAck{Stream: "LLM_EVENTS", Sequence: 1}, nil
}

func (b *blockingJS) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// newBlockingJS returns a stub plus a cleanup that releases any parked publish
// goroutine, so the test binary does not carry one past this test.
func newBlockingJS(t *testing.T) *blockingJS {
	t.Helper()
	js := &blockingJS{release: make(chan struct{})}
	t.Cleanup(func() { close(js.release) })
	return js
}

func TestPublisher_Publish_HonoursCallerDeadline(t *testing.T) {
	js := newBlockingJS(t)
	p := newPublisher(js, "LLM_EVENTS")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := p.Publish(ctx, "llm.test.subject", map[string]int{"a": 1})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("publish against a broker that never acks must return an error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Publish with a 200ms context budget took %v (want <=500ms): the context "+
			"is not bounding the publish", elapsed)
	}
}

func TestPublisher_Publish_ExpiredContextNeverReachesTheWire(t *testing.T) {
	js := newBlockingJS(t)
	p := newPublisher(js, "LLM_EVENTS")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.Publish(ctx, "llm.test.subject", map[string]int{"a": 1})
	if err == nil {
		t.Fatal("publish with an already-cancelled context must return an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if n := js.callCount(); n != 0 {
		t.Errorf("js.Publish called %d times with a dead context, want 0", n)
	}
}

// TestPublishLLMEvent_SurvivesCallerCancellation guards the fire-and-forget
// contract of the typed helpers. Their callers hand in the HTTP request's
// context (internal/app/relay/image_handler.go:160), which is already cancelled
// whenever the client hung up — bounding the publish must not turn "the browser
// closed" into "the notification was dropped". publishLLMEvent therefore
// detaches from cancellation and keeps only a budget.
func TestPublishLLMEvent_SurvivesCallerCancellation(t *testing.T) {
	js := newBlockingJS(t)
	p := newPublisher(js, "LLM_EVENTS")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		publishLLMEvent(ctx, p, SubjectLLMImageGenerated, 7, LLMImageGeneratedPayload{JobID: "j1"}, "m")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishLLMEvent did not return")
	}
	if n := js.callCount(); n != 1 {
		t.Errorf("js.Publish called %d times, want 1: a cancelled caller context must not "+
			"drop a fire-and-forget event", n)
	}
}

// TestPublisherInitWiresReconnectObservability is a source-level check: the
// connection callbacks that drive the nats_connected gauge, and the unlimited
// reconnect policy, cannot be exercised without a live broker, so what a unit
// test can prove is that Init still asks for them. Before cycle 12 the client
// was built with MaxReconnects(5) and no callbacks at all — after five failed
// attempts the connection closed permanently and nothing recorded that it had.
// RetryOnFailedConnect is deliberately absent; see the comment on Connect.
func TestPublisherInitWiresReconnectObservability(t *testing.T) {
	raw, err := os.ReadFile("publisher.go")
	if err != nil {
		t.Fatalf("read publisher.go: %v", err)
	}
	src := string(raw)
	for _, want := range []string{
		"natsgo.MaxReconnects(-1)",
		"natsgo.DisconnectErrHandler(",
		"natsgo.ReconnectHandler(",
		"natsgo.ClosedHandler(",
		"metrics.SetNATSConnected(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("publisher.go no longer contains %q — the reconnect/observability wiring is gone", want)
		}
	}
}
