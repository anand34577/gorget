package stun

import (
	"net/netip"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"203.0.113.7:51820", "[2001:db8::1]:4242"} {
		from := netip.MustParseAddrPort(s)
		resp, ok := Response(Request([12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}), from)
		if !ok {
			t.Fatal("no response")
		}
		got, err := ParseResponse(resp)
		if err != nil || got != from {
			t.Fatalf("%s: got %v %v", s, got, err)
		}
	}
	if _, ok := Response([]byte("garbage that is long enough...."), netip.MustParseAddrPort("1.2.3.4:5")); ok {
		t.Fatal("accepted garbage")
	}
}
