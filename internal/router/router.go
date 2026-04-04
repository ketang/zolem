// internal/router/router.go
package router

import (
	"strings"

	"zolem.dev/zolem/internal/config"
)

// LabelsKey is the context key for virtual host labels.
// Defined here so all packages use the same type for context value lookup.
type LabelsKey struct{}

// RouteContext carries the resolved provider and extracted labels for a request.
type RouteContext struct {
	Provider string
	Labels   map[string]string
}

// Router matches incoming Host header values against a configured routing table.
type Router struct {
	routes []config.RouteConfig
}

func New(routes []config.RouteConfig) *Router {
	return &Router{routes: routes}
}

// Match evaluates host against the routing table in order; first match wins.
// Returns the resolved RouteContext and true on match, zero value and false otherwise.
func (r *Router) Match(host string) (RouteContext, bool) {
	// strip port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	hostParts := strings.Split(host, ".")

	for _, route := range r.routes {
		patternParts := strings.Split(route.Host, ".")
		if captures, ok := matchParts(hostParts, patternParts); ok {
			labels := make(map[string]string, len(route.Labels))
			for k, v := range route.Labels {
				resolved := v
				for i, cap := range captures {
					placeholder := "{" + string(rune('0'+i+1)) + "}"
					resolved = strings.ReplaceAll(resolved, placeholder, cap)
				}
				labels[k] = resolved
			}
			return RouteContext{Provider: route.Provider, Labels: labels}, true
		}
	}
	return RouteContext{}, false
}

// matchParts attempts to match hostParts against patternParts where "*" is a
// single-label wildcard. Returns captured wildcard values in order.
func matchParts(host, pattern []string) ([]string, bool) {
	if len(host) != len(pattern) {
		return nil, false
	}
	var captures []string
	for i, p := range pattern {
		if p == "*" {
			captures = append(captures, host[i])
		} else if p != host[i] {
			return nil, false
		}
	}
	return captures, true
}
