package balancer

import (
	"testing"

	"github.com/adevsh/rivus/backend"
	"github.com/adevsh/rivus/config"
)

// TestRoundRobinDistribution verifies fair distribution over three backends.
func TestRoundRobinDistribution(t *testing.T) {
	t.Parallel()

	b1 := mustBackend(t, "http://127.0.0.1:9101")
	b2 := mustBackend(t, "http://127.0.0.1:9102")
	b3 := mustBackend(t, "http://127.0.0.1:9103")
	backends := []*backend.Backend{b1, b2, b3}

	rr := &RoundRobin{}
	counts := map[string]int{
		b1.URL.Host: 0,
		b2.URL.Host: 0,
		b3.URL.Host: 0,
	}

	for i := 0; i < 9; i++ {
		next := rr.Next(backends)
		if next == nil {
			t.Fatalf("Next() returned nil at iteration %d", i)
		}
		counts[next.URL.Host]++
	}

	if counts[b1.URL.Host] != 3 || counts[b2.URL.Host] != 3 || counts[b3.URL.Host] != 3 {
		t.Fatalf("distribution mismatch: got %v, want each backend selected 3 times", counts)
	}
}

// TestLeastConnSelection verifies lowest ActiveConns backend is selected.
func TestLeastConnSelection(t *testing.T) {
	t.Parallel()

	b1 := mustBackend(t, "http://127.0.0.1:9201")
	b2 := mustBackend(t, "http://127.0.0.1:9202")
	b3 := mustBackend(t, "http://127.0.0.1:9203")

	b1.ActiveConns.Add(5)
	b2.ActiveConns.Add(2)
	b3.ActiveConns.Add(8)

	lc := &LeastConn{}
	next := lc.Next([]*backend.Backend{b1, b2, b3})
	if next == nil {
		t.Fatalf("Next() returned nil")
	}
	if next != b2 {
		t.Fatalf("selected backend = %s, want %s", next.URL.Host, b2.URL.Host)
	}
}

func mustBackend(t *testing.T, rawURL string) *backend.Backend {
	t.Helper()

	b, err := backend.New(config.BackendConfig{URL: rawURL}, nil, nil)
	if err != nil {
		t.Fatalf("backend.New(%q) failed: %v", rawURL, err)
	}
	return b
}
