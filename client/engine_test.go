package client

import (
	"net/netip"
	"testing"
)

func TestSubtractPrefixes(t *testing.T) {
	out := subtractPrefixes([]netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, lanRanges)
	for _, p := range out {
		for _, lan := range []string{"10.1.2.3", "172.20.0.1", "192.168.1.1", "169.254.1.1"} {
			if p.Contains(netip.MustParseAddr(lan)) {
				t.Fatalf("%s still routed via %s", lan, p)
			}
		}
	}
	covered := false
	for _, p := range out {
		if p.Contains(netip.MustParseAddr("8.8.8.8")) {
			covered = true
		}
	}
	if !covered || len(out) < 10 {
		t.Fatalf("internet not covered: %v", out)
	}
}
