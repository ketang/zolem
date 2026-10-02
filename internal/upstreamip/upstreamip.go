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
	nat64Prefix     = netip.MustParsePrefix("64:ff9b::/96")
	nat64LocalUse   = netip.MustParsePrefix("64:ff9b:1::/48")
	teredoPrefix    = netip.MustParsePrefix("2001::/32")
	sixToFourPrefix = netip.MustParsePrefix("2002::/16")
)

// Embedded returns the IPv4 address embedded in addr when addr is an
// IPv4-mapped (::ffff:a.b.c.d), NAT64 (64:ff9b::/96), 6to4 (2002::/16), or
// deprecated IPv4-compatible (::a.b.c.d) IPv6 address, and addr itself otherwise.
// :: and ::1 are never treated as IPv4-compatible.
func Embedded(addr netip.Addr) netip.Addr {
	v4, _ := unwrap(addr)
	return v4
}

// unwrap is Embedded plus whether the IPv4 address was reached through a
// translation/tunnel wrapper (NAT64, 6to4, IPv4-compatible) rather than a
// plain IPv4-mapped address.
func unwrap(addr netip.Addr) (out netip.Addr, tunneled bool) {
	addr = addr.Unmap()
	if !addr.Is6() {
		return addr, false
	}
	b := addr.As16()
	if nat64Prefix.Contains(addr.WithZone("")) {
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	}
	if sixToFourPrefix.Contains(addr.WithZone("")) {
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	for _, x := range b[:12] {
		if x != 0 {
			return addr, false
		}
	}
	if addr == netip.IPv6Unspecified() || addr == netip.IPv6Loopback() {
		return addr, false
	}
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
}

// Classify classifies addr, first resolving any embedded IPv4 address.
// An embedded loopback/private IPv4 reached through a NAT64, 6to4, or
// IPv4-compatible wrapper is classified Public, not Private: those forms
// route through a gateway/relay and are not the local address, so they
// require allow_external_ollama_upstream. Embedded link-local, unspecified,
// and multicast addresses stay Blocked. Plain IPv4-mapped addresses are
// classified as the IPv4 address they are.
func Classify(addr netip.Addr) Class {
	if addr.Zone() != "" {
		return Blocked
	}
	addr, tunneled := unwrap(addr)
	switch {
	case addr.Is6() && (nat64LocalUse.Contains(addr) || teredoPrefix.Contains(addr)):
		return Blocked
	case addr.IsLoopback():
		return tunnelDowngrade(Private, tunneled)
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast(), addr.IsInterfaceLocalMulticast(), addr.IsUnspecified(), addr.IsMulticast():
		return Blocked
	case addr.Is4() && addr.As4()[0] == 0:
		return Blocked
	case addr.IsPrivate():
		return tunnelDowngrade(Private, tunneled)
	default:
		return Public
	}
}

func tunnelDowngrade(c Class, tunneled bool) Class {
	if tunneled {
		return Public
	}
	return c
}
