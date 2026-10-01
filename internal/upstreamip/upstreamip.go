// Package upstreamip holds the single IP-safety classification contract for
// Ollama upstream targets. It is shared by profile-create validation
// (internal/runtime) and dial-time enforcement (internal/ollama) so the two
// cannot drift.
package upstreamip

import "net/netip"

// Class is the policy class of an upstream IP address.
type Class int

const (
	// Private covers loopback and private (RFC1918/RFC4193) addresses:
	// always permitted.
	Private Class = iota
	// Public covers all other routable addresses: permitted only when the
	// policy allows external upstreams.
	Public
	// Blocked covers link-local, unspecified (including all of 0.0.0.0/8),
	// and multicast addresses: never permitted, regardless of policy.
	Blocked
)

var nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")

// Embedded returns the IPv4 address embedded in addr when addr is an
// IPv4-mapped (::ffff:a.b.c.d), NAT64 (64:ff9b::/96), or deprecated
// IPv4-compatible (::a.b.c.d) IPv6 address, and addr itself otherwise.
// :: and ::1 are never treated as IPv4-compatible.
func Embedded(addr netip.Addr) netip.Addr {
	addr = addr.Unmap()
	if !addr.Is6() {
		return addr
	}
	b := addr.As16()
	if nat64Prefix.Contains(addr.WithZone("")) {
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	for _, x := range b[:12] {
		if x != 0 {
			return addr
		}
	}
	if addr == netip.IPv6Unspecified() || addr == netip.IPv6Loopback() {
		return addr
	}
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
}

// Classify classifies addr, first resolving any embedded IPv4 address.
func Classify(addr netip.Addr) Class {
	addr = Embedded(addr)
	switch {
	case addr.IsLoopback():
		return Private
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast(), addr.IsInterfaceLocalMulticast(), addr.IsUnspecified(), addr.IsMulticast():
		return Blocked
	case addr.Is4() && addr.As4()[0] == 0:
		return Blocked
	case addr.IsPrivate():
		return Private
	default:
		return Public
	}
}
