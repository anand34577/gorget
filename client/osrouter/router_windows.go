//go:build windows

package osrouter

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/firewall"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/anand34577/gorget/client"
)

// nrptComment tags the DNS policy rules we create so we only ever remove our own.
const nrptComment = "Gorget"

// fwRuleName is the Windows Defender Firewall rule that lets other devices of the network
// reach this computer through the tunnel. Windows blocks unsolicited inbound traffic
// (including ping) on a new adapter, which looks like "I can ping them but they can't ping me".
// Who may connect is decided by the access rules, which the client enforces itself.
const fwRuleName = "Gorget (traffic from the VPN)"

type windowsRouter struct {
	log *slog.Logger

	mu       sync.Mutex
	luid     winipcfg.LUID
	haveLUID bool
	fwOn     bool
	fwKey    string
	nrptOn   bool
	nrptKey  string
	allowKey string
}

func newRouter(log *slog.Logger) (Router, error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("the Gorget daemon must run as Administrator (it configures network adapters)")
	}
	// Rules from a daemon that crashed would keep steering names to a dead resolver.
	removeNRPT() // synchronous: a late removal would delete the rules we add next
	removeInboundAllow()
	return &windowsRouter{log: log}, nil
}

// Protect binds a socket to the physical default interface so the daemon's own
// traffic (control connection, relay, peer UDP) never enters the tunnel.
//
// It runs from a dialer/listener Control hook, before the socket is bound, so the
// socket's family can't be read with getsockname. Both options are tried instead:
// an IPv4 socket accepts only IP_UNICAST_IF, and a dual-stack IPv6 socket accepts
// both (the IPv4 one covers its IPv4-mapped traffic).
func (r *windowsRouter) Protect(fd uintptr) bool {
	const ipUnicastIf, ipv6UnicastIf = 31, 31
	h := windows.Handle(fd)
	r.mu.Lock()
	own, have := r.luid, r.haveLUID
	r.mu.Unlock()
	// Bind even before the adapter exists: sockets opened now outlive the moment the
	// tunnel takes over the default route.
	ok4, ok6 := false, false
	if idx, found := defaultInterface(windows.AF_INET, own, have); found {
		// IP_UNICAST_IF takes the index in network byte order.
		ok4 = windows.SetsockoptInt(h, windows.IPPROTO_IP, ipUnicastIf, int(htonl(idx))) == nil
	} else {
		ok4 = true // no physical IPv4 route: nothing to bind to
	}
	if idx, found := defaultInterface(windows.AF_INET6, own, have); found {
		ok6 = windows.SetsockoptInt(h, windows.IPPROTO_IPV6, ipv6UnicastIf, int(idx)) == nil
	}
	return ok4 || ok6
}

func htonl(v uint32) uint32 {
	return v<<24 | (v&0xff00)<<8 | (v>>8)&0xff00 | v>>24
}

// defaultInterface returns the interface of the best default route that isn't ours.
func defaultInterface(family winipcfg.AddressFamily, own winipcfg.LUID, haveOwn bool) (uint32, bool) {
	routes, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		return 0, false
	}
	best, bestMetric, found := uint32(0), ^uint32(0), false
	for i := range routes {
		rt := &routes[i]
		if rt.DestinationPrefix.PrefixLength != 0 || (haveOwn && rt.InterfaceLUID == own) {
			continue
		}
		iface, err := rt.InterfaceLUID.IPInterface(family)
		if err != nil {
			continue
		}
		if m := rt.Metric + iface.Metric; m < bestMetric {
			best, bestMetric, found = rt.InterfaceIndex, m, true
		}
	}
	return best, found
}

func (r *windowsRouter) ApplyTUN(cfg client.TUNConfig) (tun.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(cfg.Addresses) == 0 {
		r.teardownLocked()
		return nil, nil
	}
	var dev tun.Device
	if !r.haveLUID {
		d, err := tun.CreateTUN(InterfaceName, cfg.MTU)
		if err != nil {
			return nil, fmt.Errorf("create TUN (is wintun.dll next to gorget.exe?): %w", err)
		}
		nt, ok := d.(*tun.NativeTun)
		if !ok {
			_ = d.Close()
			return nil, errors.New("unexpected TUN device type")
		}
		r.luid, r.haveLUID, dev = winipcfg.LUID(nt.LUID()), true, d
	}
	if err := r.configure(cfg); err != nil {
		return dev, err
	}
	return dev, nil
}

func (r *windowsRouter) configure(cfg client.TUNConfig) error {
	var v4, v6 []netip.Prefix
	for _, a := range cfg.Addresses {
		if a.Addr().Is4() {
			v4 = append(v4, a)
		} else {
			v6 = append(v6, a)
		}
	}
	for fam, addrs := range map[winipcfg.AddressFamily][]netip.Prefix{windows.AF_INET: v4, windows.AF_INET6: v6} {
		if len(addrs) == 0 {
			_ = r.luid.FlushIPAddresses(fam)
			continue
		}
		if err := r.luid.SetIPAddressesForFamily(fam, addrs); err != nil {
			return fmt.Errorf("addresses: %w", err)
		}
		if ifc, err := r.luid.IPInterface(fam); err == nil {
			ifc.NLMTU = uint32(cfg.MTU)
			ifc.UseAutomaticMetric = false
			ifc.Metric = 0 // beats the physical adapter for DNS and routing ties
			ifc.ForwardingEnabled = cfg.Forward
			ifc.DadTransmits = 0
			ifc.RouterDiscoveryBehavior = winipcfg.RouterDiscoveryDisabled
			if err := ifc.Set(); err != nil {
				r.log.Debug("interface settings", "err", err)
			}
		}
	}
	if err := r.setRoutes(cfg); err != nil {
		return fmt.Errorf("routes: %w", err)
	}
	if err := r.setDNS(cfg); err != nil {
		r.log.Warn("DNS configuration failed", "err", err)
	}
	if err := r.setInboundAllow(cfg); err != nil {
		r.log.Warn("couldn't open the Windows firewall for the VPN; other devices may be unable to reach this computer", "err", err)
	}
	if err := r.setKillSwitch(cfg); err != nil {
		r.log.Warn("kill switch", "err", err)
	}
	if cfg.Forward {
		// Windows has no built-in NAT without Hyper-V, so forwarding works for subnet
		// routing between routed networks but an exit node can't masquerade.
		r.log.Warn("this device forwards traffic but Windows can't translate addresses for internet exit traffic; use a Linux device as exit node")
	}
	return nil
}

func (r *windowsRouter) setRoutes(cfg client.TUNConfig) error {
	// Replace the whole table: simpler and correct since the tunnel owns these routes.
	var d4, d6 []*winipcfg.RouteData
	for _, p := range cfg.Routes {
		// Shared networks (subnet routes) get a high metric: when the computer sits in
		// that same network (at home), Windows prefers the directly connected route.
		// The overlay and the default route of an exit node keep metric 0.
		metric := uint32(0)
		if p.Bits() > 0 && !coveredBy(cfg.Overlay, p) && p.Addr() != tunxDNS {
			metric = 500
		}
		if p.Addr().Is4() {
			d4 = append(d4, &winipcfg.RouteData{Destination: p, NextHop: netip.IPv4Unspecified(), Metric: metric})
		} else {
			d6 = append(d6, &winipcfg.RouteData{Destination: p, NextHop: netip.IPv6Unspecified(), Metric: metric})
		}
	}
	// Specific local-network routes already beat 0.0.0.0/0, so LAN access (ExcludedRoutes)
	// needs nothing extra here; the kill switch is what has to be told about it.
	if err := r.luid.SetRoutesForFamily(windows.AF_INET, d4); err != nil {
		return err
	}
	return r.luid.SetRoutesForFamily(windows.AF_INET6, d6)
}

// ---------- DNS ----------

func (r *windowsRouter) setDNS(cfg client.TUNConfig) error {
	var v4, v6 []netip.Addr
	for _, a := range cfg.DNS {
		if a.Is4() {
			v4 = append(v4, a)
		} else {
			v6 = append(v6, a)
		}
	}
	search := cleanDomains(cfg.SearchDomains)
	// The adapter's own DNS servers are only set when Gorget answers everything. In
	// split mode Windows would otherwise send every lookup to the tunnel adapter
	// (it has the lowest metric), so only the NRPT rules below steer Gorget names.
	if len(cfg.DNS) == 0 || !cfg.OverrideDNS {
		v4, v6 = nil, nil
	}
	if err := r.luid.SetDNS(windows.AF_INET, v4, search); err != nil {
		return err
	}
	if err := r.luid.SetDNS(windows.AF_INET6, v6, nil); err != nil {
		return err
	}

	// NRPT: names under the network's domains always go to Gorget DNS, no matter which
	// adapter Windows would otherwise ask. A "." rule sends everything when overriding.
	var namespaces []string
	if len(cfg.DNS) > 0 {
		if cfg.OverrideDNS {
			namespaces = []string{"."}
		} else {
			for _, d := range cleanDomains(cfg.MatchDomains) {
				// ".d" covers names below d; "d" the name itself.
				namespaces = append(namespaces, "."+d, d)
			}
		}
	}
	key := ""
	if len(namespaces) > 0 {
		quoted := make([]string, len(namespaces))
		for i, n := range namespaces {
			quoted[i] = "'" + n + "'"
		}
		servers := make([]string, len(cfg.DNS))
		for i, a := range cfg.DNS {
			servers[i] = "'" + a.String() + "'" // netip.Addr: digits, dots, colons only
		}
		key = strings.Join(quoted, ",") + "|" + strings.Join(servers, ",")
	}
	// PowerShell takes about a second to start, so skip it when nothing changed.
	if key == r.nrptKey && r.nrptOn == (key != "") {
		return nil
	}
	r.clearNRPT()
	if key == "" {
		return nil
	}
	ns, srv, _ := strings.Cut(key, "|")
	script := fmt.Sprintf("Add-DnsClientNrptRule -Namespace %s -NameServers %s -Comment '%s'", ns, srv, nrptComment)
	if err := powershell(script); err != nil {
		return err
	}
	r.nrptOn, r.nrptKey = true, key
	return nil
}

func (r *windowsRouter) clearNRPT() {
	if !r.nrptOn {
		return
	}
	removeNRPT()
	r.nrptOn, r.nrptKey = false, ""
}

// removeNRPT deletes every rule we created, including ones left by a crashed daemon.
func removeNRPT() {
	_ = powershell(fmt.Sprintf("Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '%s' } | Remove-DnsClientNrptRule -Force", nrptComment))
}

func powershell(script string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("powershell: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------- inbound firewall rule ----------

// setInboundAllow allows traffic arriving on the tunnel adapter from the network's own
// addresses. The rule is limited to this adapter and the overlay ranges, so other networks
// stay as protected as before.
func (r *windowsRouter) setInboundAllow(cfg client.TUNConfig) error {
	var remote []string
	for _, p := range cfg.Overlay {
		remote = append(remote, "'"+p.String()+"'") // netip.Prefix: digits, dots, colons and a slash only
	}
	if len(remote) == 0 {
		return nil
	}
	key := strings.Join(remote, ",")
	if key == r.allowKey {
		return nil // PowerShell takes about a second; skip it when nothing changed
	}
	script := fmt.Sprintf("Remove-NetFirewallRule -DisplayName '%[1]s' -ErrorAction SilentlyContinue; "+
		"New-NetFirewallRule -DisplayName '%[1]s' -Direction Inbound -Action Allow -Profile Any "+
		"-InterfaceAlias '%[2]s' -RemoteAddress %[3]s | Out-Null", fwRuleName, InterfaceName, key)
	if err := powershell(script); err != nil {
		return err
	}
	r.allowKey = key
	return nil
}

func removeInboundAllow() {
	_ = powershell(fmt.Sprintf("Remove-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue", fwRuleName))
}

// ---------- kill switch (Windows Filtering Platform) ----------

func (r *windowsRouter) setKillSwitch(cfg client.TUNConfig) error {
	want := cfg.FullTunnel && cfg.KillSwitch
	var dns []netip.Addr
	if cfg.OverrideDNS {
		dns = cfg.DNS
	}
	key := fmt.Sprint(want, r.luid, dns)
	if r.fwOn && key == r.fwKey {
		return nil // unchanged: re-creating the filters would open a brief gap
	}
	if r.fwOn {
		firewall.DisableFirewall()
		r.fwOn, r.fwKey = false, ""
	}
	if !want {
		return nil
	}
	// Blocks everything except the tunnel adapter, DHCP/NDP and this process (which
	// carries the encrypted tunnel traffic). When DNS is overridden, other DNS is blocked too.
	// The filters live in a dynamic WFP session, so they vanish if the daemon dies.
	if err := firewall.EnableFirewall(uint64(r.luid), false, dns); err != nil {
		return err
	}
	r.fwOn, r.fwKey = true, key
	if cfg.AllowLAN {
		r.log.Info("kill switch on: the Windows firewall layer used here also blocks the local network while an exit node is active")
	}
	return nil
}

// ---------- teardown ----------

func (r *windowsRouter) teardownLocked() {
	r.clearNRPT()
	if r.allowKey != "" {
		removeInboundAllow()
		r.allowKey = ""
	}
	if r.fwOn {
		firewall.DisableFirewall()
		r.fwOn, r.fwKey = false, ""
	}
	// The adapter itself is closed by the engine; forget it so the next connect creates a new one.
	r.luid, r.haveLUID = 0, false
}

func (r *windowsRouter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.teardownLocked()
	return nil
}

// tunxDNS is the in-tunnel resolver address (always routed into the tunnel).
var tunxDNS = netip.MustParseAddr("100.100.100.100")

// coveredBy reports whether p lies inside one of the prefixes.
func coveredBy(ps []netip.Prefix, p netip.Prefix) bool {
	for _, q := range ps {
		if q.Bits() <= p.Bits() && q.Contains(p.Addr()) {
			return true
		}
	}
	return false
}
