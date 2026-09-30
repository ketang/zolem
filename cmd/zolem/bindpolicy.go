package main

import (
	"errors"
	"net"
	"slices"
	"strings"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// resolveBindPolicy turns the -allow-non-loopback-bind / -listener-port-range
// flags into a bind policy. Loopback-only (the zero policy) is the default.
func resolveBindPolicy(allow bool, allowedHosts []string, portRange string, controlPlane bool) (runtimecfg.BindPolicy, error) {
	if !allow {
		if portRange != "" {
			return runtimecfg.BindPolicy{}, errors.New("-listener-port-range requires -allow-non-loopback-bind")
		}
		return runtimecfg.BindPolicy{}, nil
	}
	if len(allowedHosts) == 0 {
		return runtimecfg.BindPolicy{}, errors.New("-allow-non-loopback-bind requires at least one -allowed-host")
	}
	// localhost and loopback IPs always pass the Host check, so an allowlist
	// made only of them adds nothing.
	if !slices.ContainsFunc(allowedHosts, func(h string) bool { return !runtimecfg.IsLoopbackName(h) }) {
		return runtimecfg.BindPolicy{}, errors.New("-allow-non-loopback-bind requires at least one -allowed-host that is not localhost or a loopback IP (those always pass)")
	}
	if !controlPlane {
		if portRange != "" {
			return runtimecfg.BindPolicy{}, errors.New("-listener-port-range applies only to control-plane mode (-local-admin-addr), not fixed-listener mode")
		}
		return runtimecfg.NewWildcardBindPolicy(0, 0), nil
	}
	if portRange == "" {
		return runtimecfg.BindPolicy{}, errors.New("-allow-non-loopback-bind in control-plane mode requires -listener-port-range LOW-HIGH")
	}
	low, high, err := runtimecfg.ParsePortRange(portRange)
	if err != nil {
		return runtimecfg.BindPolicy{}, err
	}
	return runtimecfg.NewWildcardBindPolicy(low, high), nil
}

// logNonLoopbackBind emits the startup warning when wildcard binds are enabled.
func logNonLoopbackBind(logf func(string, ...any), policy runtimecfg.BindPolicy, allowedHosts []string) {
	if policy.AllowsWildcard() {
		logf("warn: non-loopback bind enabled; allowed hosts: %s", strings.Join(allowedHosts, ", "))
	}
}

// adminBindPolicy is the policy for the admin server's own address: the
// listener port range constrains created listeners, not the admin port.
func adminBindPolicy(policy runtimecfg.BindPolicy) runtimecfg.BindPolicy {
	if policy.AllowsWildcard() {
		return runtimecfg.NewWildcardBindPolicy(0, 0)
	}
	return runtimecfg.BindPolicy{}
}

// advertisedAddr maps a wildcard bind host to localhost so the reported URL is
// usable by clients; other addresses are returned unchanged.
func advertisedAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !runtimecfg.IsWildcardHost(host) {
		return addr
	}
	return net.JoinHostPort("localhost", port)
}
