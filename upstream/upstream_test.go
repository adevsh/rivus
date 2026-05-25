package upstream

import (
	"testing"

	"github.com/adevsh/rivus/config"
)

// TestRewritePathPrecedence verifies all four rewrite precedence branches.
func TestRewritePathPrecedence(t *testing.T) {
	t.Parallel()

	t.Run("regex rewrite wins", func(t *testing.T) {
		t.Parallel()

		u := mustUpstream(t, UpstreamConfig{
			Prefix: "/static",
			Rewrite: &config.RewriteConfig{
				Pattern:     "^/static/(.*)$",
				Replacement: "/assets/$1",
			},
			Balancer: config.BalancerRoundRobin,
			Backends: []config.BackendConfig{{URL: "http://127.0.0.1:9401"}},
		})
		got := u.RewritePath("/static/app.js")
		if got != "/assets/app.js" {
			t.Fatalf("RewritePath() = %q, want %q", got, "/assets/app.js")
		}
	})

	t.Run("replace prefix second", func(t *testing.T) {
		t.Parallel()

		u := mustUpstream(t, UpstreamConfig{
			Prefix:        "/api",
			ReplacePrefix: "/v1",
			Balancer:      config.BalancerRoundRobin,
			Backends:      []config.BackendConfig{{URL: "http://127.0.0.1:9402"}},
		})
		got := u.RewritePath("/api/users")
		if got != "/v1/users" {
			t.Fatalf("RewritePath() = %q, want %q", got, "/v1/users")
		}
	})

	t.Run("strip prefix third", func(t *testing.T) {
		t.Parallel()

		u := mustUpstream(t, UpstreamConfig{
			Prefix:      "/api",
			StripPrefix: true,
			Balancer:    config.BalancerRoundRobin,
			Backends:    []config.BackendConfig{{URL: "http://127.0.0.1:9403"}},
		})
		got := u.RewritePath("/api/users")
		if got != "/users" {
			t.Fatalf("RewritePath() = %q, want %q", got, "/users")
		}
	})

	t.Run("passthrough fourth", func(t *testing.T) {
		t.Parallel()

		u := mustUpstream(t, UpstreamConfig{
			Prefix:   "/api",
			Balancer: config.BalancerRoundRobin,
			Backends: []config.BackendConfig{{URL: "http://127.0.0.1:9404"}},
		})
		got := u.RewritePath("/api/users")
		if got != "/api/users" {
			t.Fatalf("RewritePath() = %q, want %q", got, "/api/users")
		}
	})
}

func mustUpstream(t *testing.T, cfg UpstreamConfig) *Upstream {
	t.Helper()

	u, err := New(
		"test",
		cfg,
		FeatureFlags{},
		CircuitBreakerConfig{},
		PerBackendLimiterConfig{},
	)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return u
}
