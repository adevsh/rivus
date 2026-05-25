// Package balancer provides request dispatch strategies for upstream backend
// pools. It supports round-robin and least-connections selection and filters
// out unavailable backends before each pick.
package balancer

import (
	"errors"
	"fmt"

	"github.com/adevsh/rivus/backend"
	"github.com/adevsh/rivus/config"
)

// ErrUnknownStrategy indicates that balancer strategy is not supported.
var ErrUnknownStrategy = errors.New("unknown balancer strategy")

// Balancer selects the next backend for an upstream request.
type Balancer interface {
	Next(backends []*backend.Backend) *backend.Backend
}

// New constructs a balancer instance for the provided strategy name.
func New(strategy string) (Balancer, error) {
	switch strategy {
	case config.BalancerRoundRobin:
		return &RoundRobin{}, nil
	case config.BalancerLeastConnections:
		return &LeastConn{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownStrategy, strategy)
	}
}
