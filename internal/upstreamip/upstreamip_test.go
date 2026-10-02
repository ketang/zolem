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
		{"64:ff9b::7f00:1", Public},
		{"64:ff9b::a00:1", Public},
		{"64:ff9b::a9fe:a9fe", Blocked},
		{"64:ff9b::1", Blocked},
		{"64:ff9b::808:808", Public},
		// Deprecated IPv4-compatible (::a.b.c.d).
		{"::7f00:1", Public},
		{"::a9fe:a9fe", Blocked},
		{"::808:808", Public},
		{"::a00:1", Public},
		// 6to4 (2002::/16) embeds the IPv4 address in bits 16-47.
		{"2002:a9fe:a9fe::", Blocked},
		{"2002:7f00:1::1", Public},
		{"2002:808:808::1", Public},
		{"2002:0a00:0001::", Public},
		// NAT64 local-use and Teredo are blocked outright.
		{"64:ff9b:1::1", Blocked},
		{"64:ff9b:1:ffff::1", Blocked},
		{"2001::1", Blocked},
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", Blocked},
		// Zoned literals are refused.
		{"::1%eth0", Blocked},
		{"fd00::1%eth0", Blocked},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := Classify(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Fatalf("Classify(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestZeroClassFailsClosed(t *testing.T) {
	var c Class
	if c != Blocked {
		t.Fatalf("zero Class = %v, want Blocked", c)
	}
}
