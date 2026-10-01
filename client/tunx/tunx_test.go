package tunx

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

func udp4(src, dst string, sport, dport uint16) []byte {
	return buildUDPReply(netip.MustParseAddr(src), netip.MustParseAddr(dst), sport, dport, []byte("hello"))
}

func tcp4(src, dst string, sport, dport uint16) []byte {
	b := make([]byte, 40)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], 40)
	b[9] = protoTCP
	s, d := netip.MustParseAddr(src).As4(), netip.MustParseAddr(dst).As4()
	copy(b[12:], s[:])
	copy(b[16:], d[:])
	binary.BigEndian.PutUint16(b[20:], sport)
	binary.BigEndian.PutUint16(b[22:], dport)
	return b
}

func TestFilterAndConntrack(t *testing.T) {
	f := NewFilter([]Rule{{
		Src:    []netip.Prefix{netip.MustParsePrefix("100.80.0.0/16")},
		Dst:    []netip.Prefix{netip.MustParsePrefix("100.80.0.2/32")},
		Ports:  [][2]uint16{{22, 22}},
		Protos: []uint8{protoTCP},
	}})
	in := func(b []byte) bool { p, ok := parse(b); return ok && f.allows(p) }
	if !in(tcp4("100.80.0.5", "100.80.0.2", 50000, 22)) {
		t.Fatal("ssh should be allowed")
	}
	if in(tcp4("100.80.0.5", "100.80.0.2", 50000, 80)) {
		t.Fatal("port 80 should be denied")
	}
	if in(udp4("100.80.0.5", "100.80.0.2", 50000, 22)) {
		t.Fatal("udp should be denied")
	}
	if in(tcp4("10.0.0.5", "100.80.0.2", 50000, 22)) {
		t.Fatal("foreign source should be denied")
	}
	ct := NewConntrack()
	out, _ := parse(tcp4("100.80.0.2", "100.80.0.9", 40000, 443))
	ct.Outbound(out)
	reply, _ := parse(tcp4("100.80.0.9", "100.80.0.2", 443, 40000))
	if !ct.IsReply(reply) {
		t.Fatal("reply should be tracked")
	}
	other, _ := parse(tcp4("100.80.0.9", "100.80.0.2", 443, 40001))
	if ct.IsReply(other) {
		t.Fatal("unrelated packet treated as reply")
	}
	var nilFilter *Filter
	if p, _ := parse(tcp4("100.80.0.5", "100.80.0.2", 1, 22)); nilFilter.allows(p) {
		t.Fatal("nil filter must deny")
	}
}

func TestUDPReplyChecksum(t *testing.T) {
	b := udp4("100.100.100.100", "100.80.0.2", 53, 40000)
	if checksum(b[:20], 0) != 0 {
		t.Fatal("bad IPv4 header checksum")
	}
	s, d := netip.MustParseAddr("100.100.100.100").As4(), netip.MustParseAddr("100.80.0.2").As4()
	if checksum(b[20:], pseudoSum(s[:], d[:], len(b)-20)) != 0 {
		t.Fatal("bad UDP checksum")
	}
	p, ok := parse(b)
	if !ok || p.sport != 53 || p.dport != 40000 || p.payloadOff != 20 {
		t.Fatalf("parse %+v", p)
	}
	b6 := buildUDPReply(DNSv6, netip.MustParseAddr("fd00::2"), 53, 1000, []byte("x"))
	if p6, ok := parse(b6); !ok || p6.proto != protoUDP || p6.dport != 1000 {
		t.Fatalf("parse v6 %+v", p6)
	}
}
