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
