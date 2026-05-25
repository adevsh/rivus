// Package backend holds the runtime state for one upstream target: its URL,
// health flag, active-connection counter, circuit breaker, and per-backend
// rate limiter.
package backend

import (
	"fmt"
	"net/url"
	"sync/atomic"

	"github.com/adevsh/rivus/circuitbreaker"
	"github.com/adevsh/rivus/config"
	"github.com/adevsh/rivus/ratelimit"
)

// BackendConfig aliases shared backend URL config fields.
type BackendConfig = config.BackendConfig

// CircuitBreakerConfig aliases shared circuit breaker config fields.
type CircuitBreakerConfig = config.CircuitBreakerConfig

// PerBackendLimiterConfig aliases shared per-backend rate limiter config fields.
type PerBackendLimiterConfig = config.PerBackendLimiterConfig

// Backend stores runtime state and controls request admission for one target URL.
type Backend struct {
	URL           *url.URL
	Healthy       atomic.Bool
	ActiveConns   atomic.Int64
	TotalRequests atomic.Int64
	TotalErrors   atomic.Int64
	Breaker       *circuitbreaker.Breaker
	RateLimiter   *ratelimit.BackendLimiter
}

// New builds a runtime backend from config and optional feature configs.
func New(cfg BackendConfig, cbCfg *CircuitBreakerConfig, rlCfg *PerBackendLimiterConfig) (*Backend, error) {
	parsedURL, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse backend url: %w", err)
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("backend url must include scheme and host: %q", cfg.URL)
	}

	b := &Backend{
		URL: parsedURL,
	}
	b.Healthy.Store(true)

	if cbCfg != nil {
		b.Breaker = circuitbreaker.NewBreaker(*cbCfg)
	}
	if rlCfg != nil {
		b.RateLimiter = ratelimit.NewBackendLimiter(*rlCfg)
	}

	return b, nil
}

// IsAvailable reports whether backend is healthy and passes optional feature gates.
func (b *Backend) IsAvailable() bool {
	if !b.Healthy.Load() {
		return false
	}
	if b.Breaker != nil && !b.Breaker.Allow() {
		return false
	}
	if b.RateLimiter != nil && !b.RateLimiter.Allow() {
		return false
	}
	return true
}

// IncrConn increments active connection and total request counters.
func (b *Backend) IncrConn() {
	b.ActiveConns.Add(1)
	b.TotalRequests.Add(1)
}

// DecrConn decrements the active connection counter.
func (b *Backend) DecrConn() {
	b.ActiveConns.Add(-1)
}

// RecordError increments error counters and notifies the breaker.
func (b *Backend) RecordError() {
	b.TotalErrors.Add(1)
	if b.Breaker != nil {
		b.Breaker.RecordFailure()
	}
}

// RecordSuccess notifies the breaker about a successful request.
func (b *Backend) RecordSuccess() {
	if b.Breaker != nil {
		b.Breaker.RecordSuccess()
	}
}
