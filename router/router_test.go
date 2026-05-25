package router

import (
	"testing"

	"github.com/adevsh/rivus/config"
	"github.com/adevsh/rivus/upstream"
)

// TestRouterLongestPrefixMatch verifies longer prefixes win over shorter matches.
func TestRouterLongestPrefixMatch(t *testing.T) {
	t.Parallel()

	uAPIV1 := mustUpstream(t, "api-v1", config.UpstreamConfig{
		Prefix:   "/api/v1",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: "http://127.0.0.1:9301"}},
	})
	uAPI := mustUpstream(t, "api", config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: "http://127.0.0.1:9302"}},
	})
	uStatic := mustUpstream(t, "static", config.UpstreamConfig{
		Prefix:   "/static",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: "http://127.0.0.1:9303"}},
	})

	r := New(map[string]*upstream.Upstream{
		"api-v1": uAPIV1,
		"api":    uAPI,
		"static": uStatic,
	})

	match := r.Match("/api/v1/users")
	if match == nil {
		t.Fatalf("Match() returned nil")
	}
	if match != uAPIV1 {
		t.Fatalf("matched upstream = %q, want %q", match.Name, uAPIV1.Name)
	}
}

func mustUpstream(t *testing.T, name string, cfg config.UpstreamConfig) *upstream.Upstream {
	t.Helper()

	u, err := upstream.New(
		name,
		cfg,
		config.FeatureFlags{},
		config.CircuitBreakerConfig{},
		config.PerBackendLimiterConfig{},
	)
	if err != nil {
		t.Fatalf("upstream.New(%q) failed: %v", name, err)
	}
	return u
}
