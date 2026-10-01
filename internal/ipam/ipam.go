// Package ipam allocates overlay addresses from the customisable network plan.
package ipam

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
)

// Plan is the address plan of a network.
type Plan struct {
	IPv4 netip.Prefix
	IPv6 netip.Prefix // /48 ULA; hosts use the first /64
	// Reserved addresses or ranges that are never auto-assigned.
	Reserved []netip.Prefix
}

var (
	ErrExhausted = errors.New("address pool exhausted")
	cgnat        = netip.MustParsePrefix("100.64.0.0/10")
	rfc1918      = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	ula = netip.MustParsePrefix("fc00::/7")
)

// DefaultIPv4 is the default overlay IPv4 range.
const DefaultIPv4 = "100.80.0.0/16"

// RandomULA returns a random RFC 4193 /48 prefix (fdXX:XXXX:XXXX::/48).
func RandomULA() netip.Prefix {
	var b [16]byte
	b[0] = 0xfd
	if _, err := rand.Read(b[1:6]); err != nil {
		panic(err)
	}
	return netip.PrefixFrom(netip.AddrFrom16(b), 48)
}

// ValidateIPv4Range checks that p is a private/CGNAT IPv4 range between /8 and /28.
func ValidateIPv4Range(p netip.Prefix) error {
	if !p.IsValid() || !p.Addr().Is4() {
		return errors.New("must be an IPv4 CIDR")
	}
	if p != p.Masked() {
		return fmt.Errorf("%s is not a network address (did you mean %s?)", p, p.Masked())
	}
	if p.Bits() < 8 || p.Bits() > 28 {
		return errors.New("prefix length must be between /8 and /28")
	}
	if within(p, cgnat) {
		return nil
	}
	for _, r := range rfc1918 {
		if within(p, r) {
			return nil
		}
	}
	return errors.New("must be inside 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or 100.64.0.0/10")
}

func ValidateIPv6Range(p netip.Prefix) error {
	if !p.IsValid() || !p.Addr().Is6() {
		return errors.New("must be an IPv6 CIDR")
	}
	if !within(p, ula) {
		return errors.New("must be a unique local address range (fc00::/7)")
	}
	if p.Bits() > 64 {
		return errors.New("prefix length must be /64 or shorter")
	}
	return nil
}

func within(inner, outer netip.Prefix) bool {
	return outer.Bits() <= inner.Bits() && outer.Contains(inner.Addr())
}

// Overlaps reports whether two prefixes share any address.
func Overlaps(a, b netip.Prefix) bool { return a.Overlaps(b) }

// GatewayIPv4 is the first usable host address, reserved for the built-in gateway.
func (p Plan) GatewayIPv4() netip.Addr { return p.IPv4.Masked().Addr().Next() }

func (p Plan) GatewayIPv6() netip.Addr { return p.v6ForOffset(1) }

// Capacity is the number of assignable IPv4 host addresses (excluding network, broadcast, gateway).
func (p Plan) Capacity() int {
	hostBits := 32 - p.IPv4.Bits()
	return (1 << hostBits) - 3
}

// Allocate returns the next free IPv4 address (and the matching IPv6 address) not present in used.
// pool optionally restricts allocation to a sub-range of the network.
func (p Plan) Allocate(used map[netip.Addr]bool, pool netip.Prefix) (netip.Addr, netip.Addr, error) {
	rng := p.IPv4.Masked()
	if pool.IsValid() {
		if !within(pool, rng) {
			return netip.Addr{}, netip.Addr{}, fmt.Errorf("pool %s is outside network %s", pool, rng)
		}
		rng = pool.Masked()
	}
	last := lastAddr(p.IPv4.Masked())
	gw := p.GatewayIPv4()
	for a := rng.Addr(); rng.Contains(a); a = a.Next() {
		if a == p.IPv4.Masked().Addr() || a == last || a == gw || used[a] || p.isReserved(a) {
			continue
		}
		return a, p.IPv6For(a), nil
	}
	return netip.Addr{}, netip.Addr{}, ErrExhausted
}

// Check validates a requested static address.
func (p Plan) Check(a netip.Addr, used map[netip.Addr]bool) error {
	switch {
	case !a.Is4() || !p.IPv4.Contains(a):
		return fmt.Errorf("%s is not inside %s", a, p.IPv4)
	case a == p.IPv4.Masked().Addr() || a == lastAddr(p.IPv4.Masked()):
		return fmt.Errorf("%s is the network or broadcast address", a)
	case a == p.GatewayIPv4():
		return fmt.Errorf("%s is reserved for the gateway", a)
	case used[a]:
		return fmt.Errorf("%s is already in use", a)
	}
	return nil
}

func (p Plan) isReserved(a netip.Addr) bool {
	for _, r := range p.Reserved {
		if r.Contains(a) {
			return true
		}
	}
	return false
}

// IPv6For maps an IPv4 address to the IPv6 address with the same host offset.
func (p Plan) IPv6For(v4 netip.Addr) netip.Addr {
	return p.v6ForOffset(offset(p.IPv4.Masked().Addr(), v4))
}

func (p Plan) v6ForOffset(off uint32) netip.Addr {
	b := p.IPv6.Masked().Addr().As16()
	b[12] = byte(off >> 24)
	b[13] = byte(off >> 16)
	b[14] = byte(off >> 8)
	b[15] = byte(off)
	return netip.AddrFrom16(b)
}

// Translate maps an address from this plan to the same host offset in another plan
// (used when re-addressing). Returns false if it does not fit.
func (p Plan) Translate(a netip.Addr, to Plan) (netip.Addr, bool) {
	off := offset(p.IPv4.Masked().Addr(), a)
	n := new(big.Int).SetBytes(to.IPv4.Masked().Addr().AsSlice())
	n.Add(n, big.NewInt(int64(off)))
	b := n.Bytes()
	if len(b) > 4 {
		return netip.Addr{}, false
	}
	var arr [4]byte
	copy(arr[4-len(b):], b)
	out := netip.AddrFrom4(arr)
	if !to.IPv4.Contains(out) || out == lastAddr(to.IPv4.Masked()) {
		return netip.Addr{}, false
	}
	return out, true
}

func offset(base, a netip.Addr) uint32 {
	b4, a4 := base.As4(), a.As4()
	return be32(a4) - be32(b4)
}

func be32(b [4]byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	v := be32(b) | (1<<(32-p.Bits()) - 1)
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
