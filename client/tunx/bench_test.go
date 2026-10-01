package tunx

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// tcpPacket builds a minimal IPv4/TCP packet.
func tcpPacket(src, dst netip.Addr, sport, dport uint16) []byte {
	b := make([]byte, 40)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], 40)
	b[8], b[9] = 64, protoTCP
	s, d := src.As4(), dst.As4()
	copy(b[12:], s[:])
	copy(b[16:], d[:])
	binary.BigEndian.PutUint16(b[20:], sport)
	binary.BigEndian.PutUint16(b[22:], dport)
	return b
}

// BenchmarkFilter measures the per-packet cost of the inbound firewall with a realistic rule set.
func BenchmarkFilter(b *testing.B) {
	var rules []Rule
	for i := 0; i < 50; i++ {
		src := netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 80, byte(i), 0}), 24)
		rules = append(rules, Rule{Src: []netip.Prefix{src}, Dst: []netip.Prefix{netip.MustParsePrefix("100.80.255.1/32")}, Ports: [][2]uint16{{22, 22}, {443, 443}}, Protos: []uint8{protoTCP}})
	}
	f := NewFilter(rules)
	pkt := tcpPacket(netip.MustParseAddr("100.80.49.7"), netip.MustParseAddr("100.80.255.1"), 40000, 443)
	p, ok := parse(pkt)
	if !ok {
		b.Fatal("parse")
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(pkt)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !f.allows(p) {
			b.Fatal("should be allowed")
		}
	}
}

func BenchmarkParse(b *testing.B) {
	pkt := tcpPacket(netip.MustParseAddr("100.80.0.2"), netip.MustParseAddr("100.80.0.3"), 1234, 80)
	b.ReportAllocs()
	b.SetBytes(int64(len(pkt)))
	for i := 0; i < b.N; i++ {
		if _, ok := parse(pkt); !ok {
			b.Fatal("parse")
		}
	}
}

// BenchmarkFilterLargeGroup is a rule whose source is a group of 5000 devices (one
// address each), the shape access rules take in large networks.
func BenchmarkFilterLargeGroup(b *testing.B) {
	var src []netip.Prefix
	for i := 0; i < 5000; i++ {
		src = append(src, netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 80, byte(i >> 8), byte(i)}), 32))
	}
	f := NewFilter([]Rule{{Src: src, Dst: []netip.Prefix{netip.MustParsePrefix("100.80.255.1/32")}}})
	p, ok := parse(tcpPacket(netip.MustParseAddr("100.80.19.135"), netip.MustParseAddr("100.80.255.1"), 40000, 443))
	if !ok {
		b.Fatal("parse")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !f.allows(p) {
			b.Fatal("should be allowed")
		}
	}
}
