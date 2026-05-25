package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestIPLimiterAllow verifies per-IP pass/deny behavior for over-limit requests.
func TestIPLimiterAllow(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(PerIPLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	})

	const ip = "203.0.113.10"
	if !limiter.Allow(ip) {
		t.Fatalf("first request should be allowed")
	}
	if limiter.Allow(ip) {
		t.Fatalf("second immediate request should be denied")
	}
	if limiter.TotalLimited() != 1 {
		t.Fatalf("total limited = %d, want 1", limiter.TotalLimited())
	}

	time.Sleep(1100 * time.Millisecond)
	if !limiter.Allow(ip) {
		t.Fatalf("request should be allowed after refill")
	}
}

// TestIPLimiterHandler verifies middleware responses under and over the limit.
func TestIPLimiterHandler(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(PerIPLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := limiter.Handler(next)

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("X-Forwarded-For", "198.51.100.42, 10.0.0.1")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d", rec1.Code, http.StatusOK)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Forwarded-For", "198.51.100.42, 10.0.0.1")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want %d", rec2.Code, http.StatusTooManyRequests)
	}
}
