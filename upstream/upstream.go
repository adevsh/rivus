// Package upstream owns the per-route runtime: path rewriting, backend
// selection, circuit-breaker and rate-limiter gating, and the reverse-proxy
// request lifecycle including X-Forwarded-For chaining and structured logging.
package upstream

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adevsh/rivus/backend"
	"github.com/adevsh/rivus/balancer"
	"github.com/adevsh/rivus/config"
)

// UpstreamConfig aliases shared upstream configuration fields.
type UpstreamConfig = config.UpstreamConfig

// FeatureFlags aliases shared feature toggle fields.
type FeatureFlags = config.FeatureFlags

// CircuitBreakerConfig aliases shared circuit breaker settings.
type CircuitBreakerConfig = config.CircuitBreakerConfig

// PerBackendLimiterConfig aliases shared backend limiter settings.
type PerBackendLimiterConfig = config.PerBackendLimiterConfig

// Upstream owns backends, balancing strategy, and path rewrite behavior for one route prefix.
type Upstream struct {
	Name      string
	cfg       UpstreamConfig
	backends  []*backend.Backend
	balancer  balancer.Balancer
	rewriteRe *regexp.Regexp
	transport http.RoundTripper
}

// New creates an upstream runtime from config, selected features, and backend options.
func New(name string, cfg UpstreamConfig, features FeatureFlags, cbCfg CircuitBreakerConfig, rlCfg PerBackendLimiterConfig) (*Upstream, error) {
	if strings.TrimSpace(cfg.ReplacePrefix) != "" && cfg.Rewrite != nil {
		return nil, fmt.Errorf("upstream %q cannot set both replace_prefix and rewrite", name)
	}

	bs := make([]*backend.Backend, 0, len(cfg.Backends))
	for _, beCfg := range cfg.Backends {
		var cbOpt *CircuitBreakerConfig
		var rlOpt *PerBackendLimiterConfig
		if features.CircuitBreaker {
			cbCopy := cbCfg
			cbOpt = &cbCopy
		}
		if features.RateLimiter {
			rlCopy := rlCfg
			rlOpt = &rlCopy
		}

		b, err := backend.New(beCfg, cbOpt, rlOpt)
		if err != nil {
			return nil, fmt.Errorf("create backend for upstream %q: %w", name, err)
		}
		bs = append(bs, b)
	}

	bal, err := balancer.New(cfg.Balancer)
	if err != nil {
		return nil, fmt.Errorf("create balancer for upstream %q: %w", name, err)
	}

	var rewriteRe *regexp.Regexp
	if cfg.Rewrite != nil {
		rewriteRe, err = regexp.Compile(cfg.Rewrite.Pattern)
		if err != nil {
			return nil, fmt.Errorf("compile rewrite regex for upstream %q: %w", name, err)
		}
	}

	return &Upstream{
		Name:      name,
		cfg:       cfg,
		backends:  bs,
		balancer:  bal,
		rewriteRe: rewriteRe,
	}, nil
}

// Prefix returns the configured route prefix for this upstream.
func (u *Upstream) Prefix() string {
	return u.cfg.Prefix
}

// Backends returns this upstream's backend set.
func (u *Upstream) Backends() []*backend.Backend {
	return u.backends
}

// SetTransport sets the outbound round tripper used by reverse proxy requests.
func (u *Upstream) SetTransport(transport http.RoundTripper) {
	u.transport = transport
}

// Next picks the next available backend using the configured balancer.
func (u *Upstream) Next() *backend.Backend {
	return u.balancer.Next(u.backends)
}

// RewritePath rewrites the request path according to the following precedence
// (only the first matching rule fires):
//  1. rewrite — regex ReplaceAll if the pattern matches.
//  2. replace_prefix — replace the first occurrence of the upstream prefix.
//  3. strip_prefix — remove the upstream prefix from the start of path.
//  4. passthrough — path is returned unchanged.
func (u *Upstream) RewritePath(path string) string {
	if u.rewriteRe != nil && u.rewriteRe.MatchString(path) {
		return u.rewriteRe.ReplaceAllString(path, u.cfg.Rewrite.Replacement)
	}
	if u.cfg.ReplacePrefix != "" {
		return strings.Replace(path, u.cfg.Prefix, u.cfg.ReplacePrefix, 1)
	}
	if u.cfg.StripPrefix {
		return strings.TrimPrefix(path, u.cfg.Prefix)
	}
	return path
}

// ServeHTTP forwards one request to a selected backend through a reverse proxy.
func (u *Upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestPath := r.URL.Path
	method := r.Method
	b := u.Next()
	if b == nil {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		slog.Warn("request completed",
			"upstream", u.Name,
			"backend", "",
			"method", method,
			"path", requestPath,
			"status", http.StatusServiceUnavailable,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return
	}

	var hadError atomic.Bool
	statusCode := http.StatusBadGateway
	originalQuery := r.URL.RawQuery
	clientIP := requestIP(r)

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = b.URL.Scheme
			req.URL.Host = b.URL.Host
			req.URL.Path = u.RewritePath(req.URL.Path)
			req.URL.RawQuery = originalQuery
			req.Host = b.URL.Host
			prior := req.Header.Get("X-Forwarded-For")
			if prior != "" {
				req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
			} else {
				req.Header.Set("X-Forwarded-For", clientIP)
			}
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, _ error) {
			hadError.Store(true)
			statusCode = http.StatusBadGateway
			b.RecordError()
			http.Error(rw, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
		ModifyResponse: func(resp *http.Response) error {
			statusCode = resp.StatusCode
			if resp.StatusCode >= http.StatusInternalServerError {
				hadError.Store(true)
				b.RecordError()
			}
			return nil
		},
	}
	if u.transport != nil {
		rp.Transport = u.transport
	}

	b.IncrConn()
	defer b.DecrConn()
	rp.ServeHTTP(w, r)
	if !hadError.Load() {
		b.RecordSuccess()
	}
	slog.Info("request completed",
		"upstream", u.Name,
		"backend", b.URL.String(),
		"method", method,
		"path", requestPath,
		"status", statusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}
