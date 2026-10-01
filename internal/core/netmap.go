package core

import (
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"

	pb "github.com/anand34577/gorget/gen/gorget/v1"
	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/store"
)

// NetworkMap builds the network map for a native device. Returns nil if the
// device is not active.
func (c *Core) NetworkMap(s *Snapshot, deviceID string) *pb.NetworkMap {
	d := s.Devices[deviceID]
	if d == nil || !s.Active[deviceID] || d.Kind != store.KindNative {
		return nil
	}
	nm := &pb.NetworkMap{
		Serial:      s.Serial,
		Self:        c.selfNode(s, d),
		Dns:         c.dnsConfig(s),
		Relays:      c.relays(),
		StunServers: c.stunServers(),
		Settings:    c.clientSettings(s),
	}

	peerIDs := s.Compiled.Peers(deviceID)
	gwExtra := []netip.Prefix{}
	needGateway := false
	var peers []*pb.Peer
	for _, pid := range peerIDs {
		p := s.Devices[pid]
		if p == nil || !s.Active[pid] {
			continue
		}
		switch p.Kind {
		case store.KindWireGuard:
			// Standard WireGuard clients are reachable through the gateway.
			needGateway = true
			gwExtra = append(gwExtra, deviceAddrs(p, s.Settings.Network.IPv6On)...)
			gwExtra = append(gwExtra, s.PrimaryRoutesOf(p.ID)...) // networks behind a WireGuard router
			continue
		case store.KindGateway:
			needGateway = true
			continue
		}
		peers = append(peers, c.peer(s, p, nil))
	}
	if needGateway && s.GatewayID != "" {
		if gw := s.Devices[s.GatewayID]; gw != nil {
			peers = append(peers, c.peer(s, gw, gwExtra))
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
	nm.Peers = peers

	// Domain routes: only through exit nodes this device may actually reach.
	reachable := map[string]string{} // device name -> peer id
	for _, p := range nm.Peers {
		if p.IsExitNode {
			reachable[p.Name] = p.Id
		}
	}
	for _, dr := range s.Settings.Routing.DomainRoutes {
		if id, ok := reachable[dr.Via]; ok {
			nm.DomainRoutes = append(nm.DomainRoutes, &pb.DomainRoute{Domain: NormalizeRouteDomain(dr.Domain), ViaPeerId: id})
		}
	}

	for _, r := range s.Compiled.InboundRules(deviceID) {
		nm.FirewallRules = append(nm.FirewallRules, toPBRule(r))
	}
	return nm
}

func (c *Core) selfNode(s *Snapshot, d *store.Device) *pb.SelfNode {
	n := &pb.SelfNode{
		Id:               d.ID,
		Name:             d.Name,
		Fqdn:             s.FQDN(d),
		Ipv4:             d.IPv4 + "/32",
		NetworkCidrV4:    s.Plan.IPv4.String(),
		Tags:             d.Tags,
		UserEmail:        s.OwnerEmail(d),
		ExitNodeApproved: d.ExitAdvertised && d.ExitApproved,
	}
	if s.Settings.Network.IPv6On {
		n.Ipv6 = d.IPv6 + "/128"
		n.NetworkCidrV6 = s.Plan.IPv6.String()
	}
	if !d.KeyExpiryDisabled {
		n.KeyExpiresAtUnix = d.KeyExpiresAt
	}
	for _, r := range s.Routes[d.ID] {
		if r.Approved && r.Enabled && r.Advertised {
			n.ApprovedRoutes = append(n.ApprovedRoutes, r.CIDR)
		}
	}
	return n
}

func (c *Core) peer(s *Snapshot, p *store.Device, extraAllowed []netip.Prefix) *pb.Peer {
	addrs := deviceAddrs(p, s.Settings.Network.IPv6On)
	routes := s.PrimaryRoutesOf(p.ID)
	allowed := append(append(append([]netip.Prefix{}, addrs...), routes...), extraAllowed...)
	out := &pb.Peer{
		Id:                 p.ID,
		Name:               p.Name,
		Fqdn:               s.FQDN(p),
		WireguardPublicKey: p.WGPublicKey,
		DiscoPublicKey:     p.DiscoKey,
		Addresses:          prefixStrings(addrs),
		AllowedIps:         prefixStrings(allowed),
		Endpoints:          p.Endpoints,
		HomeRelay:          p.HomeRelay,
		Online:             s.Online[p.ID],
		LastSeenUnix:       p.LastSeenAt,
		Os:                 p.OS,
		Tags:               p.Tags,
		User:               s.OwnerEmail(p),
		IsExitNode:         p.ExitAdvertised && p.ExitApproved,
		IsGateway:          p.Kind == store.KindGateway,
		SubnetRoutes:       prefixStrings(routes),
	}
	if p.Kind == store.KindGateway {
		out.KeepaliveSeconds = 25
	}
	return out
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

func toPBRule(r policy.FirewallRule) *pb.FirewallRule {
	out := &pb.FirewallRule{SrcCidrs: prefixStrings(r.Src), DstCidrs: prefixStrings(r.Dst)}
	if r.Proto != "" {
		out.Protocols = []string{r.Proto}
	}
	if !(len(r.Ports) == 1 && r.Ports[0] == policy.AllPorts[0]) {
		for _, pr := range r.Ports {
			out.Ports = append(out.Ports, &pb.PortRange{First: uint32(pr.First), Last: uint32(pr.Last)})
		}
	}
	// Exclusions are expressed to clients by subtracting them from the destination set.
	if len(r.Exclude) > 0 {
		out.DstCidrs = prefixStrings(subtractPrefixes(r.Dst, r.Exclude))
	}
	return out
}

func (c *Core) dnsConfig(s *Snapshot) *pb.DNSConfig {
	ds := s.Settings.DNS
	out := &pb.DNSConfig{
		MagicDns:         ds.MagicDNS,
		Domain:           s.Settings.Network.Domain,
		SearchDomains:    append([]string{}, ds.SearchDomains...),
		Nameservers:      ds.Nameservers,
		OverrideLocalDns: ds.OverrideLocal,
	}
	if ds.MagicDNS && !slices.Contains(out.SearchDomains, out.Domain) {
		out.SearchDomains = append([]string{out.Domain}, out.SearchDomains...)
	}
	for _, sp := range ds.Split {
		out.Split = append(out.Split, &pb.SplitDNSRoute{Domain: sp.Domain, Nameservers: sp.Nameservers})
	}
	for _, r := range ds.Records {
		out.Records = append(out.Records, &pb.DNSRecord{Name: RecordFQDN(r.Name, s.Settings.Network.Domain), Type: strings.ToUpper(r.Type), Value: r.Value})
	}
	return out
}

// RecordFQDN qualifies a record name with the network domain unless it already contains a dot.
// RecordFQDN qualifies a custom record name: a single word ("nas", or "*.apps" for a
// wildcard) gets the network domain; anything with a dot ("jellyfin.home.lan") is
// used as written.
func RecordFQDN(name, domain string) string {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	wild := strings.HasPrefix(name, "*.")
	base := strings.TrimPrefix(name, "*.")
	if !strings.Contains(base, ".") {
		base += "." + domain
	}
	if wild {
		return "*." + base
	}
	return base
}

func (c *Core) relays() []*pb.RelayServer {
	var out []*pb.RelayServer
	if c.cluster != nil {
		// Every live instance is a relay; clients pick the nearest.
		seen := map[string]bool{} // instances behind one shared name are one relay to clients
		for _, in := range c.cluster.Instances() {
			if in.RelayURL != "" && !seen[in.RelayURL] {
				seen[in.RelayURL] = true
				out = append(out, &pb.RelayServer{Region: in.Region, Name: in.Name, Url: in.RelayURL, UdpAddr: in.UDPAddr})
			}
		}
	} else if c.Cfg.Relay.Enabled {
		u, _ := url.Parse(c.Cfg.PublicURL)
		scheme := "wss"
		if u.Scheme == "http" {
			scheme = "ws"
		}
		out = append(out, &pb.RelayServer{Region: c.Cfg.Relay.Region, Name: u.Hostname(), Url: scheme + "://" + u.Host + "/relay", UdpAddr: c.Cfg.Relay.UDPAdvertise})
	}
	for _, r := range c.Cfg.Relay.External {
		out = append(out, &pb.RelayServer{Region: r.Region, Name: r.Name, Url: r.URL})
	}
	return out
}

func (c *Core) stunServers() []string {
	if c.Cfg.STUN.Enabled && c.Cfg.STUN.Advertise != "" {
		return []string{c.Cfg.STUN.Advertise}
	}
	return nil
}

func (c *Core) clientSettings(s *Snapshot) *pb.ClientSettings {
	cs := s.Settings.Client
	return &pb.ClientSettings{
		AllowLanAccessDefault:   cs.AllowLANDefault,
		KillSwitchEnforced:      cs.KillSwitchEnforced,
		ForcedExitNodeId:        cs.ForcedExitNodeID,
		DefaultExitNodeId:       cs.DefaultExitNodeID,
		Mtu:                     uint32(cs.MTU),
		AllowUserExitNodeChoice: cs.AllowUserExitChoice,
		AllowCustomDns:          cs.AllowCustomDNS,
	}
}

// Diff computes the delta from old to cur. Returns nil if nothing changed.
func Diff(old, cur *pb.NetworkMap) *pb.NetworkMapDelta {
	d := &pb.NetworkMapDelta{Serial: cur.Serial}
	changed := false
	if !proto.Equal(old.Self, cur.Self) {
		d.Self, changed = cur.Self, true
	}
	oldPeers := map[string]*pb.Peer{}
	for _, p := range old.Peers {
		oldPeers[p.Id] = p
	}
	seen := map[string]bool{}
	for _, p := range cur.Peers {
		seen[p.Id] = true
		if op, ok := oldPeers[p.Id]; !ok || !proto.Equal(op, p) {
			d.PeersUpserted = append(d.PeersUpserted, p)
			changed = true
		}
	}
	for id := range oldPeers {
		if !seen[id] {
			d.PeersRemoved = append(d.PeersRemoved, id)
			changed = true
		}
	}
	if !proto.Equal(old.Dns, cur.Dns) {
		d.Dns, changed = cur.Dns, true
	}
	if !rulesEqual(old.FirewallRules, cur.FirewallRules) {
		d.Firewall, changed = &pb.FirewallRuleSet{Rules: cur.FirewallRules}, true
	}
	if !relaysEqual(old, cur) {
		d.Relays, changed = &pb.RelaySet{Relays: cur.Relays}, true
	}
	if !proto.Equal(old.Settings, cur.Settings) {
		d.Settings, changed = cur.Settings, true
	}
	if !slices.EqualFunc(old.DomainRoutes, cur.DomainRoutes, func(x, y *pb.DomainRoute) bool { return proto.Equal(x, y) }) {
		d.DomainRoutes, changed = &pb.DomainRouteSet{Routes: cur.DomainRoutes}, true
	}
	if !changed {
		return nil
	}
	return d
}

func rulesEqual(a, b []*pb.FirewallRule) bool {
	return slices.EqualFunc(a, b, func(x, y *pb.FirewallRule) bool { return proto.Equal(x, y) })
}

func relaysEqual(a, b *pb.NetworkMap) bool {
	return slices.EqualFunc(a.Relays, b.Relays, func(x, y *pb.RelayServer) bool { return proto.Equal(x, y) }) &&
		slices.Equal(a.StunServers, b.StunServers)
}

// ---------- prefix arithmetic ----------

// subtractPrefixes returns base minus excl as a minimal list of prefixes.
func subtractPrefixes(base, excl []netip.Prefix) []netip.Prefix {
	out := base
	for _, e := range excl {
		var next []netip.Prefix
		for _, b := range out {
			next = append(next, subtractOne(b, e)...)
		}
		out = next
	}
	return out
}

func subtractOne(b, e netip.Prefix) []netip.Prefix {
	if !b.Overlaps(e) {
		return []netip.Prefix{b}
	}
	if e.Bits() <= b.Bits() {
		return nil // e covers b entirely
	}
	// Split b into halves until e is isolated.
	var out []netip.Prefix
	cur := b
	for cur.Bits() < e.Bits() {
		lo, hi := halves(cur)
		if lo.Contains(e.Addr()) {
			out = append(out, hi)
			cur = lo
		} else {
			out = append(out, lo)
			cur = hi
		}
	}
	return out
}

func halves(p netip.Prefix) (netip.Prefix, netip.Prefix) {
	bits := p.Bits() + 1
	lo := netip.PrefixFrom(p.Addr(), bits).Masked()
	b := lo.Addr().AsSlice()
	idx := (bits - 1) / 8
	b[idx] |= 0x80 >> ((bits - 1) % 8)
	hiAddr, _ := netip.AddrFromSlice(b)
	if p.Addr().Is4() {
		hiAddr = hiAddr.Unmap()
	}
	return lo, netip.PrefixFrom(hiAddr, bits)
}
