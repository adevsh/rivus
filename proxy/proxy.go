package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/adevsh/rivus/backend"
	"github.com/adevsh/rivus/config"
	"github.com/adevsh/rivus/health"
	"github.com/adevsh/rivus/metrics"
	"github.com/adevsh/rivus/ratelimit"
	rivusrouter "github.com/adevsh/rivus/router"
	"github.com/adevsh/rivus/upstream"
)

// Server wires all runtime dependencies for the Rivus proxy process.
type Server struct {
	cfg        *config.Config
	httpServer *http.Server
	router     *rivusrouter.Router
	checker    *health.Checker
	collector  *metrics.Collector
	ipLimiter  *ratelimit.IPLimiter
	ctx        context.Context
	cancel     context.CancelFunc
}

// New constructs a runnable server from config with feature-flagged components.
func New(cfg *config.Config) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config must not be nil")
	}

	upstreams := make(map[string]*upstream.Upstream, len(cfg.Upstreams))
	for name, upCfg := range cfg.Upstreams {
		up, err := upstream.New(name, upCfg, cfg.Features, cfg.CircuitBreaker, cfg.RateLimiter.PerBackend)
		if err != nil {
			return nil, fmt.Errorf("build upstream %q: %w", name, err)
		}
		upstreams[name] = up
	}

	rtr := rivusrouter.New(upstreams)

	var ipLimiter *ratelimit.IPLimiter
	if cfg.Features.RateLimiter {
		ipLimiter = ratelimit.NewIPLimiter(cfg.RateLimiter.PerIP)
	}

	var collector *metrics.Collector
	if cfg.Features.Metrics {
		collector = metrics.NewCollector(upstreams, ipLimiter)
	}

	transport := &http.Transport{
		MaxIdleConns:          cfg.Transport.MaxIdleConns,
		ResponseHeaderTimeout: time.Duration(cfg.Transport.ResponseHeaderTimeout) * time.Second,
		IdleConnTimeout:       time.Duration(cfg.Transport.IdleConnTimeoutSeconds) * time.Second,
		DialContext: (&net.Dialer{
			Timeout: time.Duration(cfg.Transport.DialTimeoutSeconds) * time.Second,
		}).DialContext,
	}
	for _, up := range upstreams {
		up.SetTransport(transport)
	}

	var checker *health.Checker
	if cfg.Features.HealthCheck {
		healthUpstreams := make(map[string][]*backend.Backend, len(upstreams))
		for name, up := range upstreams {
			healthUpstreams[name] = up.Backends()
		}
		checker = health.New(healthUpstreams, cfg.HealthCheck)
	}

	appHandler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if collector != nil {
			collector.IncrRequest()
			collector.IncrConn()
			defer collector.DecrConn()
		}
		rtr.ServeHTTP(w, r)
	}))
	if ipLimiter != nil {
		appHandler = ipLimiter.Handler(appHandler)
	}

	root := http.NewServeMux()
	root.Handle("/", appHandler)
	if collector != nil {
		root.Handle("/metrics", collector.Handler())
	}

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	if cfg.TLS.Enabled {
		pair, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load tls cert/key: %w", err)
		}
		httpServer.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{pair},
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:        cfg,
		httpServer: httpServer,
		router:     rtr,
		checker:    checker,
		collector:  collector,
		ipLimiter:  ipLimiter,
		ctx:        ctx,
		cancel:     cancel,
	}, nil
}

// Start launches background routines and starts serving HTTP or HTTPS.
func (s *Server) Start() error {
	if s.checker != nil {
		s.checker.Start(s.ctx)
	}
	if s.ipLimiter != nil {
		go s.ipLimiter.Cleanup(s.ctx)
	}

	if s.cfg.TLS.Enabled {
		return s.httpServer.ListenAndServeTLS("", "")
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown cancels background routines and gracefully drains active HTTP traffic.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	return s.httpServer.Shutdown(ctx)
}
