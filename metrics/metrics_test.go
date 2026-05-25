package metrics

import (
	"testing"

	"github.com/adevsh/rivus/config"
	"github.com/adevsh/rivus/upstream"
)

// TestCollectorSnapshot verifies aggregated counters and backend metrics fields.
func TestCollectorSnapshot(t *testing.T) {
	t.Parallel()

	up, err := upstream.New(
		"api",
		config.UpstreamConfig{
			Prefix:   "/api",
			Balancer: config.BalancerRoundRobin,
			Backends: []config.BackendConfig{
				{URL: "http://127.0.0.1:9501"},
			},
		},
		config.FeatureFlags{
			CircuitBreaker: true,
		},
		config.CircuitBreakerConfig{
			FailureThreshold: 1,
			CooldownSeconds:  30,
		},
		config.PerBackendLimiterConfig{},
	)
	if err != nil {
		t.Fatalf("upstream.New() failed: %v", err)
	}

	c := NewCollector(map[string]*upstream.Upstream{"api": up}, nil)
	c.IncrRequest()
	c.IncrConn()

	b := up.Backends()[0]
	b.IncrConn()
	b.RecordError()
	b.DecrConn()
	c.DecrConn()

	s := c.Snapshot()
	if s.TotalRequests != 1 {
		t.Fatalf("snapshot total_requests = %d, want 1", s.TotalRequests)
	}
	if s.ActiveConnections != 0 {
		t.Fatalf("snapshot active_connections = %d, want 0", s.ActiveConnections)
	}

	upstreamMetrics, ok := s.Upstreams["api"]
	if !ok {
		t.Fatalf("missing upstream metrics for api")
	}
	if len(upstreamMetrics.Backends) != 1 {
		t.Fatalf("backend metrics len = %d, want 1", len(upstreamMetrics.Backends))
	}

	bm := upstreamMetrics.Backends[0]
	if bm.TotalRequests != 1 {
		t.Fatalf("backend total_requests = %d, want 1", bm.TotalRequests)
	}
	if bm.TotalErrors != 1 {
		t.Fatalf("backend total_errors = %d, want 1", bm.TotalErrors)
	}
	if bm.CircuitBreakerState != "open" {
		t.Fatalf("breaker state = %q, want %q", bm.CircuitBreakerState, "open")
	}
	if bm.CircuitBreakerTrips != 1 {
		t.Fatalf("breaker trips = %d, want 1", bm.CircuitBreakerTrips)
	}
}
