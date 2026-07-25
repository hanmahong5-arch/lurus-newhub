// Package resilience provides fault-tolerance primitives for upstream service calls.
package resilience

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota // Normal operation — requests pass through.
	StateOpen                  // Tripped — requests rejected immediately.
	StateHalfOpen              // Probing — one request allowed to test recovery.
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Config holds circuit breaker tuning parameters.
type Config struct {
	// Threshold is the number of consecutive failures that trips the breaker.
	Threshold int
	// Timeout is the duration the breaker stays Open before transitioning to HalfOpen.
	Timeout time.Duration
	// OnStateChange is called on every state transition (for metrics/logging).
	OnStateChange func(channelID int, from, to State)
}

// DefaultConfig returns production defaults.
// Override via env: CB_THRESHOLD (default 5), CB_TIMEOUT_SEC (default 30).
func DefaultConfig() Config {
	threshold := 5
	if v := os.Getenv("CB_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			threshold = n
		}
	}
	timeout := 30 * time.Second
	if v := os.Getenv("CB_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	return Config{
		Threshold: threshold,
		Timeout:   timeout,
	}
}

// breaker is the per-channel state machine.
type breaker struct {
	mu               sync.Mutex
	state            State
	consecutiveFails int
	lastFailTime     time.Time
	threshold        int
	timeout          time.Duration
}

// allow checks whether a request should be permitted.
// Returns true if the request can proceed.
func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		// Check if timeout expired → transition to HalfOpen.
		if time.Since(b.lastFailTime) >= b.timeout {
			b.state = StateHalfOpen
			return true // Allow one probe request.
		}
		return false
	case StateHalfOpen:
		// Only one probe in flight — reject concurrent requests while probing.
		return false
	default:
		return true
	}
}

// recordSuccess resets the breaker to Closed.
// Returns (previousState, newState) for metrics reporting.
func (b *breaker) recordSuccess() (State, State) {
	b.mu.Lock()
	defer b.mu.Unlock()

	prev := b.state
	b.consecutiveFails = 0
	b.state = StateClosed
	return prev, StateClosed
}

// recordFailure increments the failure counter and trips the breaker if threshold is reached.
// Returns (previousState, newState) for metrics reporting.
func (b *breaker) recordFailure() (State, State) {
	b.mu.Lock()
	defer b.mu.Unlock()

	prev := b.state
	b.consecutiveFails++
	b.lastFailTime = time.Now()

	switch b.state {
	case StateClosed:
		if b.consecutiveFails >= b.threshold {
			b.state = StateOpen
		}
	case StateHalfOpen:
		// Probe failed — back to Open.
		b.state = StateOpen
	case StateOpen:
		// Already open, just update lastFailTime (extends timeout).
	}
	return prev, b.state
}

func (b *breaker) getState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Registry manages per-channel circuit breakers.
type Registry struct {
	mu       sync.RWMutex
	breakers map[int]*breaker
	cfg      Config
}

// NewRegistry creates a Registry with the given config.
func NewRegistry(cfg Config) *Registry {
	return &Registry{
		breakers: make(map[int]*breaker),
		cfg:      cfg,
	}
}

func (r *Registry) getOrCreate(channelID int) *breaker {
	r.mu.RLock()
	b, ok := r.breakers[channelID]
	r.mu.RUnlock()
	if ok {
		return b
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after write lock.
	if b, ok = r.breakers[channelID]; ok {
		return b
	}
	b = &breaker{
		threshold: r.cfg.Threshold,
		timeout:   r.cfg.Timeout,
	}
	r.breakers[channelID] = b
	return b
}

// Allow returns true if the channel's breaker permits a request.
func (r *Registry) Allow(channelID int) bool {
	return r.getOrCreate(channelID).allow()
}

// RecordSuccess records a successful request, resetting the breaker to Closed.
func (r *Registry) RecordSuccess(channelID int) {
	b := r.getOrCreate(channelID)
	prev, curr := b.recordSuccess()
	if prev != curr && r.cfg.OnStateChange != nil {
		r.cfg.OnStateChange(channelID, prev, curr)
	}
}

// RecordFailure records a failed request, potentially tripping the breaker.
func (r *Registry) RecordFailure(channelID int) {
	b := r.getOrCreate(channelID)
	prev, curr := b.recordFailure()
	if prev != curr && r.cfg.OnStateChange != nil {
		r.cfg.OnStateChange(channelID, prev, curr)
	}
}

// GetState returns the current state of a channel's breaker.
func (r *Registry) GetState(channelID int) State {
	return r.getOrCreate(channelID).getState()
}

// Cleanup removes breakers for channels that no longer exist.
// Call periodically to prevent unbounded memory growth.
func (r *Registry) Cleanup(activeChannelIDs map[int]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.breakers {
		if _, exists := activeChannelIDs[id]; !exists {
			delete(r.breakers, id)
		}
	}
}

// BreakerSnapshot is a read-only view of one channel's breaker, for operator
// surfaces (admin API / console) that need to answer "which upstreams is the
// gateway currently refusing to send traffic to, and why".
type BreakerSnapshot struct {
	ChannelID int    `json:"channel_id"`
	State     string `json:"state"`
	// ConsecutiveFails is the current run of failures; it resets on success.
	ConsecutiveFails int `json:"consecutive_fails"`
	// Threshold is the run length that trips the breaker, so a reader can show
	// "3/5" without knowing the server's configuration.
	Threshold int `json:"threshold"`
	// LastFailUnix is 0 when the breaker has never recorded a failure.
	LastFailUnix int64 `json:"last_fail_unix"`
	// ProbeEligibleUnix is when an Open breaker becomes eligible for its
	// half-open probe. NOTE the state field reports the LAST RECORDED state:
	// an Open breaker past this instant has not transitioned yet — the switch
	// to HalfOpen happens on the next admission check, because performing it
	// here would consume the single probe slot for a mere status read.
	// Zero unless State is "open".
	ProbeEligibleUnix int64 `json:"probe_eligible_unix,omitempty"`
}

// Snapshot returns the current state of every known breaker. Side-effect free:
// it never creates a breaker and never advances a state machine, so polling it
// cannot change routing behaviour.
func (r *Registry) Snapshot() []BreakerSnapshot {
	r.mu.RLock()
	ids := make([]int, 0, len(r.breakers))
	brs := make([]*breaker, 0, len(r.breakers))
	for id, b := range r.breakers {
		ids = append(ids, id)
		brs = append(brs, b)
	}
	r.mu.RUnlock()

	out := make([]BreakerSnapshot, 0, len(ids))
	for i, b := range brs {
		b.mu.Lock()
		snap := BreakerSnapshot{
			ChannelID:        ids[i],
			State:            b.state.String(),
			ConsecutiveFails: b.consecutiveFails,
			Threshold:        b.threshold,
		}
		if !b.lastFailTime.IsZero() {
			snap.LastFailUnix = b.lastFailTime.Unix()
			if b.state == StateOpen {
				snap.ProbeEligibleUnix = b.lastFailTime.Add(b.timeout).Unix()
			}
		}
		b.mu.Unlock()
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out
}
