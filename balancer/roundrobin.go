package balancer

// RoundRobin strategy — distributes requests across available backends in cyclic
// order, using an atomic counter for lock-free, concurrent-safe selection.

import (
	"sync/atomic"

	"github.com/adevsh/rivus/backend"
)

// RoundRobin selects available backends in cyclic order.
type RoundRobin struct {
	counter atomic.Uint64
}

// Next returns the next available backend using round-robin selection.
func (r *RoundRobin) Next(backends []*backend.Backend) *backend.Backend {
	available := make([]*backend.Backend, 0, len(backends))
	for _, b := range backends {
		if b != nil && b.IsAvailable() {
			available = append(available, b)
		}
	}
	if len(available) == 0 {
		return nil
	}

	idx := int(r.counter.Add(1)-1) % len(available)
	return available[idx]
}
