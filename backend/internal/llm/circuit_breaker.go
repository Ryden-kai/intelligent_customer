// Package llm — circuit_breaker.go
//
// Per-channel circuit breaker used by the Router. State machine:
//
//	closed   --(failures >= threshold)--> open
//	open     --(now > openUntil)        --> half_open
//	half_open --(probe succeeds)         --> closed
//	half_open --(probe fails)            --> open
//
// Allow() returns true in closed + half_open. The half_open probe is a
// single attempt — only the next call after the cooldown gets to probe;
// concurrent calls during half_open are allowed through (they form the
// probe batch in practice, but the state machine is simplified to one
// transition per cooldown to keep tests deterministic).
//
// The breaker is goroutine-safe and intentionally has zero external deps
// so it can be reused for non-LLM resources (DB pools, Jev, etc.) in
// future iterations.

package llm

import (
	"sync"
	"time"
)

// CircuitState is the public state enum.
type CircuitState string

const (
	StateClosed   CircuitState = "closed"
	StateOpen     CircuitState = "open"
	StateHalfOpen CircuitState = "half_open"
)

// CircuitBreakerConfig controls the breaker behaviour.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures that flips
	// closed → open. Set to math.MaxInt to effectively disable.
	FailureThreshold int
	// Cooldown is how long the breaker stays open before allowing a
	// probe (half_open).
	Cooldown time.Duration
	// OnStateChange is called whenever the state flips. Optional.
	OnStateChange func(from, to CircuitState)
}

// CircuitBreaker is a thread-safe circuit breaker for a single resource.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            CircuitState
	failures         int
	openedAt         time.Time
	threshold        int
	cooldown         time.Duration
	onStateChange    func(from, to CircuitState)
	now              func() time.Time // injectable for tests
}

// NewCircuitBreaker builds a breaker with the given config. Threshold ≤
// 0 is clamped to 1 so a single failure opens the circuit (useful for
// tests). Negative cooldown → 1 second default.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 1
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = time.Second
	}
	return &CircuitBreaker{
		state:         StateClosed,
		threshold:     cfg.FailureThreshold,
		cooldown:      cfg.Cooldown,
		onStateChange: cfg.OnStateChange,
		now:           time.Now,
	}
}

// Allow reports whether the next call should proceed. In open state it
// also performs the cooldown → half_open transition.
func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateClosed, StateHalfOpen:
		return true
	case StateOpen:
		if b.now().Sub(b.openedAt) >= b.cooldown {
			b.transition(StateHalfOpen)
			return true
		}
		return false
	default:
		return false
	}
}

// RecordSuccess resets the failure counter and, if half_open, closes
// the breaker. Safe to call when the breaker is already closed.
func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	if b.state == StateHalfOpen {
		b.transition(StateClosed)
	}
}

// RecordFailure increments the failure counter and flips state when
// the threshold is reached. In half_open, any failure re-opens.
func (b *CircuitBreaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.state == StateHalfOpen {
		b.openedAt = b.now()
		b.transition(StateOpen)
		return
	}
	if b.state == StateClosed && b.failures >= b.threshold {
		b.openedAt = b.now()
		b.transition(StateOpen)
	}
}

// State returns the current state (mostly for observability + tests).
func (b *CircuitBreaker) State() CircuitState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Snapshot is a struct copy useful for observability + tests.
type Snapshot struct {
	State    CircuitState
	Failures int
	OpenedAt time.Time
}

// Snapshot returns a struct copy of the breaker's state.
func (b *CircuitBreaker) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Snapshot{
		State:    b.state,
		Failures: b.failures,
		OpenedAt: b.openedAt,
	}
}

// transition is the single mutation point — keeps the OnStateChange hook
// firing under the lock so observers see a consistent state.
func (b *CircuitBreaker) transition(to CircuitState) {
	if b.state == to {
		return
	}
	from := b.state
	b.state = to
	cb := b.onStateChange
	if cb != nil {
		cb(from, to)
	}
}

// ForceOpen is a test helper to put the breaker straight into open
// without waiting for failures to accumulate. Not exposed in production
// callers — only used by *_test.go.
func (b *CircuitBreaker) ForceOpen() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.openedAt = b.now()
	b.transition(StateOpen)
}

// SetClock replaces the internal clock; intended for tests so they can
// advance time deterministically without sleeping.
func (b *CircuitBreaker) SetClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
}
