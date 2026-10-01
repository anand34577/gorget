package portmap

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"
)

// ---------- NAT-PMP (RFC 6886) ----------

func natpmpMapRequest(port uint16, lifetime time.Duration) []byte {
	b := make([]byte, 12)
	b[0], b[1] = 0, 1 // version 0, opcode: map UDP
	binary.BigEndian.PutUint16(b[4:], port)
	binary.BigEndian.PutUint16(b[6:], port) // suggested external port
	binary.BigEndian.PutUint32(b[8:], uint32(lifetime/time.Second))
	return b
}

func natpmpMap(ctx context.Context, gw netip.Addr, port uint16, lifetime time.Duration) (netip.AddrPort, error) {
	resp, err := exchange(ctx, gw, 5351, natpmpMapRequest(port, lifetime), func(r []byte) bool {
		return len(r) >= 16 && r[0] == 0 && r[1] == 129 && binary.BigEndian.Uint16(r[8:]) == port
	})
	if err != nil {
		return netip.AddrPort{}, err
	}
	if code := binary.BigEndian.Uint16(resp[2:]); code != 0 {
		return netip.AddrPort{}, fmt.Errorf("NAT-PMP refused the mapping (result %d)", code)
	}
	if lifetime == 0 {
		return netip.AddrPort{}, nil
	}
	ext := binary.BigEndian.Uint16(resp[10:])
	// The public address needs its own request.
	ip, err := exchange(ctx, gw, 5351, []byte{0, 0}, func(r []byte) bool { return len(r) >= 12 && r[0] == 0 && r[1] == 128 })
	if err != nil {
		return netip.AddrPort{}, err
	}
	if code := binary.BigEndian.Uint16(ip[2:]); code != 0 {
		return netip.AddrPort{}, fmt.Errorf("NAT-PMP could not tell our public address (result %d)", code)
	}
	a := netip.AddrFrom4([4]byte(ip[8:12]))
	if a.IsUnspecified() {
		return netip.AddrPort{}, fmt.Errorf("the router has no public address")
	}
	return netip.AddrPortFrom(a, ext), nil
}

// ---------- PCP (RFC 6887) ----------

func pcpMapRequest(local netip.Addr, port uint16, lifetime time.Duration, nonce [12]byte) []byte {
	b := make([]byte, 60)
	b[0], b[1] = 2, 1 // version 2, opcode MAP
	binary.BigEndian.PutUint32(b[4:], uint32(lifetime/time.Second))
	l := local.As16()
	copy(b[8:24], l[:]) // our address (IPv4-mapped)
	copy(b[24:36], nonce[:])
	b[36] = 17 // UDP
	binary.BigEndian.PutUint16(b[40:], port)
	binary.BigEndian.PutUint16(b[42:], port) // suggested external port
	return b
}

func pcpMap(ctx context.Context, gw, local netip.Addr, port uint16, lifetime time.Duration) (netip.AddrPort, error) {
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])
	resp, err := exchange(ctx, gw, 5351, pcpMapRequest(local, port, lifetime, nonce), func(r []byte) bool {
		return len(r) >= 60 && r[0] == 2 && r[1] == 0x81 && string(r[24:36]) == string(nonce[:])
	})
	if err != nil {
		return netip.AddrPort{}, err
	}
	if code := resp[3]; code != 0 {
		return netip.AddrPort{}, fmt.Errorf("PCP refused the mapping (result %d)", code)
	}
	if lifetime == 0 {
		return netip.AddrPort{}, nil
	}
	extPort := binary.BigEndian.Uint16(resp[42:])
	var ip16 [16]byte
	copy(ip16[:], resp[44:60])
	ext := netip.AddrFrom16(ip16).Unmap()
	if !ext.IsValid() || ext.IsUnspecified() {
		return netip.AddrPort{}, fmt.Errorf("the router gave no public address")
	}
	return netip.AddrPortFrom(ext, extPort), nil
}
