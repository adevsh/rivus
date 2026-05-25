package balancer

// LeastConn strategy — picks the available backend with the fewest active
// connections, preferring less-loaded targets for long-lived or expensive requests.

import "github.com/adevsh/rivus/backend"

// LeastConn selects the available backend with the lowest active connections.
type LeastConn struct{}

// Next returns the available backend that currently has the fewest active connections.
func (l *LeastConn) Next(backends []*backend.Backend) *backend.Backend {
	var selected *backend.Backend
	var minConns int64

	for _, b := range backends {
		if b == nil || !b.IsAvailable() {
			continue
		}
		active := b.ActiveConns.Load()
		if selected == nil || active < minConns {
			selected = b
			minConns = active
		}
	}

	return selected
}
