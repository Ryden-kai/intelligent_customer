package llm

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCircuitBreaker_StaysClosedBelowThreshold(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 3, Cooldown: time.Second})
	for i := 0; i < 2; i++ {
		cb.RecordFailure()
	}
	if cb.State() != StateClosed {
		t.Fatalf("expected closed under threshold, got %s", cb.State())
	}
}

func TestCircuitBreaker_OpensAtThreshold(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 3, Cooldown: time.Second})
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	if cb.State() != StateOpen {
		t.Fatalf("expected open at threshold, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow=false in open state")
	}
}

func TestCircuitBreaker_HalfOpenAfterCooldown(t *testing.T) {
	now := time.Unix(0, 0)
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, Cooldown: 30 * time.Second})
	cb.SetClock(func() time.Time { return now })
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected open after single failure, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatal("Allow=false right after opening")
	}
	// Advance past cooldown.
	now = now.Add(31 * time.Second)
	if !cb.Allow() {
		t.Fatal("Allow=true after cooldown")
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected half_open after cooldown probe, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenSuccessCloses(t *testing.T) {
	now := time.Unix(0, 0)
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, Cooldown: time.Second})
	cb.SetClock(func() time.Time { return now })
	cb.RecordFailure()
	now = now.Add(2 * time.Second)
	_ = cb.Allow() // half_open
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Fatalf("expected closed after successful probe, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	now := time.Unix(0, 0)
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, Cooldown: time.Second})
	cb.SetClock(func() time.Time { return now })
	cb.RecordFailure()
	now = now.Add(2 * time.Second)
	_ = cb.Allow() // half_open
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected re-open after failed probe, got %s", cb.State())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 3, Cooldown: time.Second})
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateClosed {
		t.Fatalf("expected closed after success reset, got %s", cb.State())
	}
}

func TestCircuitBreaker_StateChangeHookFires(t *testing.T) {
	var transitions int32
	var mu sync.Mutex
	var lastFrom, lastTo CircuitState
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		Cooldown:         time.Second,
		OnStateChange: func(from, to CircuitState) {
			atomic.AddInt32(&transitions, 1)
			mu.Lock()
			lastFrom, lastTo = from, to
			mu.Unlock()
		},
	})
	cb.RecordFailure()
	mu.Lock()
	from, to := lastFrom, lastTo
	mu.Unlock()
	if from != StateClosed || to != StateOpen || atomic.LoadInt32(&transitions) != 1 {
		t.Fatalf("expected closed->open transition, got %s->%s (count=%d)", from, to, atomic.LoadInt32(&transitions))
	}
}

func TestCircuitBreaker_ConcurrentAllowIsSafe(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 100, Cooldown: time.Second})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = cb.Allow()
			}
		}()
	}
	wg.Wait()
	// Just exercising the race detector is the assertion — no panic = pass.
}

func TestCircuitBreaker_ForceOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 100, Cooldown: time.Hour})
	cb.ForceOpen()
	if cb.State() != StateOpen {
		t.Fatalf("expected open after ForceOpen, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatal("Allow=false while open")
	}
}
