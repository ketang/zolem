package upstreamip

import (
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		addr string
		want Class
	}{
		{"127.0.0.1", Private},
		{"::1", Private},
		{"10.0.0.1", Private},
		{"192.168.1.1", Private},
		{"fd00::1", Private},
		{"8.8.8.8", Public},
		{"2001:4860:4860::8888", Public},
		{"169.254.169.254", Blocked},
		{"fe80::1", Blocked},
		{"224.0.0.1", Blocked},
		{"ff02::1", Blocked},
		{"0.0.0.0", Blocked},
		{"::", Blocked},
		// 0.0.0.0/8 routes to the local host on some platforms.
		{"0.0.0.1", Blocked},
		{"0.255.255.255", Blocked},
		// IPv4-mapped.
		{"::ffff:169.254.169.254", Blocked},
		{"::ffff:0.0.0.1", Blocked},
		{"::ffff:8.8.8.8", Public},
		{"::ffff:127.0.0.1", Private},
		// NAT64 (64:ff9b::/96) embeds the IPv4 address.
		{"64:ff9b::7f00:1", Private},
		{"64:ff9b::a00:1", Private},
		{"64:ff9b::a9fe:a9fe", Blocked},
		{"64:ff9b::1", Blocked},
		{"64:ff9b::808:808", Public},
		// Deprecated IPv4-compatible (::a.b.c.d).
		{"::7f00:1", Private},
		{"::a9fe:a9fe", Blocked},
		{"::808:808", Public},
		{"::a00:1", Private},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := Classify(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Fatalf("Classify(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
