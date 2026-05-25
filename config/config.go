package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	// BalancerRoundRobin is the round-robin balancing strategy name.
	BalancerRoundRobin = "round_robin"
	// BalancerLeastConnections is the least-connections balancing strategy name.
	BalancerLeastConnections = "least_connections"
)

// TLSConfig defines TLS enablement and certificate file locations.
type TLSConfig struct {
	Enabled  bool   `json:"enabled"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// TransportConfig defines outbound HTTP transport tuning values.
type TransportConfig struct {
	MaxIdleConns           int `json:"max_idle_conns"`
	DialTimeoutSeconds     int `json:"dial_timeout_seconds"`
	ResponseHeaderTimeout  int `json:"response_header_timeout_seconds"`
	IdleConnTimeoutSeconds int `json:"idle_conn_timeout_seconds"`
}

// FeatureFlags controls optional runtime features.
type FeatureFlags struct {
	RateLimiter    bool `json:"rate_limiter"`
	CircuitBreaker bool `json:"circuit_breaker"`
	HealthCheck    bool `json:"health_check"`
	Metrics        bool `json:"metrics"`
}

// PerIPLimiterConfig defines token bucket settings for per-IP limiting.
type PerIPLimiterConfig struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Burst             int     `json:"burst"`
}

// PerBackendLimiterConfig defines token bucket settings for per-backend limiting.
type PerBackendLimiterConfig struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Burst             int     `json:"burst"`
}

// RateLimiterConfig groups per-IP and per-backend limiter configuration.
type RateLimiterConfig struct {
	PerIP      PerIPLimiterConfig      `json:"per_ip"`
	PerBackend PerBackendLimiterConfig `json:"per_backend"`
}

// HealthCheckConfig defines periodic backend health probe behavior.
type HealthCheckConfig struct {
	IntervalSeconds int    `json:"interval_seconds"`
	Path            string `json:"path"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
}

// CircuitBreakerConfig defines failure threshold and cooldown timing.
type CircuitBreakerConfig struct {
	FailureThreshold int `json:"failure_threshold"`
	CooldownSeconds  int `json:"cooldown_seconds"`
}

// RewriteConfig defines regex-based path rewrite behavior.
type RewriteConfig struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

// BackendConfig defines one backend target URL.
type BackendConfig struct {
	URL string `json:"url"`
}

// UpstreamConfig defines routing, rewriting, balancing, and backend targets.
type UpstreamConfig struct {
	Prefix        string          `json:"prefix"`
	StripPrefix   bool            `json:"strip_prefix"`
	ReplacePrefix string          `json:"replace_prefix"`
	Rewrite       *RewriteConfig  `json:"rewrite"`
	Balancer      string          `json:"balancer"`
	Backends      []BackendConfig `json:"backends"`
}

// Config defines all server configuration loaded from JSON.
type Config struct {
	Listen         string                    `json:"listen"`
	TLS            TLSConfig                 `json:"tls"`
	Transport      TransportConfig           `json:"transport"`
	Features       FeatureFlags              `json:"features"`
	RateLimiter    RateLimiterConfig         `json:"rate_limiter"`
	HealthCheck    HealthCheckConfig         `json:"health_check"`
	CircuitBreaker CircuitBreakerConfig      `json:"circuit_breaker"`
	Upstreams      map[string]UpstreamConfig `json:"upstreams"`
}

// Load reads, decodes, and validates config from a JSON file path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	var cfg Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config json: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks the config for required fields and invalid combinations.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen must be non-empty")
	}

	if c.TLS.Enabled {
		if strings.TrimSpace(c.TLS.CertFile) == "" || strings.TrimSpace(c.TLS.KeyFile) == "" {
			return errors.New("tls cert_file and key_file must be non-empty when tls is enabled")
		}
	}

	for name, up := range c.Upstreams {
		if len(up.Backends) == 0 {
			return fmt.Errorf("upstream %q must have at least one backend", name)
		}

		if up.Balancer != BalancerRoundRobin && up.Balancer != BalancerLeastConnections {
			return fmt.Errorf("upstream %q has invalid balancer %q", name, up.Balancer)
		}

		if strings.TrimSpace(up.ReplacePrefix) != "" && up.Rewrite != nil {
			return fmt.Errorf("upstream %q cannot set both replace_prefix and rewrite", name)
		}

		for i, be := range up.Backends {
			if _, err := url.Parse(be.URL); err != nil {
				return fmt.Errorf("upstream %q backend %d has invalid url %q: %w", name, i, be.URL, err)
			}
		}
	}

	return nil
}
