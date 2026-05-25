package circuitbreaker

import "testing"

// TestBreakerStateTransitions verifies Closed -> Open -> HalfOpen -> Closed behavior.
func TestBreakerStateTransitions(t *testing.T) {
	t.Parallel()

	b := NewBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		CooldownSeconds:  0,
	})

	if b.State() != StateClosed {
		t.Fatalf("initial state = %q, want %q", b.State(), StateClosed)
	}
	if !b.Allow() {
		t.Fatalf("closed breaker should allow requests")
	}

	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("state after failure = %q, want %q", b.State(), StateOpen)
	}
	if b.Trips() != 1 {
		t.Fatalf("trips = %d, want 1", b.Trips())
	}

	if !b.Allow() {
		t.Fatalf("open breaker should allow a probe after cooldown")
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("state after probe allowance = %q, want %q", b.State(), StateHalfOpen)
	}
	if b.Allow() {
		t.Fatalf("half-open breaker must allow exactly one probe")
	}

	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Fatalf("state after probe success = %q, want %q", b.State(), StateClosed)
	}
}
