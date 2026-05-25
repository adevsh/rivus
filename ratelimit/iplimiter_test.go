package ratelimit

import (
	"net"
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
	}, nil)

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
// With no trusted proxies, the limiter keys on RemoteAddr regardless of XFF.
func TestIPLimiterHandler(t *testing.T) {
	t.Parallel()

	limiter := NewIPLimiter(PerIPLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	}, nil)

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

// TestIPLimiterClientIPTrustedProxy verifies that the effective client IP is
// resolved from X-Forwarded-For only when RemoteAddr is inside a trusted CIDR.
func TestIPLimiterClientIPTrustedProxy(t *testing.T) {
	t.Parallel()

	_, trusted, _ := net.ParseCIDR("10.0.0.0/8")
	limiter := NewIPLimiter(PerIPLimiterConfig{RequestsPerSecond: 1, Burst: 1}, []*net.IPNet{trusted})

	// Peer is trusted → real client is the rightmost untrusted XFF entry.
	reqTrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	reqTrusted.RemoteAddr = "10.1.2.3:9999"
	reqTrusted.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := limiter.clientIP(reqTrusted); got != "1.2.3.4" {
		t.Errorf("trusted peer: clientIP() = %q, want %q", got, "1.2.3.4")
	}

	// Peer is untrusted → XFF is ignored, RemoteAddr is used directly.
	reqUntrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	reqUntrusted.RemoteAddr = "5.6.7.8:9999"
	reqUntrusted.Header.Set("X-Forwarded-For", "9.9.9.9")
	if got := limiter.clientIP(reqUntrusted); got != "5.6.7.8" {
		t.Errorf("untrusted peer: clientIP() = %q, want %q", got, "5.6.7.8")
	}

	// No trusted proxies configured → always use RemoteAddr.
	noTrustLimiter := NewIPLimiter(PerIPLimiterConfig{RequestsPerSecond: 1, Burst: 1}, nil)
	reqNoTrust := httptest.NewRequest(http.MethodGet, "/", nil)
	reqNoTrust.RemoteAddr = "5.6.7.8:9999"
	reqNoTrust.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := noTrustLimiter.clientIP(reqNoTrust); got != "5.6.7.8" {
		t.Errorf("no trust: clientIP() = %q, want %q", got, "5.6.7.8")
	}
}
