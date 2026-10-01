package core

import (
	"net/netip"
	"sort"
	"strings"

	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/store"
)

// GatewayPeer is one WireGuard peer of the built-in gateway.
type GatewayPeer struct {
	ID         string
	Name       string
	Kind       string
	PublicKey  string
	PSK        string // plaintext, may be empty
	AllowedIPs []netip.Prefix
	Keepalive  int
}

// GatewayView is everything the gateway data plane needs.
type GatewayView struct {
	Serial uint64
	Addr4  netip.Addr
	Addr6  netip.Addr
	Net4   netip.Prefix
	Net6   netip.Prefix
	IPv6On bool
	Peers  []GatewayPeer
	// Forward lists allowed forwarded flows (default deny).
	Forward []policy.FirewallRule
	// WGClients are the overlay addresses of standard clients (DNS + NAT source).
	WGClients []netip.Prefix
	// ExitSources may use the gateway for internet egress (NAT).
	ExitSources []netip.Prefix
	DNS         DNSView
}

// DNSView is the resolver configuration served on the gateway.
type DNSView struct {
	Domain      string
	Hosts       map[string][]netip.Addr // fqdn -> addrs
	CNAMEs      map[string]string
	Nameservers []string
	Split       []SplitDNS
	FailOpen    bool
}

// GatewayView builds the gateway configuration from a snapshot.
func (c *Core) GatewayView(s *Snapshot) *GatewayView {
	if s.GatewayID == "" {
		return nil
	}
	gw := s.Devices[s.GatewayID]
	if gw == nil {
		return nil
	}
	v := &GatewayView{
		Serial: s.Serial,
		Addr4:  netip.MustParseAddr(gw.IPv4),
		Addr6:  netip.MustParseAddr(gw.IPv6),
		Net4:   s.Plan.IPv4,
		Net6:   s.Plan.IPv6,
		IPv6On: s.Settings.Network.IPv6On,
		DNS:    c.dnsView(s),
	}
	natives := map[string]bool{}
	for _, id := range s.Compiled.Peers(s.GatewayID) {
		natives[id] = true
	}
	for id := range s.Active {
		d := s.Devices[id]
		if d.Kind != store.KindWireGuard {
			continue
		}
		psk := ""
		if d.PSK != "" {
			if p, err := c.Box.Open(d.PSK); err == nil {
				psk = p
			} else {
				c.Log.Error("cannot decrypt PSK; peer skipped", "device", d.Name, "err", err)
				continue
			}
		}
		addrs := deviceAddrs(d, v.IPv6On)
		// A router behind this config also carries its local networks (site routes).
		allowed := append(append([]netip.Prefix{}, addrs...), s.PrimaryRoutesOf(d.ID)...)
		v.Peers = append(v.Peers, GatewayPeer{ID: d.ID, Name: d.Name, Kind: d.Kind, PublicKey: d.WGPublicKey, PSK: psk, AllowedIPs: allowed})
		v.WGClients = append(v.WGClients, addrs...)
		v.Forward = append(v.Forward, s.Compiled.OutboundRules(id)...)
		v.Forward = append(v.Forward, s.Compiled.InboundRules(id)...)
		for _, pid := range s.Compiled.Peers(id) {
			natives[pid] = true
		}
		if d.TunnelMode == store.TunnelFull || s.Compiled.CanUseExitNode(id) {
			v.ExitSources = append(v.ExitSources, addrs...)
		}
	}
	// Natives using the gateway as exit node.
	for _, r := range s.Compiled.InboundRules(s.GatewayID) {
		v.Forward = append(v.Forward, r)
		if containsPrefix(r.Dst, policy.InternetV4) {
			v.ExitSources = append(v.ExitSources, r.Src...)
		}
	}
	for id := range natives {
		d := s.Devices[id]
		if d == nil || !s.Active[id] || d.Kind != store.KindNative {
			continue
		}
		allowed := append(deviceAddrs(d, v.IPv6On), s.PrimaryRoutesOf(id)...)
		v.Peers = append(v.Peers, GatewayPeer{ID: d.ID, Name: d.Name, Kind: d.Kind, PublicKey: d.WGPublicKey, AllowedIPs: allowed})
	}
	sort.Slice(v.Peers, func(i, j int) bool { return v.Peers[i].ID < v.Peers[j].ID })
	return v
}

func containsPrefix(ps []netip.Prefix, p netip.Prefix) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

func (c *Core) dnsView(s *Snapshot) DNSView {
	ds := s.Settings.DNS
	v := DNSView{
		Domain:      strings.ToLower(s.Settings.Network.Domain),
		Hosts:       map[string][]netip.Addr{},
		CNAMEs:      map[string]string{},
		Nameservers: ds.Nameservers,
		Split:       ds.Split,
		FailOpen:    ds.FailOpen,
	}
	if ds.MagicDNS {
		for id := range s.Active {
			d := s.Devices[id]
			name := strings.ToLower(s.FQDN(d))
			for _, p := range deviceAddrs(d, s.Settings.Network.IPv6On) {
				v.Hosts[name] = append(v.Hosts[name], p.Addr())
			}
		}
	}
	for _, r := range ds.Records {
		name := RecordFQDN(r.Name, v.Domain)
		switch strings.ToUpper(r.Type) {
		case "A", "AAAA":
			if a, err := netip.ParseAddr(r.Value); err == nil {
				v.Hosts[name] = append(v.Hosts[name], a)
			}
		case "CNAME":
			v.CNAMEs[name] = strings.TrimSuffix(strings.ToLower(r.Value), ".")
		}
	}
	return v
}
