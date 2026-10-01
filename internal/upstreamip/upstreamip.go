// Package upstreamip holds the single IP-safety classification contract for
// Ollama upstream targets. It is shared by profile-create validation
// (internal/runtime) and dial-time enforcement (internal/ollama) so the two
// cannot drift.
package upstreamip

import "net/netip"

// Class is the policy class of an upstream IP address.
type Class int

const (
	// Blocked covers link-local, unspecified (including all of 0.0.0.0/8),
	// multicast, zoned, Teredo, and NAT64 local-use addresses: never
	// permitted, regardless of policy. It is the zero value so an
	// uninitialized or ignored-error Class fails closed.
	Blocked Class = iota
	// Private covers loopback and private (RFC1918/RFC4193) addresses:
	// always permitted.
	Private
	// Public covers all other routable addresses: permitted only when the
	// policy allows external upstreams.
	Public
)

var (
	nat64Prefix    = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalUse  = netip.MustParsePrefix("64:ff9b:1::/48")
	teredoPrefix   = netip.MustParsePrefix("2001::/32")
	sixToFourPrefx = netip.MustParsePrefix("2002::/16")
)

// Embedded returns the IPv4 address embedded in addr when addr is an
// IPv4-mapped (::ffff:a.b.c.d), NAT64 (64:ff9b::/96), 6to4 (2002::/16), or
// deprecated IPv4-compatible (::a.b.c.d) IPv6 address, and addr itself otherwise.
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
	if sixToFourPrefx.Contains(addr) {
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
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
	if addr.Zone() != "" {
		return Blocked
	}
	addr = Embedded(addr)
	switch {
	case addr.Is6() && (nat64LocalUse.Contains(addr) || teredoPrefix.Contains(addr)):
		return Blocked
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
