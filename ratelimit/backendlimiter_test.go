package ratelimit

import (
	"testing"
	"time"
)

// TestBackendLimiterAllow verifies pass/deny behavior around the token budget.
func TestBackendLimiterAllow(t *testing.T) {
	t.Parallel()

	limiter := NewBackendLimiter(PerBackendLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	})

	if !limiter.Allow() {
		t.Fatalf("first request should be allowed")
	}
	if limiter.Allow() {
		t.Fatalf("second immediate request should be denied")
	}

	time.Sleep(1100 * time.Millisecond)
	if !limiter.Allow() {
		t.Fatalf("request should be allowed after refill")
	}
}
