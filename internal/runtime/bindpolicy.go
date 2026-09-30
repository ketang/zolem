package runtimecfg

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// BindPolicy decides which listen addresses are acceptable. The zero value is
// loopback-only, which is zolem's default. NewWildcardBindPolicy opts in to
// the wildcard hosts 0.0.0.0 and :: (for containers); specific non-loopback
// IPs are never accepted.
type BindPolicy struct {
	allowWildcard bool
	portLow       int
	portHigh      int
}

// NewWildcardBindPolicy returns a policy that accepts loopback and wildcard
// hosts. When high > 0, every address must also use a port in [low, high].
func NewWildcardBindPolicy(low, high int) BindPolicy {
	return BindPolicy{allowWildcard: true, portLow: low, portHigh: high}
}

// AllowsWildcard reports whether wildcard hosts are accepted.
func (p BindPolicy) AllowsWildcard() bool { return p.allowWildcard }

// ValidateAddr checks host (and port range, when configured) of addr.
func (p BindPolicy) ValidateAddr(addr string) error {
	if p.allowWildcard {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return err
		}
		if !IsWildcardHost(host) {
			if err := validateLoopbackAddr(addr); err != nil {
				return errors.New("listener addr must bind to localhost, a loopback IP, 0.0.0.0, or ::")
			}
		}
	} else if err := validateLoopbackAddr(addr); err != nil {
		return err
	}
	if p.portHigh > 0 {
		_, portStr, _ := net.SplitHostPort(addr)
		port, err := strconv.Atoi(portStr)
		if err != nil || port < p.portLow || port > p.portHigh {
			return fmt.Errorf("listener port %s outside -listener-port-range %d-%d", portStr, p.portLow, p.portHigh)
		}
	}
	return nil
}

// IsWildcardHost reports whether host is the IPv4 or IPv6 unspecified address.
func IsWildcardHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// ParsePortRange parses "LOW-HIGH" into a valid 1..65535 range.
func ParsePortRange(s string) (low, high int, err error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return 0, 0, fmt.Errorf("invalid port range %q: want LOW-HIGH", s)
	}
	low, err1 := strconv.Atoi(a)
	high, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("invalid port range %q: want LOW-HIGH", s)
	}
	if low < 1 || high > 65535 || low > high {
		return 0, 0, fmt.Errorf("invalid port range %q: want 1 <= LOW <= HIGH <= 65535", s)
	}
	return low, high, nil
}

// ValidateListenerSpecWithPolicy is ValidateListenerSpec under an explicit
// bind policy.
func ValidateListenerSpecWithPolicy(spec ListenerSpec, policy BindPolicy) error {
	if err := validateListenerSpecFields(spec); err != nil {
		return err
	}
	return policy.ValidateAddr(spec.Addr)
}
