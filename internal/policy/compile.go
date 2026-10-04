package policy

import (
	"fmt"
	"math/bits"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"
)

// Node is the policy engine's view of a device.
type Node struct {
	ID         string
	Name       string
	Kind       string // native, wireguard, gateway
	OwnerID    string // empty for tag-owned / gateway nodes
	OwnerEmail string
	Tags       []string
	Addrs      []netip.Prefix // overlay /32 and /128
	Routes     []netip.Prefix // approved & enabled subnet routes
	ExitNode   bool           // approved exit node
}

// Env is everything outside the policy document needed to compile it.
type Env struct {
	Nodes  []Node
	Groups map[string][]string // group name -> user IDs
	// Overlay network ranges (IPv4 and IPv6), excluded from internet rules.
	Overlay []netip.Prefix
	Now     time.Time
}

// Internet ranges used for exit-node rules.
var (
	InternetV4 = netip.MustParsePrefix("0.0.0.0/0")
	InternetV6 = netip.MustParsePrefix("::/0")
)

// CompiledRule is a rule resolved to concrete nodes and prefixes.
type CompiledRule struct {
	ID          string
	Index       int
	Description string
	SrcNodes    map[string]bool
	SrcPrefixes []netip.Prefix
	// Destination: per-node destination prefixes (node addresses and/or routes the rule covers).
	DstNodes    map[string][]netip.Prefix
	DstPrefixes []netip.Prefix // all destination prefixes (including non-node CIDRs)
	Ports       []PortRange
	Proto       string
	Internet    bool
	// Self rules connect each source node only to nodes of the same owner.
	Self bool
}

// Compiled is the result of compiling a policy against an Env.
type Compiled struct {
	Policy *Policy
	Rules  []CompiledRule
	nodes  map[string]*Node
	// Peer relationships as one bitset row per node: bit j of rows[i] means nodes
	// ids[i] and ids[j] must be configured as WireGuard peers. ids is sorted, so
	// walking a row yields peers in ID order. With n devices this takes n*n bits
	// (3 MB for 5000 devices) instead of a map entry per pair.
	ids  []string
	idx  map[string]int
	rows []bitset
	// exitUsers can route internet traffic through exit nodes.
	exitUsers map[string]bool
	// NextExpiry is the earliest future rule expiry (zero if none) — recompile then.
	NextExpiry time.Time
	private    []netip.Prefix
}

func (p *Policy) Compile(env Env) *Compiled {
	if env.Now.IsZero() {
		env.Now = time.Now()
	}
	c := &Compiled{
		Policy:    p,
		nodes:     make(map[string]*Node, len(env.Nodes)),
		idx:       make(map[string]int, len(env.Nodes)),
		exitUsers: map[string]bool{},
	}
	for i := range env.Nodes {
		c.nodes[env.Nodes[i].ID] = &env.Nodes[i]
	}
	c.ids = make([]string, 0, len(c.nodes))
	for id := range c.nodes {
		c.ids = append(c.ids, id)
	}
	sort.Strings(c.ids)
	words := (len(c.ids) + 63) / 64
	backing := make([]uint64, words*len(c.ids)) // one allocation for all rows
	c.rows = make([]bitset, len(c.ids))
	for i, id := range c.ids {
		c.idx[id] = i
		c.rows[i] = backing[i*words : (i+1)*words : (i+1)*words]
	}
	r := newResolver(p, env)

	for i, rule := range p.ACLs {
		if rule.Disabled {
			continue
		}
		if rule.Expires != "" {
			exp, err := time.Parse(time.RFC3339, rule.Expires)
			if err == nil {
				if !env.Now.Before(exp) {
					continue
				}
				if c.NextExpiry.IsZero() || exp.Before(c.NextExpiry) {
					c.NextExpiry = exp
				}
			}
		}
		srcNodes := map[string]bool{}
		var srcPrefixes []netip.Prefix
		for _, s := range rule.Src {
			nodes, prefixes := r.resolveSrc(s)
			for _, n := range nodes {
				srcNodes[n] = true
			}
			srcPrefixes = append(srcPrefixes, prefixes...)
		}
		srcPrefixes = dedupe(srcPrefixes)

		// Each destination entry becomes its own compiled rule so that ports
		// of one destination never leak to another.
		for _, d := range rule.Dst {
			sel, portStr, err := splitDst(d)
			if err != nil {
				continue
			}
			ports, err := ParsePorts(portStr)
			if err != nil {
				continue
			}
			cr := CompiledRule{
				ID: rule.ID, Index: i, Description: rule.Description, Proto: rule.Proto,
				SrcNodes: srcNodes, SrcPrefixes: srcPrefixes, Ports: ports,
				DstNodes: map[string][]netip.Prefix{},
			}
			switch sel {
			case "autogroup:internet":
				cr.Internet = true
				for _, n := range env.Nodes {
					if n.ExitNode {
						cr.DstNodes[n.ID] = []netip.Prefix{InternetV4, InternetV6}
					}
				}
				cr.DstPrefixes = []netip.Prefix{InternetV4, InternetV6}
			case "autogroup:self":
				cr.Self = true
				for _, n := range env.Nodes {
					if n.OwnerID != "" && len(n.Tags) == 0 {
						cr.DstNodes[n.ID] = appendUnique(cr.DstNodes[n.ID], n.Addrs...)
						cr.DstPrefixes = append(cr.DstPrefixes, n.Addrs...)
					}
				}
				cr.DstPrefixes = dedupe(cr.DstPrefixes)
			default:
				cr.DstNodes = r.resolveDst(sel)
				cr.DstPrefixes = r.dstPrefixes(sel)
			}
			c.Rules = append(c.Rules, cr)

			// Peer relationships: every source peers with every destination.
			if cr.Self {
				// Only pairs with the same owner; owners have few devices each.
				byOwner := map[string][]string{}
				for d := range cr.DstNodes {
					if n := c.nodes[d]; n != nil && n.OwnerID != "" {
						byOwner[n.OwnerID] = append(byOwner[n.OwnerID], d)
					}
				}
				for s := range cr.SrcNodes {
					if n := c.nodes[s]; n != nil {
						for _, d := range byOwner[n.OwnerID] {
							if s != d && sameOwner(n, c.nodes[d]) {
								c.link(s, d)
							}
						}
					}
				}
			} else {
				srcBits, dstBits := c.bits(cr.SrcNodes), c.bitsOf(cr.DstNodes)
				for s := range cr.SrcNodes {
					if i, ok := c.idx[s]; ok {
						c.rows[i].or(dstBits)
					}
				}
				for d := range cr.DstNodes {
					if i, ok := c.idx[d]; ok {
						c.rows[i].or(srcBits)
					}
				}
			}
			if cr.Internet {
				for s := range cr.SrcNodes {
					c.exitUsers[s] = true
				}
			}
		}
	}
	// A node is never its own peer.
	for i := range c.rows {
		c.rows[i].clear(i)
	}
	// Private space that internet rules must never cover: the overlay itself and every subnet route.
	c.private = append(c.private, env.Overlay...)
	for _, n := range env.Nodes {
		c.private = append(c.private, n.Routes...)
	}
	// Never forwardable through an exit node, whatever the rule says: loopback and link-local
	// space includes the cloud metadata service (169.254.169.254) of a VPS exit node.
	c.private = append(c.private, neverForwarded...)
	c.private = dedupe(c.private)
	return c
}

// bitset is a fixed-size set of node indexes.
type bitset []uint64

func (b bitset) set(i int)      { b[i>>6] |= 1 << (uint(i) & 63) }
func (b bitset) clear(i int)    { b[i>>6] &^= 1 << (uint(i) & 63) }
func (b bitset) has(i int) bool { return b[i>>6]&(1<<(uint(i)&63)) != 0 }
func (b bitset) or(o bitset) {
	for i := range b {
		b[i] |= o[i]
	}
}

func (c *Compiled) newBits() bitset { return make(bitset, (len(c.ids)+63)/64) }

func (c *Compiled) bits(ids map[string]bool) bitset {
	b := c.newBits()
	for id := range ids {
		if i, ok := c.idx[id]; ok {
			b.set(i)
		}
	}
	return b
}

func (c *Compiled) bitsOf(ids map[string][]netip.Prefix) bitset {
	b := c.newBits()
	for id := range ids {
		if i, ok := c.idx[id]; ok {
			b.set(i)
		}
	}
	return b
}

// Private returns prefixes excluded from internet (exit-node) rules.
func (c *Compiled) Private() []netip.Prefix { return c.private }

func sameOwner(a, b *Node) bool {
	return a != nil && b != nil && a.OwnerID != "" && a.OwnerID == b.OwnerID && len(a.Tags) == 0 && len(b.Tags) == 0
}

func (c *Compiled) link(a, b string) {
	i, ok1 := c.idx[a]
	j, ok2 := c.idx[b]
	if !ok1 || !ok2 {
		return
	}
	c.rows[i].set(j)
	c.rows[j].set(i)
}

// Peers returns the IDs of nodes that must be configured as peers of id (sorted).
func (c *Compiled) Peers(id string) []string {
	i, ok := c.idx[id]
	if !ok {
		return []string{}
	}
	row := c.rows[i]
	n := 0
	for _, w := range row {
		n += bits.OnesCount64(w)
	}
	out := make([]string, 0, n)
	for wi, w := range row {
		for w != 0 {
			b := bits.TrailingZeros64(w)
			out = append(out, c.ids[wi*64+b])
			w &= w - 1
		}
	}
	return out
}

// IsPeer reports whether a and b are allowed to communicate in at least one direction.
func (c *Compiled) IsPeer(a, b string) bool {
	i, ok1 := c.idx[a]
	j, ok2 := c.idx[b]
	return ok1 && ok2 && c.rows[i].has(j)
}

// CanUseExitNode reports whether node id may route internet traffic via exit nodes.
func (c *Compiled) CanUseExitNode(id string) bool { return c.exitUsers[id] }

// FirewallRule is an inbound (or, for gateway enforcement, forwarding) allow rule.
type FirewallRule struct {
	RuleID string
	Src    []netip.Prefix
	Dst    []netip.Prefix
	// Exclude lists destinations the rule must not match even though Dst covers them
	// (internet rules exclude the overlay and private subnet routes).
	Exclude []netip.Prefix
	Ports   []PortRange
	Proto   string
}

func (c *Compiled) exclude(r *CompiledRule) []netip.Prefix {
	if r.Internet {
		return c.private
	}
	return nil
}

// InboundRules returns the rules a node must enforce on traffic arriving at it
// (to its own addresses, its subnet routes, or — for exit nodes — the internet).
func (c *Compiled) InboundRules(id string) []FirewallRule {
	var out []FirewallRule
	self := c.nodes[id]
	for _, r := range c.Rules {
		dst, ok := r.DstNodes[id]
		if !ok {
			continue
		}
		src := r.SrcPrefixes
		if r.Self {
			src = nil
			for s := range r.SrcNodes {
				if sameOwner(c.nodes[s], self) {
					src = append(src, c.nodes[s].Addrs...)
				}
			}
			if len(src) == 0 {
				continue
			}
		}
		out = append(out, FirewallRule{RuleID: r.ID, Src: src, Dst: dst, Exclude: c.exclude(&r), Ports: r.Ports, Proto: r.Proto})
	}
	return out
}

// OutboundRules returns what traffic originating from node id may reach. The
// gateway uses this to filter traffic of standard WireGuard clients.
func (c *Compiled) OutboundRules(id string) []FirewallRule {
	var out []FirewallRule
	self := c.nodes[id]
	for _, r := range c.Rules {
		if !r.SrcNodes[id] {
			continue
		}
		dst := r.DstPrefixes
		if r.Self {
			dst = nil
			for d, pfx := range r.DstNodes {
				if sameOwner(c.nodes[d], self) && d != id {
					dst = append(dst, pfx...)
				}
			}
			if len(dst) == 0 {
				continue
			}
		}
		out = append(out, FirewallRule{RuleID: r.ID, Src: self.Addrs, Dst: dst, Exclude: c.exclude(&r), Ports: r.Ports, Proto: r.Proto})
	}
	return out
}

// Decision is the result of an access check.
type Decision struct {
	Allowed bool   `json:"allowed"`
	RuleID  string `json:"rule_id,omitempty"`
	Index   int    `json:"rule_index"`
	Reason  string `json:"reason"`
}

// Check evaluates whether a packet from src to dst:port/proto is allowed.
func (c *Compiled) Check(src, dst netip.Addr, port uint16, proto string) Decision {
	srcNode := c.nodeByAddr(src)
	dstNode := c.nodeByAddr(dst)
	for _, r := range c.Rules {
		if r.Proto != "" && proto != "" && r.Proto != proto {
			continue
		}
		if !containsAddr(r.SrcPrefixes, src) {
			continue
		}
		if !portAllowed(r.Ports, port) {
			continue
		}
		if r.Self {
			if srcNode == nil || dstNode == nil || !sameOwner(srcNode, dstNode) || !containsAddr(r.DstNodes[dstNode.ID], dst) {
				continue
			}
		} else if !containsAddr(r.DstPrefixes, dst) {
			continue
		}
		// Internet rules only match addresses outside the overlay & known subnet routes.
		if r.Internet && containsAddr(c.private, dst) {
			continue
		}
		reason := "allowed by rule"
		if r.ID != "" {
			reason += " " + r.ID
		} else {
			reason += fmt.Sprintf(" #%d", r.Index+1)
		}
		return Decision{Allowed: true, RuleID: r.ID, Index: r.Index, Reason: reason}
	}
	return Decision{Allowed: false, Index: -1, Reason: "no rule allows this traffic (default deny)"}
}

func (c *Compiled) nodeByAddr(a netip.Addr) *Node {
	for _, n := range c.nodes {
		for _, p := range n.Addrs {
			if p.Contains(a) {
				return n
			}
		}
	}
	return nil
}

// Node returns the compiled node with the given ID.
func (c *Compiled) Node(id string) *Node { return c.nodes[id] }

// TestResult is the outcome of one policy test assertion.
type TestResult struct {
	Src      string   `json:"src"`
	Dst      string   `json:"dst"`
	Expect   string   `json:"expect"`
	Passed   bool     `json:"passed"`
	Decision Decision `json:"decision"`
	Error    string   `json:"error,omitempty"`
}

// RunTests evaluates the policy's tests.
func (c *Compiled) RunTests(env Env) []TestResult {
	r := resolver{p: c.Policy, env: env, nodes: env.Nodes}
	var out []TestResult
	for _, t := range c.Policy.Tests {
		srcAddrs := r.testAddrs(t.Src)
		run := func(dst string, expect bool) {
			res := TestResult{Src: t.Src, Dst: dst, Expect: map[bool]string{true: "accept", false: "deny"}[expect]}
			sel, port, err := splitTestDst(dst)
			if err != nil {
				res.Error = err.Error()
				out = append(out, res)
				return
			}
			dstAddrs := r.testAddrs(sel)
			if len(srcAddrs) == 0 || len(dstAddrs) == 0 {
				res.Error = "selector matches no device or address"
				out = append(out, res)
				return
			}
			res.Passed = true
			for _, s := range srcAddrs {
				for _, d := range dstAddrs {
					dec := c.Check(s, d, port, t.Proto)
					res.Decision = dec
					if dec.Allowed != expect {
						res.Passed = false
						res.Decision.Reason = fmt.Sprintf("%s → %s:%d: %s", s, d, port, dec.Reason)
						out = append(out, res)
						return
					}
				}
			}
			out = append(out, res)
		}
		for _, d := range t.Accept {
			run(d, true)
		}
		for _, d := range t.Deny {
			run(d, false)
		}
	}
	return out
}

// AutoApproveRoute reports whether node may have route auto-approved.
func (p *Policy) AutoApproveRoute(env Env, node Node, route netip.Prefix) bool {
	r := resolver{p: p, env: env, nodes: env.Nodes}
	for cidr, sels := range p.AutoApprovers.Routes {
		pfx, err := parsePrefix(cidr)
		if err != nil || pfx.Bits() > route.Bits() || !pfx.Contains(route.Addr()) {
			continue
		}
		for _, s := range sels {
			if r.nodeMatches(s, &node) {
				return true
			}
		}
	}
	return false
}

func (p *Policy) AutoApproveExitNode(env Env, node Node) bool {
	r := resolver{p: p, env: env, nodes: env.Nodes}
	for _, s := range p.AutoApprovers.ExitNode {
		if r.nodeMatches(s, &node) {
			return true
		}
	}
	return false
}

// CanOwnTag reports whether a user (by id/email/groups) may assign tag.
func (p *Policy) CanOwnTag(env Env, userID, email, tag string) bool {
	owners, ok := p.TagOwners[tag]
	if !ok {
		return false
	}
	for _, o := range owners {
		switch {
		case strings.HasPrefix(o, "group:"):
			if slices.Contains(env.Groups[strings.TrimPrefix(o, "group:")], userID) {
				return true
			}
		case strings.EqualFold(strings.TrimPrefix(o, "user:"), email):
			return true
		}
	}
	return false
}

// ---------- resolution ----------

type resolver struct {
	p     *Policy
	env   Env
	nodes []Node
	// groups is env.Groups as sets, built on first use (membership tests are hot).
	groups map[string]map[string]bool
}

func newResolver(p *Policy, env Env) *resolver {
	return &resolver{p: p, env: env, nodes: env.Nodes}
}

func (r *resolver) inGroup(group, userID string) bool {
	if r.groups == nil {
		r.groups = make(map[string]map[string]bool, len(r.env.Groups))
		for g, members := range r.env.Groups {
			set := make(map[string]bool, len(members))
			for _, m := range members {
				set[m] = true
			}
			r.groups[g] = set
		}
	}
	return r.groups[group][userID]
}

func (r *resolver) nodeMatches(sel string, n *Node) bool {
	tagged := len(n.Tags) > 0
	switch {
	case sel == "*":
		return true
	case sel == "autogroup:member":
		return n.OwnerID != "" && !tagged
	case strings.HasPrefix(sel, "group:"):
		if tagged || n.OwnerID == "" {
			return false
		}
		return r.inGroup(strings.TrimPrefix(sel, "group:"), n.OwnerID)
	case strings.HasPrefix(sel, "tag:"):
		return slices.Contains(n.Tags, sel)
	case strings.HasPrefix(sel, "device:"):
		return strings.EqualFold(n.Name, strings.TrimPrefix(sel, "device:"))
	case strings.HasPrefix(sel, "user:"), strings.Contains(sel, "@"):
		return !tagged && n.OwnerEmail != "" && strings.EqualFold(n.OwnerEmail, strings.TrimPrefix(sel, "user:"))
	}
	if pfx, ok := r.prefixFor(sel); ok {
		for _, a := range n.Addrs {
			if pfx.Overlaps(a) {
				return true
			}
		}
		for _, rt := range n.Routes {
			if pfx.Overlaps(rt) {
				return true
			}
		}
	}
	return false
}

func (r *resolver) prefixFor(sel string) (netip.Prefix, bool) {
	sel = strings.TrimPrefix(sel, "host:")
	if v, ok := r.p.Hosts[sel]; ok {
		p, err := parsePrefix(v)
		return p, err == nil
	}
	p, err := parsePrefix(sel)
	return p, err == nil
}

// resolveSrc returns matching node IDs and the source prefixes for the selector.
func (r *resolver) resolveSrc(sel string) ([]string, []netip.Prefix) {
	var ids []string
	var prefixes []netip.Prefix
	if sel == "*" {
		prefixes = append(prefixes, InternetV4, InternetV6)
	}
	pfx, isPrefix := r.prefixFor(sel)
	if isPrefix {
		prefixes = append(prefixes, pfx)
	}
	for i := range r.nodes {
		n := &r.nodes[i]
		if !r.nodeMatches(sel, n) {
			continue
		}
		ids = append(ids, n.ID)
		if !isPrefix && sel != "*" {
			prefixes = append(prefixes, n.Addrs...)
		}
	}
	return ids, prefixes
}

// resolveDst returns node ID -> the destination prefixes on that node covered by the selector.
func (r *resolver) resolveDst(sel string) map[string][]netip.Prefix {
	out := map[string][]netip.Prefix{}
	pfx, isPrefix := r.prefixFor(sel)
	for i := range r.nodes {
		n := &r.nodes[i]
		if isPrefix {
			var hit []netip.Prefix
			for _, a := range n.Addrs {
				if pfx.Overlaps(a) {
					hit = append(hit, a)
				}
			}
			for _, rt := range n.Routes {
				if pfx.Overlaps(rt) {
					hit = append(hit, narrower(pfx, rt))
				}
			}
			if len(hit) > 0 {
				out[n.ID] = hit
			}
			continue
		}
		if r.nodeMatches(sel, n) {
			out[n.ID] = append([]netip.Prefix{}, n.Addrs...)
			if sel == "*" {
				// "*" as destination includes subnet routes.
				out[n.ID] = append(out[n.ID], n.Routes...)
			}
		}
	}
	return out
}

func (r *resolver) dstPrefixes(sel string) []netip.Prefix {
	if pfx, ok := r.prefixFor(sel); ok {
		return []netip.Prefix{pfx}
	}
	var out []netip.Prefix
	for id, p := range r.resolveDst(sel) {
		_ = id
		out = append(out, p...)
	}
	return out
}

// testAddrs resolves a test selector to concrete addresses (first address of each node).
func (r *resolver) testAddrs(sel string) []netip.Addr {
	if pfx, ok := r.prefixFor(sel); ok && !strings.Contains(sel, "@") {
		return []netip.Addr{pfx.Addr()}
	}
	var out []netip.Addr
	for i := range r.nodes {
		n := &r.nodes[i]
		if r.nodeMatches(sel, n) {
			for _, a := range n.Addrs {
				if a.Addr().Is4() {
					out = append(out, a.Addr())
				}
			}
		}
	}
	return out
}

func narrower(a, b netip.Prefix) netip.Prefix {
	if a.Bits() >= b.Bits() {
		return a
	}
	return b
}

func containsAddr(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func portAllowed(ranges []PortRange, port uint16) bool {
	if port == 0 {
		return true
	}
	for _, r := range ranges {
		if port >= r.First && port <= r.Last {
			return true
		}
	}
	return false
}

func appendUnique(dst []netip.Prefix, ps ...netip.Prefix) []netip.Prefix {
	for _, p := range ps {
		if !slices.Contains(dst, p) {
			dst = append(dst, p)
		}
	}
	return dst
}

// dedupe removes repeated prefixes, keeping the first occurrence (order matters
// for readable output). Linear time: rules can cover thousands of addresses.
func dedupe(ps []netip.Prefix) []netip.Prefix {
	if len(ps) < 16 {
		return appendUnique(nil, ps...)
	}
	seen := make(map[netip.Prefix]struct{}, len(ps))
	out := make([]netip.Prefix, 0, len(ps))
	for _, p := range ps {
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// neverForwarded are destinations exit-node rules don't cover.
var neverForwarded = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fe80::/10"),
}
