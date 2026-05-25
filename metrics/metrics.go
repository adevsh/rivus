// Package metrics collects gateway counters (uptime, request totals, active
// connections, per-backend health, circuit-breaker state, rate-limiter drops)
// and serves them as a JSON snapshot at the /metrics endpoint.
package metrics

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/adevsh/rivus/ratelimit"
	"github.com/adevsh/rivus/upstream"
)

// BackendMetrics captures per-backend runtime counters and health state.
type BackendMetrics struct {
	URL                 string `json:"url"`
	Healthy             bool   `json:"healthy"`
	ActiveConns         int64  `json:"active_conns"`
	TotalRequests       int64  `json:"total_requests"`
	TotalErrors         int64  `json:"total_errors"`
	CircuitBreakerState string `json:"circuit_breaker_state"`
	CircuitBreakerTrips int64  `json:"circuit_breaker_trips"`
}

// UpstreamMetrics aggregates backend metrics for one upstream.
type UpstreamMetrics struct {
	Backends []BackendMetrics `json:"backends"`
}

// RateLimiterMetrics captures global rate limiter counters.
type RateLimiterMetrics struct {
	TotalLimitedRequests int64 `json:"total_limited_requests"`
}

// Snapshot is the complete metrics payload served at the metrics endpoint.
type Snapshot struct {
	UptimeSeconds     int64                      `json:"uptime_seconds"`
	TotalRequests     int64                      `json:"total_requests"`
	ActiveConnections int64                      `json:"active_connections"`
	Upstreams         map[string]UpstreamMetrics `json:"upstreams"`
	RateLimiter       RateLimiterMetrics         `json:"rate_limiter"`
}

// Collector aggregates counters from upstream backends and entrypoint middleware.
type Collector struct {
	upstreams     map[string]*upstream.Upstream
	ipLimiter     *ratelimit.IPLimiter
	startTime     time.Time
	totalRequests atomic.Int64
	activeConns   atomic.Int64
}

// NewCollector creates a metrics collector with upstream and limiter references.
func NewCollector(upstreams map[string]*upstream.Upstream, ipLimiter *ratelimit.IPLimiter) *Collector {
	return &Collector{
		upstreams: upstreams,
		ipLimiter: ipLimiter,
		startTime: time.Now(),
	}
}

// IncrRequest increments the total request counter.
func (c *Collector) IncrRequest() {
	c.totalRequests.Add(1)
}

// IncrConn increments the active connection counter.
func (c *Collector) IncrConn() {
	c.activeConns.Add(1)
}

// DecrConn decrements the active connection counter.
func (c *Collector) DecrConn() {
	c.activeConns.Add(-1)
}

// Snapshot reads all counters and assembles one consistent metrics view.
func (c *Collector) Snapshot() Snapshot {
	upstreams := make(map[string]UpstreamMetrics, len(c.upstreams))
	for name, up := range c.upstreams {
		if up == nil {
			continue
		}

		backends := up.Backends()
		bms := make([]BackendMetrics, 0, len(backends))
		for _, b := range backends {
			if b == nil {
				continue
			}

			bm := BackendMetrics{
				URL:           b.URL.String(),
				Healthy:       b.Healthy.Load(),
				ActiveConns:   b.ActiveConns.Load(),
				TotalRequests: b.TotalRequests.Load(),
				TotalErrors:   b.TotalErrors.Load(),
			}
			if b.Breaker != nil {
				bm.CircuitBreakerState = string(b.Breaker.State())
				bm.CircuitBreakerTrips = b.Breaker.Trips()
			}
			bms = append(bms, bm)
		}
		upstreams[name] = UpstreamMetrics{Backends: bms}
	}

	var limited int64
	if c.ipLimiter != nil {
		limited = c.ipLimiter.TotalLimited()
	}

	return Snapshot{
		UptimeSeconds:     int64(time.Since(c.startTime).Seconds()),
		TotalRequests:     c.totalRequests.Load(),
		ActiveConnections: c.activeConns.Load(),
		Upstreams:         upstreams,
		RateLimiter: RateLimiterMetrics{
			TotalLimitedRequests: limited,
		},
	}
}

// Handler returns an HTTP handler that serves the current metrics snapshot as JSON.
func (c *Collector) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data, err := json.Marshal(c.Snapshot())
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}
