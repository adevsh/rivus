package health

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/adevsh/rivus/backend"
	"github.com/adevsh/rivus/config"
)

// TestCheckerProbeTransitions verifies unhealthy and healthy transitions across stop/restart.
func TestCheckerProbeTransitions(t *testing.T) {
	t.Parallel()

	srv := newRestartableServer(t)
	srv.start(t)
	defer srv.stop(t)

	b, err := backend.New(config.BackendConfig{URL: srv.url()}, nil, nil)
	if err != nil {
		t.Fatalf("backend.New() failed: %v", err)
	}

	checker := New(
		map[string][]*backend.Backend{"api": {b}},
		config.HealthCheckConfig{
			IntervalSeconds: 1,
			Path:            "/health",
			TimeoutSeconds:  1,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checker.Start(ctx)

	waitFor(t, 2500*time.Millisecond, func() bool { return b.Healthy.Load() }, "backend should be healthy while server is up")

	srv.stop(t)
	waitFor(t, 2500*time.Millisecond, func() bool { return !b.Healthy.Load() }, "backend should become unhealthy within 2 ticks after shutdown")

	srv.start(t)
	waitFor(t, 2500*time.Millisecond, func() bool { return b.Healthy.Load() }, "backend should become healthy within 2 ticks after restart")
}

type restartableServer struct {
	addr string
	mu   sync.Mutex
	srv  *http.Server
}

func newRestartableServer(t *testing.T) *restartableServer {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	return &restartableServer{addr: addr}
}

func (s *restartableServer) url() string {
	return "http://" + s.addr
}

func (s *restartableServer) start(t *testing.T) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		return
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		t.Fatalf("restartable server listen failed: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	s.srv = &http.Server{Handler: mux}
	go func(server *http.Server, listener net.Listener) {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			t.Logf("restartable server serve error: %v", serveErr)
		}
	}(s.srv, ln)
}

func (s *restartableServer) stop(t *testing.T) {
	t.Helper()

	s.mu.Lock()
	server := s.srv
	s.srv = nil
	s.mu.Unlock()

	if server == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("restartable server shutdown failed: %v", err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, message string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(message)
}
