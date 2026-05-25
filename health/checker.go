// Package health periodically probes backend URLs and updates each backend's
// health state, allowing the router to exclude unreachable targets from
// request dispatch.
package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/adevsh/rivus/backend"
	"github.com/adevsh/rivus/config"
)

// HealthCheckConfig aliases shared health checker configuration fields.
type HealthCheckConfig = config.HealthCheckConfig

// Checker periodically probes all configured backends and updates health flags.
type Checker struct {
	upstreams map[string][]*backend.Backend
	cfg       HealthCheckConfig
	client    *http.Client
}

// New creates a checker with timeout and redirect settings from config.
func New(upstreams map[string][]*backend.Backend, cfg HealthCheckConfig) *Checker {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Second
	}

	return &Checker{
		upstreams: upstreams,
		cfg:       cfg,
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Start begins one lifecycle-managed goroutine that runs backend probes on a ticker.
func (c *Checker) Start(ctx context.Context) {
	interval := time.Duration(c.cfg.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Second
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.checkAll()
			}
		}
	}()
}

// checkAll probes all backends across all upstream groups.
func (c *Checker) checkAll() {
	for _, group := range c.upstreams {
		for _, b := range group {
			if b == nil {
				continue
			}
			c.probe(b)
		}
	}
}

// probe sends one health request and updates backend health based on response status.
func (c *Checker) probe(b *backend.Backend) {
	healthURL := buildHealthURL(b.URL, c.cfg.Path)

	resp, err := c.client.Get(healthURL)
	if err != nil {
		b.Healthy.Store(false)
		slog.Warn("health probe failed", "backend", b.URL.String(), "err", err)
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		b.Healthy.Store(true)
		slog.Debug("health probe healthy", "backend", b.URL.String(), "status", resp.StatusCode)
		return
	}

	b.Healthy.Store(false)
	slog.Warn("health probe unhealthy", "backend", b.URL.String(), "status", resp.StatusCode)
}

func buildHealthURL(base *url.URL, path string) string {
	if base == nil {
		return path
	}
	u := *base
	if path == "" {
		return u.String()
	}

	ref := &url.URL{Path: path}
	return u.ResolveReference(ref).String()
}
