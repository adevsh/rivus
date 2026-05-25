package stress

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adevsh/rivus/config"
	"github.com/adevsh/rivus/proxy"
)

var (
	testMainServer *proxy.Server
	testMainCancel context.CancelFunc
)

func TestMain(m *testing.M) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cfg := baseConfig()
	cfg.Listen = mustFreeAddr()
	cfg.Features.Metrics = true
	cfg.Upstreams["bootstrap"] = config.UpstreamConfig{
		Prefix:   "/bootstrap",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}

	s, err := proxy.New(cfg)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		testMainCancel = cancel
		_ = ctx
		testMainServer = s
		go func() { _ = s.Start() }()
		_ = waitHTTP("http://"+cfg.Listen+"/metrics", 2*time.Second)
	}

	code := m.Run()

	if testMainServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = testMainServer.Shutdown(shutdownCtx)
		cancel()
	}
	if testMainCancel != nil {
		testMainCancel()
	}
	backend.Close()
	os.Exit(code)
}

func TestStressScenarios(t *testing.T) {
	t.Run("Scenario 1 — Round-robin distribution", testScenarioRoundRobinDistribution)
	t.Run("Scenario 2 — Least-connections routing", testScenarioLeastConnectionsRouting)
	t.Run("Scenario 3 — Health check failover", testScenarioHealthCheckFailover)
	t.Run("Scenario 4 — Circuit breaker trip", testScenarioCircuitBreakerTrip)
	t.Run("Scenario 5 — Per-IP rate limiting", testScenarioPerIPRateLimiting)
	t.Run("Scenario 6 — Per-backend rate limiting", testScenarioPerBackendRateLimiting)
	t.Run("Scenario 7 — TLS termination", testScenarioTLSTermination)
	t.Run("Scenario 8 — Graceful shutdown", testScenarioGracefulShutdown)
	t.Run("Scenario 9 — No-upstream 404", testScenarioNoUpstream404)
	t.Run("Scenario 10 — All-backends-down 503", testScenarioAllBackendsDown503)
}

func testScenarioRoundRobinDistribution(t *testing.T) {
	var c1, c2, c3 atomic.Int64
	b1 := httptest.NewServer(counterHandler(&c1, 0, http.StatusOK))
	b2 := httptest.NewServer(counterHandler(&c2, 0, http.StatusOK))
	b3 := httptest.NewServer(counterHandler(&c3, 0, http.StatusOK))
	defer b1.Close()
	defer b2.Close()
	defer b3.Close()

	cfg := baseConfig()
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: b1.URL}, {URL: b2.URL}, {URL: b3.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	client := &http.Client{Timeout: 2 * time.Second}
	for range 300 {
		resp, err := client.Get(baseURL + "/api/users")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
	}

	for _, got := range []int64{c1.Load(), c2.Load(), c3.Load()} {
		if got < 85 || got > 115 {
			t.Fatalf("distribution out of expected bounds: c1=%d c2=%d c3=%d", c1.Load(), c2.Load(), c3.Load())
		}
	}
}

func testScenarioLeastConnectionsRouting(t *testing.T) {
	var fastCount, slowCount atomic.Int64
	fast := httptest.NewServer(counterHandler(&fastCount, 0, http.StatusOK))
	slow := httptest.NewServer(counterHandler(&slowCount, 50*time.Millisecond, http.StatusOK))
	defer fast.Close()
	defer slow.Close()

	cfg := baseConfig()
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerLeastConnections,
		Backends: []config.BackendConfig{{URL: fast.URL}, {URL: slow.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	var wg sync.WaitGroup
	client := &http.Client{Timeout: 3 * time.Second}
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(baseURL + "/api/load")
			if err == nil && resp != nil {
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	if slowCount.Load() >= fastCount.Load() {
		t.Fatalf("slow backend should receive fewer requests, slow=%d fast=%d", slowCount.Load(), fastCount.Load())
	}
}

func testScenarioHealthCheckFailover(t *testing.T) {
	var b1Count, b2Count atomic.Int64
	b1 := newRestartableBackend(counterHandler(&b1Count, 0, http.StatusOK))
	b2 := httptest.NewServer(counterHandler(&b2Count, 0, http.StatusOK))
	defer b2.Close()

	b1.Start(t)
	defer b1.Stop(t)

	cfg := baseConfig()
	cfg.Features.HealthCheck = true
	cfg.HealthCheck = config.HealthCheckConfig{IntervalSeconds: 1, Path: "/health", TimeoutSeconds: 1}
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: b1.URL()}, {URL: b2.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	b1.Stop(t)
	time.Sleep(2200 * time.Millisecond)

	client := &http.Client{Timeout: 2 * time.Second}
	b1Count.Store(0)
	b2Count.Store(0)
	for range 50 {
		resp, err := client.Get(baseURL + "/api/failover")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		_ = resp.Body.Close()
	}
	if b1Count.Load() != 0 || b2Count.Load() != 50 {
		t.Fatalf("expected all traffic to backend2 after failover, b1=%d b2=%d", b1Count.Load(), b2Count.Load())
	}

	b1.Start(t)
	time.Sleep(2200 * time.Millisecond)
	b1Count.Store(0)
	b2Count.Store(0)
	for range 60 {
		resp, err := client.Get(baseURL + "/api/recover")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		_ = resp.Body.Close()
	}
	if b1Count.Load() == 0 || b2Count.Load() == 0 {
		t.Fatalf("expected traffic on both backends after recovery, b1=%d b2=%d", b1Count.Load(), b2Count.Load())
	}
}

func testScenarioCircuitBreakerTrip(t *testing.T) {
	var failMode atomic.Bool
	failMode.Store(true)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failMode.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Features.CircuitBreaker = true
	cfg.CircuitBreaker = config.CircuitBreakerConfig{FailureThreshold: 3, CooldownSeconds: 1}
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()
	client := &http.Client{Timeout: 2 * time.Second}

	for range 4 {
		resp, err := client.Get(baseURL + "/api/cb")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		_ = resp.Body.Close()
	}

	resp, err := client.Get(baseURL + "/api/cb")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected unavailable status after breaker trip, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	time.Sleep(1100 * time.Millisecond)
	failMode.Store(false)

	probeResp, err := client.Get(baseURL + "/api/cb")
	if err != nil {
		t.Fatalf("probe request failed: %v", err)
	}
	_ = probeResp.Body.Close()
	if probeResp.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200", probeResp.StatusCode)
	}
}

func testScenarioPerIPRateLimiting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Features.RateLimiter = true
	cfg.RateLimiter.PerIP = config.PerIPLimiterConfig{RequestsPerSecond: 10, Burst: 5}
	cfg.RateLimiter.PerBackend = config.PerBackendLimiterConfig{RequestsPerSecond: 1000, Burst: 1000}
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	client := &http.Client{Timeout: 2 * time.Second}
	var limited int
	for range 50 {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/rl", nil)
		req.Header.Set("X-Forwarded-For", "198.51.100.10")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			limited++
		}
		_ = resp.Body.Close()
	}
	if limited == 0 {
		t.Fatalf("expected some 429 responses, got none")
	}
}

func testScenarioPerBackendRateLimiting(t *testing.T) {
	backend := httptest.NewServer(counterHandler(new(atomic.Int64), 0, http.StatusOK))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Features.RateLimiter = true
	cfg.RateLimiter.PerIP = config.PerIPLimiterConfig{RequestsPerSecond: 1000, Burst: 1000}
	cfg.RateLimiter.PerBackend = config.PerBackendLimiterConfig{RequestsPerSecond: 5, Burst: 2}
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	client := &http.Client{Timeout: 2 * time.Second}
	var got503 int
	for range 30 {
		resp, err := client.Get(baseURL + "/api/be-rl")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if resp.StatusCode == http.StatusServiceUnavailable {
			got503++
		}
		_ = resp.Body.Close()
	}
	if got503 == 0 {
		t.Fatalf("expected some 503 responses when backend limiter saturated")
	}
}

func testScenarioTLSTermination(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	certFile, keyFile := mustSelfSignedCert(t)

	cfg := baseConfig()
	cfg.TLS.Enabled = true
	cfg.TLS.CertFile = certFile
	cfg.TLS.KeyFile = keyFile
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	resp, err := client.Get(baseURL + "/api/tls")
	if err != nil {
		t.Fatalf("https request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, want %q", string(body), "ok")
	}
}

func testScenarioGracefulShutdown(t *testing.T) {
	backend := httptest.NewServer(counterHandler(new(atomic.Int64), 100*time.Millisecond, http.StatusOK))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}

	s, baseURL, errCh := mustStartProxyServer(t, cfg)
	client := &http.Client{Timeout: 3 * time.Second}

	statuses := make(chan int, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(baseURL + "/api/slow")
			if err != nil {
				statuses <- 0
				return
			}
			statuses <- resp.StatusCode
			_ = resp.Body.Close()
		}()
	}

	time.Sleep(20 * time.Millisecond)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
	wg.Wait()
	close(statuses)

	for st := range statuses {
		if st != http.StatusOK {
			t.Fatalf("expected all in-flight requests 200, got status %d", st)
		}
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("server exit error: %v", err)
	}
}

func testScenarioNoUpstream404(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(baseURL + "/unknown/path")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func testScenarioAllBackendsDown503(t *testing.T) {
	b1 := newRestartableBackend(counterHandler(new(atomic.Int64), 0, http.StatusOK))
	b1.Start(t)
	defer b1.Stop(t)

	cfg := baseConfig()
	cfg.Features.HealthCheck = true
	cfg.HealthCheck = config.HealthCheckConfig{IntervalSeconds: 1, Path: "/health", TimeoutSeconds: 1}
	cfg.Upstreams["api"] = config.UpstreamConfig{
		Prefix:   "/api",
		Balancer: config.BalancerRoundRobin,
		Backends: []config.BackendConfig{{URL: b1.URL()}},
	}

	baseURL, stop := mustStartProxy(t, cfg)
	defer stop()

	b1.Stop(t)
	time.Sleep(2200 * time.Millisecond)

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(baseURL + "/api/down")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func baseConfig() *config.Config {
	return &config.Config{
		Listen: mustFreeAddr(),
		TLS:    config.TLSConfig{Enabled: false},
		Transport: config.TransportConfig{
			MaxIdleConns:           200,
			DialTimeoutSeconds:     2,
			ResponseHeaderTimeout:  3,
			IdleConnTimeoutSeconds: 30,
		},
		Features: config.FeatureFlags{},
		RateLimiter: config.RateLimiterConfig{
			PerIP:      config.PerIPLimiterConfig{RequestsPerSecond: 1000, Burst: 1000},
			PerBackend: config.PerBackendLimiterConfig{RequestsPerSecond: 1000, Burst: 1000},
		},
		HealthCheck:    config.HealthCheckConfig{IntervalSeconds: 1, Path: "/health", TimeoutSeconds: 1},
		CircuitBreaker: config.CircuitBreakerConfig{FailureThreshold: 3, CooldownSeconds: 1},
		Upstreams:      map[string]config.UpstreamConfig{},
	}
}

func mustStartProxy(t *testing.T, cfg *config.Config) (string, func()) {
	t.Helper()
	s, baseURL, errCh := mustStartProxyServer(t, cfg)
	return baseURL, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("server exit error: %v", err)
		}
	}
}

func mustStartProxyServer(t *testing.T, cfg *config.Config) (*proxy.Server, string, chan error) {
	t.Helper()
	s, err := proxy.New(cfg)
	if err != nil {
		t.Fatalf("proxy.New() failed: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start()
	}()

	scheme := "http"
	if cfg.TLS.Enabled {
		scheme = "https"
	}
	baseURL := scheme + "://" + cfg.Listen
	if err := waitHTTP(baseURL, 3*time.Second); err != nil {
		t.Fatalf("proxy did not start: %v", err)
	}
	return s, baseURL, errCh
}

func waitHTTP(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 300 * time.Millisecond}
	if len(baseURL) >= len("https://") && baseURL[:len("https://")] == "https://" {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/metrics")
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", baseURL)
}

func counterHandler(counter *atomic.Int64, delay time.Duration, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
	}
}

func mustFreeAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

type restartableBackend struct {
	addr    string
	handler http.Handler
	mu      sync.Mutex
	srv     *http.Server
}

func newRestartableBackend(handler http.Handler) *restartableBackend {
	return &restartableBackend{
		addr:    mustFreeAddr(),
		handler: handler,
	}
}

func (r *restartableBackend) URL() string {
	return "http://" + r.addr
}

func (r *restartableBackend) Start(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.srv != nil {
		return
	}
	ln, err := net.Listen("tcp", r.addr)
	if err != nil {
		t.Fatalf("restartable backend listen failed: %v", err)
	}
	r.srv = &http.Server{Handler: r.handler}
	go func(srv *http.Server, l net.Listener) {
		_ = srv.Serve(l)
	}(r.srv, ln)
}

func (r *restartableBackend) Stop(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	srv := r.srv
	r.srv = nil
	r.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func mustSelfSignedCert(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key failed: %v", err)
	}

	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:   time.Now().Add(-time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate failed: %v", err)
	}

	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")

	certOut, err := os.Create(certFile)
	if err != nil {
		t.Fatalf("create cert file failed: %v", err)
	}
	defer certOut.Close()
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyOut, err := os.Create(keyFile)
	if err != nil {
		t.Fatalf("create key file failed: %v", err)
	}
	defer keyOut.Close()
	_ = pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return certFile, keyFile
}
