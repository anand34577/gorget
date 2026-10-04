// Package gateway runs the built-in WireGuard gateway: it terminates standard
// WireGuard clients, bridges them into the mesh, enforces ACLs with nftables,
// NATs exit traffic and serves DNS on its overlay address.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/dnsserver"
	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/store"
)

// Status is reported in the admin console.
type Status struct {
	Enabled   bool      `json:"enabled"`
	Running   bool      `json:"running"`
	Backend   string    `json:"backend"`
	Interface string    `json:"interface"`
	Endpoint  string    `json:"endpoint"`
	PublicKey string    `json:"public_key"`
	Peers     int       `json:"peers"`
	Error     string    `json:"error,omitempty"`
	AppliedAt time.Time `json:"applied_at"`
	Serial    uint64    `json:"serial"`
}

// dataplane is implemented per OS.
type dataplane interface {
	// Setup creates the interface with the given addresses.
	Setup(addr4, addr6 netip.Prefix, mtu, port int, key wgtypes.Key) error
	ConfigurePeers(peers []wgtypes.PeerConfig) error
	SetRoutes(routes []netip.Prefix) error
	ApplyFirewall(ruleset string) error
	Peers() ([]wgtypes.Peer, error)
	Backend() string
	Close() error
}

type Gateway struct {
	cfg  config.GatewayConfig
	core *core.Core
	log  *slog.Logger
	dns  *dnsserver.Server

	mu     sync.Mutex
	status Status
	dp     dataplane
	setup  struct {
		addr4, addr6 netip.Prefix
	}
	lastRules string
	known     map[string]bool // public keys currently configured
	// Per standard client: receive counter at the last sample, and when it last changed or a DB write happened.
	rx      map[string]int64
	touched map[string]time.Time
}

func New(c *core.Core, log *slog.Logger) *Gateway {
	g := &Gateway{cfg: c.Cfg.Gateway, core: c, log: log, dns: dnsserver.New(log), known: map[string]bool{}, rx: map[string]int64{}, touched: map[string]time.Time{}}
	g.status = Status{Enabled: g.cfg.Enabled, Interface: g.cfg.Interface, Endpoint: g.cfg.Endpoint, PublicKey: c.GatewayPublicKey().String()}
	return g
}

func (g *Gateway) Status() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.status
}

func (g *Gateway) setErr(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		g.status.Error = err.Error()
	} else {
		g.status.Error = ""
	}
}

// Run applies every new snapshot until ctx is done.
func (g *Gateway) Run(ctx context.Context) {
	if !g.cfg.Enabled {
		return
	}
	dp, err := newDataplane(g.cfg, g.log)
	if err != nil {
		g.log.Warn("WireGuard gateway unavailable", "err", err)
		g.setErr(err)
		return
	}
	g.dp = dp
	defer func() {
		g.dns.Shutdown()
		if err := dp.Close(); err != nil {
			g.log.Warn("gateway shutdown", "err", err)
		}
	}()
	snaps := g.core.Coord.Subscribe()
	stats := time.NewTicker(5 * time.Second) // fast enough that the console notices a client within seconds
	defer stats.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-snaps:
			if err := g.apply(s); err != nil {
				g.log.Error("gateway apply failed", "err", err)
				g.setErr(err)
			} else {
				g.setErr(nil)
			}
		case <-stats.C:
			g.collectStats(ctx)
		}
	}
}

func (g *Gateway) apply(s *core.Snapshot) error {
	v := g.core.GatewayView(s)
	if v == nil {
		return fmt.Errorf("gateway device missing")
	}
	a4 := netip.PrefixFrom(v.Addr4, v.Net4.Bits())
	a6 := netip.Prefix{}
	if v.IPv6On {
		a6 = netip.PrefixFrom(v.Addr6, 64)
	}
	if a4 != g.setup.addr4 || a6 != g.setup.addr6 {
		if err := g.dp.Setup(a4, a6, g.cfg.MTU, g.cfg.ListenPort, g.core.GatewayPrivateKey()); err != nil {
			return fmt.Errorf("interface setup: %w", err)
		}
		g.setup.addr4, g.setup.addr6 = a4, a6
		g.lastRules = ""
		if g.cfg.DNS {
			g.dns.Shutdown()
			if err := g.dns.Listen(net.JoinHostPort(v.Addr4.String(), "53")); err != nil {
				g.log.Warn("gateway DNS", "err", err)
			}
			if v.IPv6On {
				if err := g.dns.Listen(net.JoinHostPort(v.Addr6.String(), "53")); err != nil {
					g.log.Warn("gateway DNS (IPv6)", "err", err)
				}
			}
		}
	}

	// Peers.
	want := map[string]bool{}
	var pcs []wgtypes.PeerConfig
	for _, p := range v.Peers {
		key, err := wgtypes.ParseKey(p.PublicKey)
		if err != nil {
			continue
		}
		pc := wgtypes.PeerConfig{PublicKey: key, ReplaceAllowedIPs: true, UpdateOnly: false}
		for _, a := range p.AllowedIPs {
			pc.AllowedIPs = append(pc.AllowedIPs, prefixToIPNet(a))
		}
		if p.PSK != "" {
			if psk, err := wgtypes.ParseKey(p.PSK); err == nil {
				pc.PresharedKey = &psk
			}
		}
		want[p.PublicKey] = true
		pcs = append(pcs, pc)
	}
	for k := range g.known {
		if !want[k] {
			if key, err := wgtypes.ParseKey(k); err == nil {
				pcs = append(pcs, wgtypes.PeerConfig{PublicKey: key, Remove: true})
			}
		}
	}
	if err := g.dp.ConfigurePeers(pcs); err != nil {
		return fmt.Errorf("configure peers: %w", err)
	}
	g.known = want

	// Routes to subnets behind native peers (overlay is covered by the interface address).
	var routes []netip.Prefix
	for _, p := range v.Peers {
		for _, a := range p.AllowedIPs {
			if !v.Net4.Overlaps(a) && !(v.IPv6On && v.Net6.Overlaps(a)) {
				routes = append(routes, a)
			}
		}
	}
	if err := g.dp.SetRoutes(routes); err != nil {
		g.log.Warn("gateway routes", "err", err)
	}

	// Firewall.
	egress := g.cfg.EgressInterface
	var rules string
	if rr, ok := g.dp.(interface {
		RenderFirewall(iface, egress string, v *core.GatewayView) string
	}); ok {
		rules = rr.RenderFirewall(g.cfg.Interface, egress, v) // macOS: pf instead of nftables
	} else {
		rules = BuildRuleset(g.cfg.Interface, egress, v)
	}
	if rules != g.lastRules {
		if err := g.dp.ApplyFirewall(rules); err != nil {
			return fmt.Errorf("firewall: %w", err)
		}
		g.lastRules = rules
	}

	// DNS.
	if g.cfg.DNS {
		split := map[string][]string{}
		for _, sp := range v.DNS.Split {
			split[strings.ToLower(sp.Domain)] = sp.Nameservers
		}
		allow := []netip.Prefix{v.Net4}
		if v.IPv6On {
			allow = append(allow, v.Net6)
		}
		g.dns.SetConfig(&dnsserver.Config{
			Domain: v.DNS.Domain, Hosts: v.DNS.Hosts, CNAMEs: v.DNS.CNAMEs,
			Nameservers: v.DNS.Nameservers, Split: split, AllowFrom: allow, FailOpen: v.DNS.FailOpen,
		})
	}

	g.mu.Lock()
	g.status.Running = true
	g.status.Backend = g.dp.Backend()
	g.status.Peers = len(v.Peers)
	g.status.AppliedAt = time.Now()
	g.status.Serial = v.Serial
	g.mu.Unlock()
	return nil
}

// collectStats samples the standard WireGuard peers. A client counts as active when its
// receive counter moved since the last sample or it just completed a handshake; that is
// reported to the core right away, while the database is only updated about twice a minute.
func (g *Gateway) collectStats(ctx context.Context) {
	peers, err := g.dp.Peers()
	if err != nil {
		return
	}
	s := g.core.Coord.Snapshot()
	byKey := map[string]*store.Device{}
	for _, d := range s.Devices {
		if d.Kind == store.KindWireGuard {
			byKey[d.WGPublicKey] = d
		}
	}
	now := time.Now()
	for _, p := range peers {
		key := p.PublicKey.String()
		d := byKey[key]
		if d == nil {
			continue
		}
		active := false
		if !p.LastHandshakeTime.IsZero() {
			g.core.NoteWGActivity(d.ID, p.LastHandshakeTime)
			active = true
		}
		if prev, ok := g.rx[key]; ok && p.ReceiveBytes > prev {
			g.core.NoteWGActivity(d.ID, now)
		}
		g.rx[key] = p.ReceiveBytes
		if !active || now.Sub(g.touched[key]) < 30*time.Second {
			continue
		}
		g.touched[key] = now
		ep := ""
		if p.Endpoint != nil {
			ep = p.Endpoint.String()
		}
		_ = g.core.Store.TouchDevice(ctx, d.ID, p.LastHandshakeTime.Unix(), ep, p.ReceiveBytes, p.TransmitBytes)
	}
}

func prefixToIPNet(p netip.Prefix) net.IPNet {
	return net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())}
}

// BuildRuleset renders the nftables ruleset (atomic replacement of table inet gorget).
func BuildRuleset(iface, egress string, v *core.GatewayView) string {
	var b strings.Builder
	b.WriteString("table inet gorget\ndelete table inet gorget\ntable inet gorget {\n")

	// NAT for exit traffic.
	b.WriteString("\tchain postrouting {\n\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	v4src, v6src := splitFamilies(v.ExitSources)
	oif := fmt.Sprintf("oifname != %q", iface)
	if egress != "" {
		oif = fmt.Sprintf("oifname %q", egress)
	}
	if len(v4src) > 0 {
		fmt.Fprintf(&b, "\t\tip saddr %s %s masquerade\n", set(v4src), oif)
	}
	if len(v6src) > 0 {
		fmt.Fprintf(&b, "\t\tip6 saddr %s %s masquerade\n", set(v6src), oif)
	}
	b.WriteString("\t}\n")

	// Input: only DNS and ICMP to the gateway itself from the overlay.
	fmt.Fprintf(&b, "\tchain input {\n\t\ttype filter hook input priority filter; policy accept;\n")
	fmt.Fprintf(&b, "\t\tiifname %q ct state established,related accept\n", iface)
	fmt.Fprintf(&b, "\t\tiifname %q meta l4proto { icmp, ipv6-icmp } accept\n", iface)
	fmt.Fprintf(&b, "\t\tiifname %q meta l4proto { tcp, udp } th dport 53 accept\n", iface)
	fmt.Fprintf(&b, "\t\tiifname %q drop\n\t}\n", iface)

	// Forwarding: default deny for traffic entering or leaving the gateway interface.
	fmt.Fprintf(&b, "\tchain forward {\n\t\ttype filter hook forward priority filter; policy accept;\n")
	b.WriteString("\t\ttcp flags syn tcp option maxseg size set rt mtu\n") // a smaller MTU on the way would stall big downloads
	fmt.Fprintf(&b, "\t\tiifname %q jump from_overlay\n", iface)
	fmt.Fprintf(&b, "\t\toifname %q jump to_overlay\n\t}\n", iface)

	b.WriteString("\tchain from_overlay {\n\t\tct state established,related accept\n\t\tct state invalid drop\n")
	for _, line := range ruleLines(v.Forward) {
		b.WriteString("\t\t" + line + "\n")
	}
	b.WriteString("\t\tdrop\n\t}\n")
	b.WriteString("\tchain to_overlay {\n\t\tct state established,related accept\n\t\tdrop\n\t}\n")
	b.WriteString("}\n")
	return b.String()
}

func ruleLines(rules []policy.FirewallRule) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rules {
		for _, fam := range []string{"ip", "ip6"} {
			src := family(r.Src, fam)
			dst := family(r.Dst, fam)
			if len(src) == 0 || len(dst) == 0 {
				continue
			}
			var parts []string
			if !isAll(src, fam) {
				parts = append(parts, fmt.Sprintf("%s saddr %s", fam, set(src)))
			}
			if excl := family(r.Exclude, fam); len(excl) > 0 {
				parts = append(parts, fmt.Sprintf("%s daddr != %s", fam, set(excl)))
			}
			if !isAll(dst, fam) {
				parts = append(parts, fmt.Sprintf("%s daddr %s", fam, set(dst)))
			}
			parts = append(parts, protoMatch(r, fam)...)
			line := strings.Join(append(parts, "accept"), " ")
			if r.RuleID != "" {
				line += fmt.Sprintf(" comment %q", "rule:"+sanitizeComment(r.RuleID))
			}
			if !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out
}

func protoMatch(r policy.FirewallRule, fam string) []string {
	allPorts := len(r.Ports) == 0 || (len(r.Ports) == 1 && r.Ports[0] == policy.AllPorts[0])
	switch r.Proto {
	case "icmp":
		if fam == "ip" {
			return []string{"meta l4proto icmp"}
		}
		return []string{"meta l4proto ipv6-icmp"}
	case "tcp", "udp":
		if allPorts {
			return []string{"meta l4proto " + r.Proto}
		}
		return []string{fmt.Sprintf("%s dport %s", r.Proto, portSet(r.Ports))}
	}
	if allPorts {
		return nil
	}
	return []string{"meta l4proto { tcp, udp }", "th dport " + portSet(r.Ports)}
}

func portSet(ps []policy.PortRange) string {
	var items []string
	for _, p := range ps {
		if p.First == p.Last {
			items = append(items, fmt.Sprint(p.First))
		} else {
			items = append(items, fmt.Sprintf("%d-%d", p.First, p.Last))
		}
	}
	return "{ " + strings.Join(items, ", ") + " }"
}

func family(ps []netip.Prefix, fam string) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range ps {
		if (fam == "ip") == p.Addr().Is4() {
			out = append(out, p)
		}
	}
	return out
}

func splitFamilies(ps []netip.Prefix) (v4, v6 []netip.Prefix) {
	return family(ps, "ip"), family(ps, "ip6")
}

func isAll(ps []netip.Prefix, fam string) bool {
	for _, p := range ps {
		if p.Bits() == 0 {
			return true
		}
	}
	return false
}

// collapse removes prefixes covered by others (nftables rejects overlapping set elements).
func collapse(ps []netip.Prefix) []netip.Prefix {
	sorted := append([]netip.Prefix{}, ps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Bits() < sorted[j].Bits() })
	var out []netip.Prefix
	for _, p := range sorted {
		covered := false
		for _, k := range out {
			if k.Bits() <= p.Bits() && k.Contains(p.Addr()) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p.Masked())
		}
	}
	return out
}

func set(ps []netip.Prefix) string {
	ps = collapse(ps)
	items := make([]string, 0, len(ps))
	seen := map[string]bool{}
	for _, p := range ps {
		s := p.String()
		if p.IsSingleIP() {
			s = p.Addr().String()
		}
		if !seen[s] {
			seen[s] = true
			items = append(items, s)
		}
	}
	sort.Strings(items)
	return "{ " + strings.Join(items, ", ") + " }"
}

func sanitizeComment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}
