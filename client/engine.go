package client

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"google.golang.org/protobuf/proto"

	"github.com/anand34577/gorget/client/magicsock"
	"github.com/anand34577/gorget/client/tunx"
	pb "github.com/anand34577/gorget/gen/gorget/v1"
	"github.com/anand34577/gorget/internal/dnsserver"
)

// TUNConfig is what the platform must configure on its VPN interface.
type TUNConfig struct {
	Addresses      []netip.Prefix `json:"addresses"`
	Routes         []netip.Prefix `json:"routes"`
	ExcludedRoutes []netip.Prefix `json:"excluded_routes"`
	// RoutesMinusExcluded is Routes with ExcludedRoutes subtracted, for platforms
	// that can't exclude routes (Android before API 33).
	RoutesMinusExcluded []netip.Prefix `json:"routes_minus_excluded"`
	DNS                 []netip.Addr   `json:"dns"`
	SearchDomains       []string       `json:"search_domains"`
	MTU                 int            `json:"mtu"`
	// FullTunnel is true when an exit node carries all internet traffic.
	FullTunnel bool `json:"full_tunnel"`
	// AllowLAN asks the platform to keep local networks outside the tunnel.
	AllowLAN bool `json:"allow_lan"`
	// MatchDomains are resolved by the in-tunnel DNS server even when OverrideDNS is
	// false (split DNS): the network domain and split-DNS domains.
	MatchDomains []string `json:"match_domains"`
	// OverrideDNS sends every DNS query to the in-tunnel resolver.
	OverrideDNS bool `json:"override_dns"`
	// KillSwitch blocks all traffic outside the tunnel while FullTunnel is on.
	KillSwitch bool `json:"kill_switch"`
	// Forward enables IP forwarding and NAT for traffic arriving from the tunnel,
	// so this device can act as a subnet router (ForwardRoutes) or exit node.
	Forward       bool           `json:"forward"`
	ForwardRoutes []netip.Prefix `json:"forward_routes"`
	ExitNode      bool           `json:"exit_node"`
	// Overlay are the network's own ranges (sources to NAT when forwarding).
	Overlay []netip.Prefix `json:"overlay"`
}

func (c TUNConfig) equal(o TUNConfig) bool { return reflect.DeepEqual(c, o) }

// LAN ranges excluded from the tunnel when "allow LAN access" is on.
var lanRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fc00::/7"),
}

type engine struct {
	log      *slog.Logger
	platform Platform
	magic    *magicsock.Conn
	wrap     *tunx.Wrapper
	dev      *device.Device
	dns      *dnsserver.Server

	mu       sync.Mutex
	nm       *pb.NetworkMap
	prefs    Prefs
	wgPeers  map[string]bool         // hex public keys configured
	psks     map[string]string       // hex peer key -> hex pre-shared key (post-quantum)
	dyn      map[netip.Addr]dynRoute // addresses learned for domain routes
	dynTimer *time.Timer
	stopDyn  chan struct{}
	tunCfg   TUNConfig
	tunReady bool
	exitWarn string
}

// wgVerbose logs wireguard-go's handshake details (GORGET_WG_DEBUG=1); very chatty.
var wgVerbose = os.Getenv("GORGET_WG_DEBUG") != ""

func newEngine(log *slog.Logger, p Platform, magic *magicsock.Conn, dnsSrv *dnsserver.Server, privHex string, port uint16, mtu int) (*engine, error) {
	e := &engine{log: log, platform: p, magic: magic, dns: dnsSrv, wgPeers: map[string]bool{}, psks: map[string]string{}, dyn: map[netip.Addr]dynRoute{}, stopDyn: make(chan struct{})}
	e.wrap = tunx.New(nil, mtu)
	e.wrap.SetResolver(e.resolve)
	logger := &device.Logger{
		Verbosef: func(f string, a ...any) {
			if wgVerbose {
				log.Debug("wireguard: " + fmt.Sprintf(f, a...))
			}
		},
		Errorf: func(f string, a ...any) { log.Debug("wireguard: " + fmt.Sprintf(f, a...)) },
	}
	dnsSrv.SetAnswerHook(e.learn)
	go e.sweepDynamic()
	e.dev = device.NewDevice(e.wrap, magic, logger)
	e.dev.DisableSomeRoamingForBrokenMobileSemantics()
	if err := e.dev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privHex, port)); err != nil {
		e.dev.Close()
		return nil, err
	}
	if err := e.dev.Up(); err != nil {
		e.dev.Close()
		return nil, err
	}
	return e, nil
}

// setPSK installs a pre-shared key for a peer; it takes effect on the next handshake.
func (e *engine) setPSK(peer magicsock.Key, psk magicsock.Key) {
	hk, hp := hex.EncodeToString(peer[:]), hex.EncodeToString(psk[:])
	e.mu.Lock()
	defer e.mu.Unlock()
	e.psks[hk] = hp
	if e.wgPeers[hk] {
		// update_only: never create a peer here, it would have no allowed addresses.
		if err := e.dev.IpcSet(fmt.Sprintf("public_key=%s\nupdate_only=true\npreshared_key=%s\n", hk, hp)); err != nil {
			e.log.Warn("could not install the post-quantum key", "err", err)
		}
	}
}

// reapplyTUN installs the current interface settings again, so routes that couldn't
// be added on the previous network (it contained the same range) are retried.
func (e *engine) reapplyTUN() {
	e.mu.Lock()
	cfg, ready := e.tunCfg, e.tunReady
	e.mu.Unlock()
	if !ready || e.platform == nil {
		return
	}
	if dev, err := e.platform.ApplyTUN(cfg); err != nil {
		e.log.Debug("re-applying network settings", "err", err)
	} else if dev != nil {
		e.wrap.SetDevice(dev)
	}
}

// rebind reopens the UDP sockets. Needed after a network change on Windows and
// macOS, where the daemon's sockets are pinned to the physical interface that
// was the default when they were opened.
func (e *engine) rebind() {
	if err := e.dev.BindUpdate(); err != nil {
		e.log.Warn("could not reopen sockets after a network change", "err", err)
	}
}

func (e *engine) close() {
	close(e.stopDyn)
	e.mu.Lock()
	if e.dynTimer != nil {
		e.dynTimer.Stop()
	}
	e.mu.Unlock()
	e.dev.Close()
}

// dynRoute is an address learned from a name that a domain route covers.
type dynRoute struct {
	peerID string
	until  time.Time
}

const (
	dynGrace   = 2 * time.Minute // keep a route a little beyond the DNS TTL: connections outlive their lookups
	dynMinLife = 5 * time.Minute
	dynMax     = 4096
)

// domainPeer returns the peer a name must be routed through ("" when no domain route covers it).
func (e *engine) domainPeer(name string) string {
	if e.nm == nil {
		return ""
	}
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	best, bestLen := "", -1
	for _, r := range e.nm.DomainRoutes {
		d := strings.ToLower(r.Domain)
		if (name == d || strings.HasSuffix(name, "."+d)) && len(d) > bestLen {
			best, bestLen = r.ViaPeerId, len(d)
		}
	}
	return best
}

// learn records the addresses a covered name resolved to and routes them through the exit node.
func (e *engine) learn(name string, addrs []netip.Addr, ttl uint32) {
	e.mu.Lock()
	peer := e.domainPeer(name)
	if peer == "" || e.prefs.ExitNodeID != "" {
		e.mu.Unlock()
		return // not covered, or a full exit node already carries everything
	}
	until := time.Now().Add(max(time.Duration(ttl)*time.Second+dynGrace, dynMinLife))
	added := false
	for _, a := range addrs {
		cur, ok := e.dyn[a]
		if !ok || cur.peerID != peer {
			if len(e.dyn) >= dynMax {
				continue
			}
			added = true
		}
		e.dyn[a] = dynRoute{peerID: peer, until: until}
	}
	if added && e.dynTimer == nil {
		// Coalesce bursts of lookups into one reconfiguration.
		e.dynTimer = time.AfterFunc(300*time.Millisecond, e.reapplyDynamic)
	}
	e.mu.Unlock()
}

func (e *engine) reapplyDynamic() {
	e.mu.Lock()
	e.dynTimer = nil
	nm, prefs := e.nm, e.prefs
	e.mu.Unlock()
	if nm == nil {
		return
	}
	if err := e.apply(nm, prefs); err != nil {
		e.log.Warn("applying domain routes failed", "err", err)
	}
}

// sweepDynamic forgets expired addresses (every 30 s).
func (e *engine) sweepDynamic() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-e.stopDyn:
			return
		case <-t.C:
		}
		e.mu.Lock()
		changed := false
		now := time.Now()
		for a, r := range e.dyn {
			if now.After(r.until) {
				delete(e.dyn, a)
				changed = true
			}
		}
		e.mu.Unlock()
		if changed {
			e.reapplyDynamic()
		}
	}
}

// dynamicFor lists the learned addresses routed through a peer (callers hold e.mu).
func (e *engine) dynamicFor(peerID string) []netip.Prefix {
	var out []netip.Prefix
	for a, r := range e.dyn {
		if r.peerID == peerID {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func (e *engine) resolve(ctx context.Context, q []byte) []byte {
	var m dns.Msg
	if err := m.Unpack(q); err != nil {
		return nil
	}
	resp := e.dns.Resolve(ctx, &m)
	resp.Compress = true
	b, err := resp.Pack()
	if err != nil {
		return nil
	}
	return b
}

// applyDelta merges a delta into the current map.
func applyDelta(nm *pb.NetworkMap, d *pb.NetworkMapDelta) *pb.NetworkMap {
	out := proto.Clone(nm).(*pb.NetworkMap)
	out.Serial = d.Serial
	if d.Self != nil {
		out.Self = d.Self
	}
	if d.Dns != nil {
		out.Dns = d.Dns
	}
	if d.Firewall != nil {
		out.FirewallRules = d.Firewall.Rules
	}
	if d.Relays != nil {
		out.Relays = d.Relays.Relays
	}
	if d.Settings != nil {
		out.Settings = d.Settings
	}
	if d.DomainRoutes != nil {
		out.DomainRoutes = d.DomainRoutes.Routes
	}
	if len(d.PeersUpserted) > 0 || len(d.PeersRemoved) > 0 {
		byID := map[string]*pb.Peer{}
		for _, p := range out.Peers {
			byID[p.Id] = p
		}
		for _, id := range d.PeersRemoved {
			delete(byID, id)
		}
		for _, p := range d.PeersUpserted {
			byID[p.Id] = p
		}
		out.Peers = out.Peers[:0]
		for _, p := range byID {
			out.Peers = append(out.Peers, p)
		}
		sort.Slice(out.Peers, func(i, j int) bool { return out.Peers[i].Name < out.Peers[j].Name })
	}
	return out
}

func decodeKey(s string) (magicsock.Key, bool) {
	var k magicsock.Key
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return k, false
	}
	copy(k[:], b)
	return k, true
}

func prefixes(ss []string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range ss {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
		}
	}
	return out
}

// exitNode picks the exit node to use, honouring the server's forced choice.
func exitNode(nm *pb.NetworkMap, prefs Prefs) (*pb.Peer, string) {
	want := prefs.ExitNodeID
	if s := nm.GetSettings(); s != nil {
		if s.ForcedExitNodeId != "" {
			want = s.ForcedExitNodeId
		} else if !s.AllowUserExitNodeChoice {
			want = ""
		}
	}
	if want == "" {
		return nil, ""
	}
	for _, p := range nm.Peers {
		if p.Id == want {
			if !p.IsExitNode {
				return nil, p.Name + " is no longer an exit node"
			}
			return p, ""
		}
	}
	return nil, "the selected exit node isn't available to this device"
}

// apply configures every subsystem for the given map and preferences.
func (e *engine) apply(nm *pb.NetworkMap, prefs Prefs) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nm, e.prefs = nm, prefs
	exit, warn := exitNode(nm, prefs)
	e.exitWarn = warn

	// 1. magicsock peers.
	var infos []magicsock.PeerInfo
	for _, p := range nm.Peers {
		wk, ok := decodeKey(p.WireguardPublicKey)
		if !ok {
			continue
		}
		dk, _ := decodeKey(p.DiscoPublicKey)
		info := magicsock.PeerInfo{ID: p.Id, Name: p.Name, WGKey: wk, DiscoKey: dk, HomeRelay: p.HomeRelay}
		for _, ep := range p.Endpoints {
			if ap, err := netip.ParseAddrPort(ep); err == nil {
				info.Endpoints = append(info.Endpoints, ap)
			}
		}
		infos = append(infos, info)
	}
	e.magic.SetPeers(infos)

	// 2. WireGuard peers.
	var b strings.Builder
	want := map[string]bool{}
	for _, p := range nm.Peers {
		wk, ok := decodeKey(p.WireguardPublicKey)
		if !ok {
			continue
		}
		hk := hex.EncodeToString(wk[:])
		want[hk] = true
		fmt.Fprintf(&b, "public_key=%s\nendpoint=%s\nreplace_allowed_ips=true\n", hk, magicsock.EndpointString(wk))
		if psk := e.psks[hk]; psk != "" && !p.IsGateway {
			fmt.Fprintf(&b, "preshared_key=%s\n", psk)
		}
		ka := p.KeepaliveSeconds
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", ka)
		for _, a := range p.Addresses {
			fmt.Fprintf(&b, "allowed_ip=%s\n", a)
		}
		if prefs.AcceptRoutes || p.IsGateway {
			for _, r := range p.AllowedIps {
				if !slices.Contains(p.Addresses, r) {
					fmt.Fprintf(&b, "allowed_ip=%s\n", r)
				}
			}
		}
		if exit != nil && exit.Id == p.Id {
			b.WriteString("allowed_ip=0.0.0.0/0\nallowed_ip=::/0\n")
		} else if p.IsExitNode {
			for _, d := range e.dynamicFor(p.Id) {
				fmt.Fprintf(&b, "allowed_ip=%s\n", d)
			}
		}
	}
	for hk := range e.wgPeers {
		if !want[hk] {
			fmt.Fprintf(&b, "public_key=%s\nremove=true\n", hk)
		}
	}
	if b.Len() > 0 {
		if err := e.dev.IpcSet(b.String()); err != nil {
			return fmt.Errorf("configure WireGuard: %w", err)
		}
	}
	e.wgPeers = want

	// 3. Inbound firewall.
	var rules []tunx.Rule
	for _, fr := range nm.FirewallRules {
		r := tunx.Rule{Src: prefixes(fr.SrcCidrs), Dst: prefixes(fr.DstCidrs)}
		for _, pr := range fr.Ports {
			r.Ports = append(r.Ports, [2]uint16{uint16(pr.First), uint16(pr.Last)})
		}
		for _, proto := range fr.Protocols {
			switch proto {
			case "tcp":
				r.Protos = append(r.Protos, 6)
			case "udp":
				r.Protos = append(r.Protos, 17)
			case "icmp":
				r.Protos = append(r.Protos, 1, 58)
			}
		}
		rules = append(rules, r)
	}
	e.wrap.SetFilter(tunx.NewFilter(rules))

	// 4. DNS.
	e.dns.SetConfig(dnsConfig(nm))

	// 5. OS interface.
	cfg := tunConfig(nm, prefs, exit)
	for a := range e.dyn {
		cfg.Routes = append(cfg.Routes, netip.PrefixFrom(a, a.BitLen()))
	}
	sort.Slice(cfg.Routes, func(i, j int) bool { return cfg.Routes[i].String() < cfg.Routes[j].String() })
	cfg.RoutesMinusExcluded = subtractPrefixes(cfg.Routes, cfg.ExcludedRoutes)
	if !e.tunReady || !cfg.equal(e.tunCfg) {
		dev, err := e.platform.ApplyTUN(cfg)
		if err != nil {
			return fmt.Errorf("configure VPN interface: %w", err)
		}
		if dev != nil {
			e.wrap.SetDevice(dev)
		}
		e.tunCfg, e.tunReady = cfg, true
	}
	return nil
}

func dnsConfig(nm *pb.NetworkMap) *dnsserver.Config {
	d := nm.GetDns()
	cfg := &dnsserver.Config{Hosts: map[string][]netip.Addr{}, CNAMEs: map[string]string{}, Split: map[string][]string{}, TTL: 60}
	if d == nil {
		return cfg
	}
	cfg.Domain = strings.ToLower(d.Domain)
	cfg.Nameservers = d.Nameservers
	if d.MagicDns {
		add := func(fqdn string, addrs []string) {
			for _, a := range addrs {
				if p, err := netip.ParsePrefix(a); err == nil {
					cfg.Hosts[strings.ToLower(fqdn)] = append(cfg.Hosts[strings.ToLower(fqdn)], p.Addr())
				}
			}
		}
		if s := nm.Self; s != nil {
			add(s.Fqdn, []string{s.Ipv4, s.Ipv6})
		}
		for _, p := range nm.Peers {
			add(p.Fqdn, p.Addresses)
		}
	}
	for _, r := range d.Records {
		name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
		switch r.Type {
		case "A", "AAAA":
			if a, err := netip.ParseAddr(r.Value); err == nil {
				cfg.Hosts[name] = append(cfg.Hosts[name], a)
			}
		case "CNAME":
			cfg.CNAMEs[name] = strings.ToLower(strings.TrimSuffix(r.Value, "."))
		}
	}
	for _, s := range d.Split {
		cfg.Split[strings.ToLower(s.Domain)] = s.Nameservers
	}
	return cfg
}

func tunConfig(nm *pb.NetworkMap, prefs Prefs, exit *pb.Peer) TUNConfig {
	cfg := TUNConfig{MTU: 1280, AllowLAN: prefs.AllowLAN}
	if s := nm.GetSettings(); s != nil && s.Mtu >= 1280 {
		cfg.MTU = int(s.Mtu)
	}
	self := nm.GetSelf()
	if p, err := netip.ParsePrefix(self.GetIpv4()); err == nil {
		cfg.Addresses = append(cfg.Addresses, p)
	}
	if p, err := netip.ParsePrefix(self.GetIpv6()); err == nil {
		cfg.Addresses = append(cfg.Addresses, p)
	}
	for _, n := range []string{self.GetNetworkCidrV4(), self.GetNetworkCidrV6()} {
		if p, err := netip.ParsePrefix(n); err == nil {
			cfg.Routes = append(cfg.Routes, p.Masked())
			cfg.Overlay = append(cfg.Overlay, p.Masked())
		}
	}
	// Serving as subnet router / exit node (only what the admin approved).
	cfg.ForwardRoutes = prefixes(self.GetApprovedRoutes())
	cfg.ExitNode = prefs.AdvertiseExitNode && self.GetExitNodeApproved()
	cfg.Forward = len(cfg.ForwardRoutes) > 0 || cfg.ExitNode
	if prefs.AcceptRoutes {
		for _, p := range nm.Peers {
			for _, r := range prefixes(p.SubnetRoutes) {
				if !slices.Contains(cfg.Routes, r) {
					cfg.Routes = append(cfg.Routes, r)
				}
			}
		}
	}
	if exit != nil {
		cfg.FullTunnel = true
		cfg.KillSwitch = prefs.KillSwitch || nm.GetSettings().GetKillSwitchEnforced()
		cfg.Routes = append(cfg.Routes, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0"))
		if prefs.AllowLAN {
			cfg.ExcludedRoutes = append(cfg.ExcludedRoutes, lanRanges...)
		}
	}
	if d := nm.GetDns(); prefs.UseDNS && d != nil {
		cfg.DNS = []netip.Addr{tunx.DNSv4}
		cfg.Routes = append(cfg.Routes, netip.PrefixFrom(tunx.DNSv4, 32))
		cfg.SearchDomains = d.SearchDomains
		if d.Domain != "" {
			cfg.MatchDomains = append(cfg.MatchDomains, d.Domain)
		}
		for _, sp := range d.Split {
			cfg.MatchDomains = append(cfg.MatchDomains, sp.Domain)
		}
		// Names covered by a domain route must reach our resolver so we can route the answers.
		for _, dr := range nm.DomainRoutes {
			cfg.MatchDomains = append(cfg.MatchDomains, dr.Domain)
		}
		// Custom records outside the network domain ("jellyfin.home.lan", "*.apps.home.lan")
		// are answered by our resolver too, so the system must ask it for those names.
		for _, r := range d.Records {
			name := strings.TrimPrefix(strings.ToLower(strings.TrimSuffix(r.Name, ".")), "*.")
			if name != "" && d.Domain != "" && name != d.Domain && !strings.HasSuffix(name, "."+d.Domain) && !slices.Contains(cfg.MatchDomains, name) {
				cfg.MatchDomains = append(cfg.MatchDomains, name)
			}
		}
		cfg.OverrideDNS = d.OverrideLocalDns || exit != nil
	}
	sort.Slice(cfg.Routes, func(i, j int) bool { return cfg.Routes[i].String() < cfg.Routes[j].String() })
	cfg.RoutesMinusExcluded = subtractPrefixes(cfg.Routes, cfg.ExcludedRoutes)
	return cfg
}

// subtractPrefixes returns base minus excl as a list of prefixes.
func subtractPrefixes(base, excl []netip.Prefix) []netip.Prefix {
	out := append([]netip.Prefix(nil), base...)
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
	if !b.Overlaps(e) || b.Addr().Is4() != e.Addr().Is4() {
		return []netip.Prefix{b}
	}
	if e.Bits() <= b.Bits() {
		return nil
	}
	var out []netip.Prefix
	cur := b
	for cur.Bits() < e.Bits() {
		bits := cur.Bits() + 1
		lo := netip.PrefixFrom(cur.Addr(), bits).Masked()
		raw := lo.Addr().AsSlice()
		raw[(bits-1)/8] |= 0x80 >> ((bits - 1) % 8)
		hiAddr, _ := netip.AddrFromSlice(raw)
		hi := netip.PrefixFrom(hiAddr, bits)
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

// PeerStats are WireGuard counters for one peer.
type PeerStats struct {
	RxBytes, TxBytes uint64
	LastHandshake    int64
}

func (e *engine) stats() map[string]PeerStats {
	out := map[string]PeerStats{}
	s, err := e.dev.IpcGet()
	if err != nil {
		return out
	}
	var cur string
	var st PeerStats
	flush := func() {
		if cur != "" {
			out[cur] = st
		}
	}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			flush()
			cur, st = v, PeerStats{}
		case "rx_bytes":
			st.RxBytes, _ = strconv.ParseUint(v, 10, 64)
		case "tx_bytes":
			st.TxBytes, _ = strconv.ParseUint(v, 10, 64)
		case "last_handshake_time_sec":
			st.LastHandshake, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	flush()
	return out
}

var _ tun.Device = (*tunx.Wrapper)(nil)
