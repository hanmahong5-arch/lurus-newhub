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
	// HalfOpenTimeout bounds how long HalfOpen may last without an outcome.
	// HalfOpen admits exactly one probe and then refuses every caller until
	// that probe reports (recordSuccess / recordFailure / recordInconclusive),
	// so a probe that never reports at all — a panicking caller, a code path
	// that forgot to report — used to exclude the channel from routing until
	// the process restarted. Past this bound allow() re-arms and admits a
	// fresh probe, which caps the damage at one lost probe window instead of
	// forever. Zero falls back to Timeout (see getOrCreate).
	HalfOpenTimeout time.Duration
	// OnStateChange is called on every state transition (for metrics/logging).
	OnStateChange func(channelID int, from, to State)
}

// defaultHalfOpenTimeout is the fallback probe lifetime when neither
// HalfOpenTimeout nor Timeout is configured. Long enough that a real relay
// request admitted as the probe normally reports before it expires, short
// enough that a probe which never reports cannot hide a channel for long.
const defaultHalfOpenTimeout = 60 * time.Second

// DefaultConfig returns production defaults.
// Override via env: CB_THRESHOLD (default 5), CB_TIMEOUT_SEC (default 30),
// CB_HALF_OPEN_TIMEOUT_SEC (default 60).
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
	halfOpenTimeout := defaultHalfOpenTimeout
	if v := os.Getenv("CB_HALF_OPEN_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			halfOpenTimeout = time.Duration(n) * time.Second
		}
	}
	return Config{
		Threshold:       threshold,
		Timeout:         timeout,
		HalfOpenTimeout: halfOpenTimeout,
	}
}

// breaker is the per-channel state machine.
type breaker struct {
	mu               sync.Mutex
	state            State
	consecutiveFails int
	lastFailTime     time.Time
	// halfOpenSince is when the current HalfOpen probe was admitted. Only
	// meaningful while state == StateHalfOpen; cleared on every exit from it.
	halfOpenSince   time.Time
	threshold       int
	timeout         time.Duration
	halfOpenTimeout time.Duration
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
			b.halfOpenSince = time.Now()
			return true // Allow one probe request.
		}
		return false
	case StateHalfOpen:
		// The admitted probe has a deadline. Until it expires only one probe
		// is in flight, so every other caller is refused (the original rule).
		// Past it we must assume the probe will never report — nothing else
		// leaves HalfOpen — and admit a fresh one, otherwise this channel is
		// excluded from routing for the lifetime of the process.
		if time.Since(b.halfOpenSince) >= b.halfOpenTimeout {
			b.halfOpenSince = time.Now()
			return true
		}
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
	b.halfOpenSince = time.Time{}
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
		b.halfOpenSince = time.Time{}
	case StateOpen:
		// Already open, just update lastFailTime (extends timeout).
	}
	return prev, b.state
}

// recordInconclusive reports that an admitted request finished without saying
// anything about the upstream's health — a caller-side 4xx, a client
// cancellation, a request the gateway itself refused to send. It releases the
// probe slot WITHOUT counting a failure: the channel must not be tripped by
// its users' own bad requests, but it must not stay hidden either.
//
// HalfOpen → Open, restarting the cooldown from now, so the next probe is one
// timeout away rather than immediate (an unhealthy upstream must not be
// hammered by a client that keeps sending it 4xx-producing requests).
// Closed and Open are no-ops: nothing was learned and no slot was held.
// Returns (previousState, newState) for metrics reporting.
func (b *breaker) recordInconclusive() (State, State) {
	b.mu.Lock()
	defer b.mu.Unlock()

	prev := b.state
	if b.state == StateHalfOpen {
		b.state = StateOpen
		b.halfOpenSince = time.Time{}
		b.lastFailTime = time.Now()
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
	halfOpenTimeout := r.cfg.HalfOpenTimeout
	if halfOpenTimeout <= 0 {
		// A Config built by hand (tests, embedders) carries no probe bound.
		// Fall back to the Open cooldown, which the caller did choose, and
		// only to the package default when that is unset too — never to 0,
		// which would make HalfOpen admit every caller.
		halfOpenTimeout = r.cfg.Timeout
		if halfOpenTimeout <= 0 {
			halfOpenTimeout = defaultHalfOpenTimeout
		}
	}
	b = &breaker{
		threshold:       r.cfg.Threshold,
		timeout:         r.cfg.Timeout,
		halfOpenTimeout: halfOpenTimeout,
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

// RecordInconclusive records that an admitted request ended without evidence
// about the upstream — a user 4xx, a client cancellation, a request the
// gateway refused to send. Callers MUST report one of RecordSuccess /
// RecordFailure / RecordInconclusive for every request Allow admitted:
// in HalfOpen the admission consumed the single probe slot, and only a report
// gives it back.
func (r *Registry) RecordInconclusive(channelID int) {
	b := r.getOrCreate(channelID)
	prev, curr := b.recordInconclusive()
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
	// ProbeEligibleUnix is when this breaker becomes eligible for a (further)
	// half-open probe: for "open", when the cooldown ends; for "half_open",
	// when the in-flight probe's own deadline expires and a fresh probe is
	// admitted in its place. NOTE the state field reports the LAST RECORDED
	// state: a breaker past this instant has not transitioned yet — the
	// switch happens on the next admission check, because performing it here
	// would consume the probe slot for a mere status read. Zero while closed.
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
		if b.state == StateHalfOpen && !b.halfOpenSince.IsZero() {
			snap.ProbeEligibleUnix = b.halfOpenSince.Add(b.halfOpenTimeout).Unix()
		}
		b.mu.Unlock()
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChannelID < out[j].ChannelID })
	return out
}
