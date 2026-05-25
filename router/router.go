// Package router dispatches incoming HTTP requests to upstreams using
// longest-prefix matching on the request path, with segment-boundary awareness
// so that "/api" never accidentally matches "/apitest".
package router

import (
	"net/http"
	"sort"
	"strings"

	"github.com/adevsh/rivus/upstream"
)

type route struct {
	prefix   string
	upstream *upstream.Upstream
}

// Router dispatches incoming requests to upstreams using longest-prefix matching.
type Router struct {
	routes    []route
	upstreams map[string]*upstream.Upstream
}

// New builds a prefix-sorted router from upstream instances.
func New(upstreams map[string]*upstream.Upstream) *Router {
	routes := make([]route, 0, len(upstreams))
	for _, up := range upstreams {
		if up == nil {
			continue
		}
		routes = append(routes, route{
			prefix:   up.Prefix(),
			upstream: up,
		})
	}

	sort.Slice(routes, func(i, j int) bool {
		return len(routes[i].prefix) > len(routes[j].prefix)
	})

	return &Router{
		routes:    routes,
		upstreams: upstreams,
	}
}

// Match returns the first upstream whose prefix matches the request path.
// Matching is segment-aware: "/api" matches "/api" and "/api/foo" but not "/apitest".
// A prefix of "/" acts as a catch-all and matches any non-empty path.
func (r *Router) Match(path string) *upstream.Upstream {
	for _, route := range r.routes {
		if matchesPrefix(path, route.prefix) {
			return route.upstream
		}
	}
	return nil
}

func matchesPrefix(path, prefix string) bool {
	if prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// ServeHTTP routes to matching upstream or returns 404 when no route matches.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	up := r.Match(req.URL.Path)
	if up == nil {
		http.NotFound(w, req)
		return
	}
	up.ServeHTTP(w, req)
}
