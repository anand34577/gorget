package ipam

import (
	"net/netip"
	"testing"
)

func TestAllocate(t *testing.T) {
	p := Plan{IPv4: netip.MustParsePrefix("100.80.0.0/29"), IPv6: netip.MustParsePrefix("fd12:3456:789a::/48")}
	used := map[netip.Addr]bool{}
	var got []string
	for {
		a, v6, err := p.Allocate(used, netip.Prefix{})
		if err != nil {
			if err != ErrExhausted {
				t.Fatal(err)
			}
			break
		}
		used[a] = true
		got = append(got, a.String())
		if p.IPv6For(a) != v6 {
			t.Fatal("v6 mismatch")
		}
	}
	// .0 network, .1 gateway, .7 broadcast excluded
	want := []string{"100.80.0.2", "100.80.0.3", "100.80.0.4", "100.80.0.5", "100.80.0.6"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
	if p.Capacity() != 5 {
		t.Fatalf("capacity %d", p.Capacity())
	}
	if p.IPv6For(netip.MustParseAddr("100.80.0.5")).String() != "fd12:3456:789a::5" {
		t.Fatal(p.IPv6For(netip.MustParseAddr("100.80.0.5")))
	}
}

func TestValidate(t *testing.T) {
	for _, s := range []string{"100.80.0.0/16", "10.0.0.0/8", "192.168.100.0/24", "172.20.0.0/16"} {
		if err := ValidateIPv4Range(netip.MustParsePrefix(s)); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
	for _, s := range []string{"8.8.8.0/24", "100.80.0.1/16", "10.0.0.0/30", "0.0.0.0/0"} {
		if err := ValidateIPv4Range(netip.MustParsePrefix(s)); err == nil {
			t.Errorf("%s: expected error", s)
		}
	}
	if err := ValidateIPv6Range(RandomULA()); err != nil {
		t.Fatal(err)
	}
}

func TestTranslate(t *testing.T) {
	a := Plan{IPv4: netip.MustParsePrefix("100.80.0.0/16")}
	b := Plan{IPv4: netip.MustParsePrefix("10.20.0.0/16")}
	out, ok := a.Translate(netip.MustParseAddr("100.80.3.7"), b)
	if !ok || out.String() != "10.20.3.7" {
		t.Fatal(out, ok)
	}
	small := Plan{IPv4: netip.MustParsePrefix("10.20.0.0/24")}
	if _, ok := a.Translate(netip.MustParseAddr("100.80.3.7"), small); ok {
		t.Fatal("should not fit")
	}
}
