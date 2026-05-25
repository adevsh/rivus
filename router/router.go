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
func (r *Router) Match(path string) *upstream.Upstream {
	for _, route := range r.routes {
		if strings.HasPrefix(path, route.prefix) {
			return route.upstream
		}
	}
	return nil
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
