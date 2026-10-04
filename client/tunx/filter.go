package tunx

import (
	"net/netip"
	"sync"
	"time"
)

// Rule allows inbound traffic from Src to Dst (optionally limited by ports/protocols).
type Rule struct {
	Src    []netip.Prefix
	Dst    []netip.Prefix
	Ports  [][2]uint16 // empty = all
	Protos []uint8     // empty = any
}

// Filter is an immutable inbound rule set. Everything not allowed is dropped,
// except replies to connections this device opened (tracked by Conntrack).
type Filter struct {
	rules []compiledRule
}

type compiledRule struct {
	Rule
	src, dst addrSet
}

// addrSet answers "is this address covered" quickly. Rules built from groups can
// list thousands of single addresses (/32, /128), so those go into a hash set and
// only real networks are scanned.
type addrSet struct {
	hosts map[netip.Addr]struct{}
	nets  []netip.Prefix
}

// hostSetMin is the number of single addresses from which a hash set beats a scan.
const hostSetMin = 8

func newAddrSet(ps []netip.Prefix) addrSet {
	var s addrSet
	nHosts := 0
	for _, p := range ps {
		if p.IsSingleIP() {
			nHosts++
		}
	}
	if nHosts >= hostSetMin {
		s.hosts = make(map[netip.Addr]struct{}, nHosts)
	}
	for _, p := range ps {
		switch {
		case s.hosts != nil && p.IsSingleIP():
			s.hosts[p.Addr()] = struct{}{}
		default:
			s.nets = append(s.nets, p)
		}
	}
	return s
}

func (s *addrSet) contains(a netip.Addr) bool {
	if s.hosts != nil {
		if _, ok := s.hosts[a]; ok {
			return true
		}
	}
	for _, p := range s.nets {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func NewFilter(rules []Rule) *Filter {
	f := &Filter{rules: make([]compiledRule, len(rules))}
	for i, r := range rules {
		f.rules[i] = compiledRule{Rule: r, src: newAddrSet(r.Src), dst: newAddrSet(r.Dst)}
	}
	return f
}

func (f *Filter) allows(p packet) bool {
	if f == nil {
		return false
	}
	for i := range f.rules {
		r := &f.rules[i]
		if !r.src.contains(p.src) || !r.dst.contains(p.dst) {
			continue
		}
		if len(r.Protos) > 0 && !hasProto(r.Protos, p.proto) {
			continue
		}
		if len(r.Ports) == 0 || p.fragment || !p.hasPorts {
			// ICMP and fragments are allowed once the hosts are allowed.
			return true
		}
		for _, pr := range r.Ports {
			if p.dport >= pr[0] && p.dport <= pr[1] {
				return true
			}
		}
	}
	return false
}

func hasProto(ps []uint8, p uint8) bool {
	for _, x := range ps {
		if x == p || (x == protoICMP && p == protoICMPv6) {
			return true
		}
	}
	return false
}

// flowKey identifies a connection from this device's point of view.
type flowKey struct {
	proto         uint8
	local, remote netip.Addr
	lport, rport  uint16
}

// Conntrack remembers outbound flows so their replies are let back in.
type Conntrack struct {
	mu    sync.Mutex
	flows map[flowKey]time.Time
	last  time.Time
}

const maxFlows = 65536

func NewConntrack() *Conntrack { return &Conntrack{flows: map[flowKey]time.Time{}} }

func ttl(proto uint8) time.Duration {
	switch proto {
	case protoTCP:
		return 10 * time.Minute
	case protoUDP:
		return 3 * time.Minute
	}
	return 30 * time.Second
}

// Outbound records a packet leaving this device.
func (c *Conntrack) Outbound(p packet) {
	if p.fragment {
		return
	}
	now := time.Now()
	k := flowKey{proto: p.proto, local: p.src, remote: p.dst, lport: p.sport, rport: p.dport}
	exp := now.Add(ttl(p.proto))
	c.mu.Lock()
	defer c.mu.Unlock()
	// Most packets belong to a flow seen moments ago: skip the map write unless the
	// expiry moved noticeably (keeps the hot path to one lookup).
	if old, ok := c.flows[k]; ok && exp.Sub(old) < time.Second {
		return
	}
	// Sweep every 30s; when the table is full, at most once a second (a full table
	// must not turn every packet into a 65536-entry scan).
	if since := now.Sub(c.last); since > 30*time.Second || (len(c.flows) >= maxFlows && since > time.Second) {
		for fk, e := range c.flows {
			if now.After(e) {
				delete(c.flows, fk)
			}
		}
		c.last = now
	}
	if len(c.flows) >= maxFlows {
		if _, ok := c.flows[k]; !ok {
			return // full of live flows: new ones wait for the next sweep
		}
	}
	c.flows[k] = exp
}

// IsReply reports whether an inbound packet belongs to an outbound flow.
func (c *Conntrack) IsReply(p packet) bool {
	k := flowKey{proto: p.proto, local: p.dst, remote: p.src, lport: p.dport, rport: p.sport}
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.flows[k]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(c.flows, k)
		return false
	}
	return true
}

// IsErrorFor reports whether p is an ICMP error (unreachable, packet too big, time
// exceeded, bad parameter) about a flow this device opened. Without these, IPv6 attempts
// that the exit node can't carry hang instead of failing fast, and path-MTU discovery
// breaks (large downloads stall).
func (c *Conntrack) IsErrorFor(p packet, b []byte) bool {
	if (p.proto != protoICMP && p.proto != protoICMPv6) || p.fragment || len(b) < p.payloadOff+8+20 {
		return false
	}
	t := b[p.payloadOff:]
	switch p.proto {
	case protoICMP:
		if t[0] != 3 && t[0] != 4 && t[0] != 11 && t[0] != 12 {
			return false
		}
	default:
		if t[0] < 1 || t[0] > 4 {
			return false
		}
	}
	in, ok := parseInner(t[8:])
	if !ok {
		return false
	}
	// The quoted packet is one we sent: swap it so it looks like a reply to that flow.
	return c.IsReply(packet{src: in.dst, dst: in.src, proto: in.proto, sport: in.dport, dport: in.sport, hasPorts: in.hasPorts})
}

// parseInner reads the packet quoted inside an ICMP error. It may be cut short, so only
// the addresses, protocol and (when present) the first four transport bytes are needed.
func parseInner(b []byte) (packet, bool) {
	if len(b) < 20 {
		return packet{}, false
	}
	var p packet
	var t []byte
	switch b[0] >> 4 {
	case 4:
		ihl := int(b[0]&0x0f) * 4
		if ihl < 20 || len(b) < ihl {
			return p, false
		}
		p.src = netip.AddrFrom4([4]byte(b[12:16]))
		p.dst = netip.AddrFrom4([4]byte(b[16:20]))
		p.proto, t = b[9], b[ihl:]
	case 6:
		if len(b) < 40 {
			return p, false
		}
		p.src = netip.AddrFrom16([16]byte(b[8:24]))
		p.dst = netip.AddrFrom16([16]byte(b[24:40]))
		p.proto, t = b[6], b[40:]
	default:
		return p, false
	}
	switch p.proto {
	case protoTCP, protoUDP:
		if len(t) < 4 {
			return p, false
		}
		p.sport, p.dport = uint16(t[0])<<8|uint16(t[1]), uint16(t[2])<<8|uint16(t[3])
		p.hasPorts = true
	case protoICMP, protoICMPv6:
		if len(t) >= 6 && isEcho(p.proto, t[0]) {
			id := uint16(t[4])<<8 | uint16(t[5])
			p.sport, p.dport = id, id
		}
	}
	return p, true
}
