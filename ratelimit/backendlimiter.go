package ratelimit

import (
	"sync"
	"time"

	"github.com/adevsh/rivus/config"
)

// PerBackendLimiterConfig aliases shared per-backend limiter config.
type PerBackendLimiterConfig = config.PerBackendLimiterConfig

// BackendLimiter enforces a token-bucket request rate for one backend.
type BackendLimiter struct {
	tokens     float64
	maxTokens  float64
	refillRate float64
	lastRefill time.Time
	mu         sync.Mutex
}

// NewBackendLimiter creates a backend limiter instance.
func NewBackendLimiter(cfg PerBackendLimiterConfig) *BackendLimiter {
	now := time.Now()
	maxTokens := float64(cfg.Burst)
	if maxTokens <= 0 {
		maxTokens = 1
	}
	refillRate := cfg.RequestsPerSecond
	if refillRate <= 0 {
		refillRate = 1
	}
	return &BackendLimiter{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: now,
	}
}

// Allow reports whether one request is allowed for this backend.
func (l *BackendLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	l.lastRefill = now

	l.tokens += elapsed * l.refillRate
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
	if l.tokens < 1 {
		return false
	}

	l.tokens--
	return true
}
