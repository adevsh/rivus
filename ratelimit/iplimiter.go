// Package ratelimit provides per-IP and per-backend token-bucket rate limiters
// for HTTP middleware. The per-IP limiter supports a trusted-proxy CIDR list
// for accurate client IP resolution behind load balancers.
package ratelimit

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adevsh/rivus/config"
)

// PerIPLimiterConfig aliases shared per-IP limiter config.
type PerIPLimiterConfig = config.PerIPLimiterConfig

type bucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64
	lastRefill time.Time
	lastSeen   time.Time
	mu         sync.Mutex
}

// IPLimiter enforces per-client-IP token-bucket limits.
type IPLimiter struct {
	buckets sync.Map
	cfg     PerIPLimiterConfig
	limited atomic.Int64
	trusted []*net.IPNet
}

// NewIPLimiter creates a per-IP limiter with shared settings.
// trusted is an optional list of CIDR networks whose reverse-proxies are trusted
// to set an accurate X-Forwarded-For chain; pass nil to always use RemoteAddr.
func NewIPLimiter(cfg PerIPLimiterConfig, trusted []*net.IPNet) *IPLimiter {
	if cfg.Burst <= 0 {
		cfg.Burst = 1
	}
	if cfg.RequestsPerSecond <= 0 {
		cfg.RequestsPerSecond = 1
	}
	return &IPLimiter{cfg: cfg, trusted: trusted}
}

// Allow returns true when the IP has at least one available token.
func (l *IPLimiter) Allow(ip string) bool {
	if strings.TrimSpace(ip) == "" {
		ip = "unknown"
	}

	value, _ := l.buckets.LoadOrStore(ip, &bucket{
		tokens:     float64(l.cfg.Burst),
		maxTokens:  float64(l.cfg.Burst),
		refillRate: l.cfg.RequestsPerSecond,
		lastRefill: time.Now(),
		lastSeen:   time.Now(),
	})
	b := value.(*bucket)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.lastRefill = now
	b.lastSeen = now

	b.tokens += elapsed * b.refillRate
	if b.tokens > b.maxTokens {
		b.tokens = b.maxTokens
	}
	if b.tokens < 1 {
		l.limited.Add(1)
		return false
	}

	b.tokens--
	return true
}

// TotalLimited returns the count of rejected requests.
func (l *IPLimiter) TotalLimited() int64 {
	return l.limited.Load()
}

// Cleanup periodically removes stale per-IP buckets.
func (l *IPLimiter) Cleanup(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-10 * time.Minute)
			l.buckets.Range(func(key, value any) bool {
				b, ok := value.(*bucket)
				if !ok {
					l.buckets.Delete(key)
					return true
				}

				b.mu.Lock()
				lastSeen := b.lastSeen
				b.mu.Unlock()

				if lastSeen.Before(cutoff) {
					l.buckets.Delete(key)
				}
				return true
			})
		}
	}
}

// Handler enforces per-IP limiting and forwards accepted requests.
func (l *IPLimiter) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := l.clientIP(r)
		if !l.Allow(ip) {
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the effective client IP. When the immediate peer (r.RemoteAddr)
// belongs to a configured trusted-proxy CIDR, XFF is walked right-to-left to find
// the first untrusted hop — that is the real client. With no trusted CIDRs,
// RemoteAddr is always used regardless of any X-Forwarded-For header.
func (l *IPLimiter) clientIP(r *http.Request) string {
	remote := splitHost(r.RemoteAddr)

	if len(l.trusted) == 0 || !ipInNets(remote, l.trusted) {
		return remote
	}

	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff == "" {
		return remote
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate != "" && !ipInNets(candidate, l.trusted) {
			return candidate
		}
	}
	return remote
}

func splitHost(addr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err == nil && host != "" {
		return host
	}
	if trimmed := strings.TrimSpace(addr); trimmed != "" {
		return trimmed
	}
	return "unknown"
}

func ipInNets(ip string, nets []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}
