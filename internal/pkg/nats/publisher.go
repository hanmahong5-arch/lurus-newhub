// Package nats provides a minimal NATS JetStream publisher used by newhub
// to emit quota threshold events to the LLM_EVENTS stream.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
)

const (
	// SubjectQuotaThreshold is the NATS subject for quota threshold events.
	SubjectQuotaThreshold = "llm.quota.threshold"

	defaultStream = "LLM_EVENTS"
)

// Publisher publishes events to a NATS JetStream stream.
type Publisher struct {
	js     natsgo.JetStreamContext
	stream string
}

var (
	global     *Publisher
	globalOnce sync.Once
	globalErr  error
)

// Enabled reports whether the quota NATS publisher is enabled.
// Reads LLM_QUOTA_NATS_ENABLED env var; defaults to false.
func Enabled() bool {
	v := os.Getenv("LLM_QUOTA_NATS_ENABLED")
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}
	return b
}

// streamName returns the configured stream name (LLM_QUOTA_NATS_STREAM),
// falling back to the default.
func streamName() string {
	if s := os.Getenv("LLM_QUOTA_NATS_STREAM"); s != "" {
		return s
	}
	return defaultStream
}

// natsURL returns the NATS server URL from env.
func natsURL() string {
	if u := os.Getenv("NATS_URL"); u != "" {
		return u
	}
	return natsgo.DefaultURL
}

// Init initialises the global publisher once. Safe to call multiple times.
// Returns nil if NATS is disabled.
func Init() error {
	if !Enabled() {
		return nil
	}
	globalOnce.Do(func() {
		// MaxReconnects(-1): unlimited. The previous value of 5 meant a broker
		// restart that outlasted five attempts closed the connection for the
		// lifetime of the pod, and nothing recorded that it had — every later
		// publish just failed. The three callbacks are what make the state
		// observable at all.
		//
		// Deliberately NOT RetryOnFailedConnect: with it, a Connect against a
		// down broker returns a usable-looking conn in reconnecting state and
		// this function returns nil, which contradicts the contract
		// publisher_init_test.go pins — Init must surface the failure and Get()
		// must stay nil so callers can tell "no publisher" from "publisher that
		// silently drops". The cost is that a broker down at BOOT leaves this
		// process with no publisher until it restarts; a broker that drops after
		// boot is covered by the unlimited reconnect above.
		nc, err := natsgo.Connect(natsURL(),
			natsgo.Name("lurus-newhub"),
			natsgo.Timeout(5*time.Second),
			natsgo.MaxReconnects(-1),
			natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, derr error) {
				metrics.SetNATSConnected(false)
				slog.Warn("nats disconnected", "err", derr)
			}),
			natsgo.ReconnectHandler(func(c *natsgo.Conn) {
				metrics.SetNATSConnected(true)
				slog.Info("nats reconnected", "url", c.ConnectedUrl())
			}),
			natsgo.ClosedHandler(func(_ *natsgo.Conn) {
				metrics.SetNATSConnected(false)
				slog.Warn("nats connection closed")
			}),
		)
		if err != nil {
			metrics.SetNATSConnected(false)
			globalErr = fmt.Errorf("nats connect %s: %w", natsURL(), err)
			return
		}
		js, err := nc.JetStream()
		if err != nil {
			nc.Close()
			metrics.SetNATSConnected(false)
			globalErr = fmt.Errorf("nats jetstream: %w", err)
			return
		}
		metrics.SetNATSConnected(nc.IsConnected())
		global = &Publisher{js: js, stream: streamName()}
	})
	return globalErr
}

// Get returns the global publisher. Returns nil when disabled or not yet
// initialised successfully.
func Get() *Publisher {
	return global
}

// newPublisher creates a publisher from an existing JetStreamContext.
// Used by tests to inject mock connections.
func newPublisher(js natsgo.JetStreamContext, stream string) *Publisher {
	return &Publisher{js: js, stream: stream}
}

// Publish serialises payload as JSON and publishes it to the stream, bounded by
// ctx. The ctx used to be discarded (the parameter was named `_`), so a broker
// that accepted the request and never acked pinned the calling goroutine for as
// long as the JetStream client felt like waiting; callers that had budgeted
// 200ms got minutes.
//
// ctx is handed to the client as a PubOpt (nats.Context) so the real client
// aborts the ack wait itself, AND raced against here, because an injected or
// future JetStream implementation is under no obligation to honour the opt. The
// goroutine is why: with a buffered result channel it can never block, and in
// production the nats.Context opt makes the call underneath it return at the
// same deadline, so it does not outlive the budget. Callers that want the
// publish to survive their own cancellation (fire-and-forget event helpers)
// detach before calling — see publishLLMEvent in events.go.
//
// ctx must be non-nil AND must carry a deadline. Without one, nats.go takes the
// RequestMsgWithContext branch with its ack TTL left at zero, i.e. an unbounded
// ack wait, and the select below has nothing to fire on either — the bound would
// be gone in both halves. All three call sites comply today: events.go
// (eventPublishBudget 5s), quota_threshold.go and pool_threshold.go (5s and 2s,
// handed in from internal/app/quota.go).
//
// Consumer note: a publish reported as failed here may still have landed on the
// broker. When ctx fires first the goroutine's result is discarded, so the
// error describes this process's patience, not the broker's outcome. The
// subjects are designed for that — consumers key on the event's own identity,
// so a redelivery is idempotent — but a counter fed from this error
// (CreditPoolAlertHookErrorTotal via pool_threshold.go) can over-count against a
// slow-but-healthy broker.
func (p *Publisher) Publish(ctx context.Context, subject string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("publish %s: %w", subject, cerr)
	}
	done := make(chan error, 1)
	go func() {
		_, perr := p.js.Publish(subject, data, natsgo.Context(ctx))
		done <- perr
	}()
	select {
	case perr := <-done:
		if perr != nil {
			return fmt.Errorf("publish %s: %w", subject, perr)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("publish %s: %w", subject, ctx.Err())
	}
}
