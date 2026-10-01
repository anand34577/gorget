package tunx

import (
	"encoding/binary"
	"net/netip"
)

const (
	protoICMP   = 1
	protoTCP    = 6
	protoUDP    = 17
	protoICMPv6 = 58
)

// packet is the parsed 5-tuple of an IP packet.
type packet struct {
	src, dst     netip.Addr
	proto        uint8
	sport, dport uint16
	hasPorts     bool
	fragment     bool // non-first fragment: no transport header
	payloadOff   int  // offset of the transport header
}

func parse(b []byte) (packet, bool) {
	var p packet
	if len(b) < 1 {
		return p, false
	}
	switch b[0] >> 4 {
	case 4:
		if len(b) < 20 {
			return p, false
		}
		ihl := int(b[0]&0x0f) * 4
		if ihl < 20 || len(b) < ihl {
			return p, false
		}
		p.src = netip.AddrFrom4([4]byte(b[12:16]))
		p.dst = netip.AddrFrom4([4]byte(b[16:20]))
		p.proto = b[9]
		frag := binary.BigEndian.Uint16(b[6:8]) & 0x1fff
		p.fragment = frag != 0
		p.payloadOff = ihl
	case 6:
		if len(b) < 40 {
			return p, false
		}
		p.src = netip.AddrFrom16([16]byte(b[8:24]))
		p.dst = netip.AddrFrom16([16]byte(b[24:40]))
		p.proto = b[6]
		p.payloadOff = 40
		// Skip common extension headers (hop-by-hop, routing, destination options, fragment).
		for i := 0; i < 4; i++ {
			switch p.proto {
			case 0, 43, 60:
				if len(b) < p.payloadOff+2 {
					return p, false
				}
				next := b[p.payloadOff]
				p.payloadOff += (int(b[p.payloadOff+1]) + 1) * 8
				p.proto = next
				continue
			case 44:
				if len(b) < p.payloadOff+8 {
					return p, false
				}
				next := b[p.payloadOff]
				if binary.BigEndian.Uint16(b[p.payloadOff+2:])&0xfff8 != 0 {
					p.fragment = true
				}
				p.payloadOff += 8
				p.proto = next
				continue
			}
			break
		}
	default:
		return p, false
	}
	if p.fragment {
		return p, true
	}
	t := b[p.payloadOff:]
	switch p.proto {
	case protoTCP, protoUDP:
		if len(t) < 4 {
			return p, false
		}
		p.sport, p.dport = binary.BigEndian.Uint16(t[0:2]), binary.BigEndian.Uint16(t[2:4])
		p.hasPorts = true
	case protoICMP, protoICMPv6:
		// Use the echo identifier as both "ports" so replies match requests.
		if len(t) >= 8 && isEcho(p.proto, t[0]) {
			id := binary.BigEndian.Uint16(t[4:6])
			p.sport, p.dport = id, id
		}
	}
	return p, true
}

func isEcho(proto, typ uint8) bool {
	if proto == protoICMP {
		return typ == 8 || typ == 0
	}
	return typ == 128 || typ == 129
}

// buildUDPReply wraps payload in an IP/UDP packet from (src,sport) to (dst,dport).
func buildUDPReply(src, dst netip.Addr, sport, dport uint16, payload []byte) []byte {
	udpLen := 8 + len(payload)
	if src.Is4() {
		b := make([]byte, 20+udpLen)
		b[0] = 0x45
		binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
		b[8] = 64
		b[9] = protoUDP
		s, d := src.As4(), dst.As4()
		copy(b[12:16], s[:])
		copy(b[16:20], d[:])
		binary.BigEndian.PutUint16(b[10:], checksum(b[:20], 0))
		u := b[20:]
		binary.BigEndian.PutUint16(u[0:], sport)
		binary.BigEndian.PutUint16(u[2:], dport)
		binary.BigEndian.PutUint16(u[4:], uint16(udpLen))
		copy(u[8:], payload)
		ph := pseudoSum(s[:], d[:], udpLen)
		binary.BigEndian.PutUint16(u[6:], nonZero(checksum(u, ph)))
		return b
	}
	b := make([]byte, 40+udpLen)
	b[0] = 0x60
	binary.BigEndian.PutUint16(b[4:], uint16(udpLen))
	b[6] = protoUDP
	b[7] = 64
	s, d := src.As16(), dst.As16()
	copy(b[8:24], s[:])
	copy(b[24:40], d[:])
	u := b[40:]
	binary.BigEndian.PutUint16(u[0:], sport)
	binary.BigEndian.PutUint16(u[2:], dport)
	binary.BigEndian.PutUint16(u[4:], uint16(udpLen))
	copy(u[8:], payload)
	ph := pseudoSum(s[:], d[:], udpLen)
	binary.BigEndian.PutUint16(u[6:], nonZero(checksum(u, ph)))
	return b
}

func nonZero(c uint16) uint16 {
	if c == 0 {
		return 0xffff
	}
	return c
}

func pseudoSum(src, dst []byte, l int) uint32 {
	var sum uint32
	for i := 0; i+1 < len(src); i += 2 {
		sum += uint32(src[i])<<8 | uint32(src[i+1])
		sum += uint32(dst[i])<<8 | uint32(dst[i+1])
	}
	sum += protoUDP
	sum += uint32(l)
	return sum
}

func checksum(b []byte, initial uint32) uint16 {
	sum := initial
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
