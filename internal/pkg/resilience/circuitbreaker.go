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
	// FailureWindow bounds how long a failure stays "consecutive". When
	// positive, a Closed breaker whose previous failure is older than this
	// starts counting from one again, so a long-idle channel is not tripped by
	// a failure from hours ago plus a fresh one. Zero (the default) keeps the
	// historical behaviour: the run only ever resets on a success.
	FailureWindow time.Duration
	// Now is the clock. Nil means time.Now; tests inject a fake one.
	Now func() time.Time
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
// CB_HALF_OPEN_TIMEOUT_SEC (default 60), CB_FAILURE_WINDOW_SEC (default 0 = off).
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
	var failureWindow time.Duration
	if v := os.Getenv("CB_FAILURE_WINDOW_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			failureWindow = time.Duration(n) * time.Second
		}
	}
	return Config{
		Threshold:       threshold,
		Timeout:         timeout,
		HalfOpenTimeout: halfOpenTimeout,
		FailureWindow:   failureWindow,
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
	failureWindow   time.Duration
	// clock returns the current time; nil means time.Now.
	clock func() time.Time
}

func (b *breaker) now() time.Time {
	if b.clock == nil {
		return time.Now()
	}
	return b.clock()
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
		if b.now().Sub(b.lastFailTime) >= b.timeout {
			b.state = StateHalfOpen
			b.halfOpenSince = b.now()
			return true // Allow one probe request.
		}
		return false
	case StateHalfOpen:
		// The admitted probe has a deadline. Until it expires only one probe
		// is in flight, so every other caller is refused (the original rule).
		// Past it we must assume the probe will never report — nothing else
		// leaves HalfOpen — and admit a fresh one, otherwise this channel is
		// excluded from routing for the lifetime of the process.
		if b.now().Sub(b.halfOpenSince) >= b.halfOpenTimeout {
			b.halfOpenSince = b.now()
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
	now := b.now()
	if b.state == StateClosed && b.failureWindow > 0 && b.consecutiveFails > 0 &&
		now.Sub(b.lastFailTime) > b.failureWindow {
		// The previous failure aged out: this one starts a new run.
		b.consecutiveFails = 0
	}
	b.consecutiveFails++
	b.lastFailTime = now

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
		b.lastFailTime = b.now()
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
		failureWindow:   r.cfg.FailureWindow,
		clock:           r.cfg.Now,
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

// AllOpen reports whether EVERY listed channel's breaker is currently open,
// meaning Allow would refuse it right now, and if so how long until the
// soonest of them would admit a probe. It is a read-only view for callers that
// must decide whether a whole candidate set is dead before sending anything:
// it never creates a breaker and never advances a state machine, so it does not
// consume a probe slot (that happens only on Allow).
//
// A channel counts as open when its breaker is Open and still inside its
// cooldown, or HalfOpen with the in-flight probe still inside its own bound.
// A channel with no breaker yet, a Closed one, or one whose cooldown/probe
// bound has already elapsed (Allow would admit it) counts as NOT open. An empty
// list is not "all open". The duration is the minimum remaining wait across the
// listed channels, and 0 whenever the bool is false.
func (r *Registry) AllOpen(ids []int) (bool, time.Duration) {
	if len(ids) == 0 {
		return false, 0
	}
	r.mu.RLock()
	brs := make([]*breaker, len(ids))
	for i, id := range ids {
		b, ok := r.breakers[id]
		if !ok {
			r.mu.RUnlock()
			return false, 0
		}
		brs[i] = b
	}
	r.mu.RUnlock()

	var soonest time.Duration
	for i, b := range brs {
		wait, open := b.admitsIn()
		if !open {
			return false, 0
		}
		if i == 0 || wait < soonest {
			soonest = wait
		}
	}
	return true, soonest
}

// admitsIn reports (time until allow() would admit a probe, true) when the
// breaker is currently refusing, and (0, false) when allow() would admit now.
// Side-effect free.
func (b *breaker) admitsIn() (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var wait time.Duration
	switch b.state {
	case StateOpen:
		wait = b.timeout - b.now().Sub(b.lastFailTime)
	case StateHalfOpen:
		wait = b.halfOpenTimeout - b.now().Sub(b.halfOpenSince)
	default:
		return 0, false
	}
	if wait <= 0 {
		return 0, false
	}
	return wait, true
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
