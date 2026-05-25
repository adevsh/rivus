// Package circuitbreaker implements a three-state (closed → open → half-open)
// per-backend circuit breaker that limits request admission based on configurable
// failure thresholds and a cooldown period.
package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/adevsh/rivus/config"
)

// CircuitBreakerConfig aliases shared circuit breaker config fields.
type CircuitBreakerConfig = config.CircuitBreakerConfig

// State represents the current circuit breaker state.
type State string

const (
	// StateClosed allows all requests while failures are below threshold.
	StateClosed State = "closed"
	// StateOpen rejects requests until cooldown elapses.
	StateOpen State = "open"
	// StateHalfOpen allows exactly one probe request.
	StateHalfOpen State = "half_open"
)

// Breaker tracks failures and controls request admission for a backend.
type Breaker struct {
	state         State
	failures      int
	lastTrip      time.Time
	mu            sync.Mutex
	cfg           CircuitBreakerConfig
	trips         atomic.Int64
	probeInFlight bool
}

// NewBreaker creates a new breaker initialized in closed state.
func NewBreaker(cfg CircuitBreakerConfig) *Breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 1
	}
	return &Breaker{
		state: StateClosed,
		cfg:   cfg,
	}
}

// Allow reports whether the current request may pass through the breaker.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		if b.cfg.CooldownSeconds <= 0 || time.Since(b.lastTrip) >= time.Duration(b.cfg.CooldownSeconds)*time.Second {
			b.state = StateHalfOpen
			b.probeInFlight = true
			return true
		}
		return false
	case StateHalfOpen:
		if b.probeInFlight {
			return false
		}
		b.probeInFlight = true
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful request and closes half-open circuits.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures = 0
	if b.state == StateHalfOpen {
		b.state = StateClosed
		b.probeInFlight = false
	}
}

// RecordFailure records a failed request and opens circuit when threshold is reached.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures++
	thresholdReached := b.failures >= b.cfg.FailureThreshold
	if b.state == StateHalfOpen || thresholdReached {
		b.state = StateOpen
		b.lastTrip = time.Now()
		b.failures = 0
		b.probeInFlight = false
		b.trips.Add(1)
	}
}

// State returns the current breaker state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Trips returns how many times the breaker has opened.
func (b *Breaker) Trips() int64 {
	return b.trips.Load()
}
