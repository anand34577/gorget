//go:build linux && !android

package osrouter

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/internal/netfw"
)

// Policy routing (same idea as other mesh VPNs): our routes live in table 52;
// sockets marked with fwmark bypass it, and the main table keeps priority for
// everything except its default route.
const (
	routeTable   = 52
	fwmark       = 0x80000
	fwmarkMask   = 0xff0000
	prioBypass   = 5210
	prioMainNoDf = 5230
	prioTable    = 5270
	nftKill      = "gorget_ks"
	nftFwd       = "gorget_fwd"
	nftApp       = "gorget_app"
	// cgroupName is the control group whose processes bypass the tunnel.
	cgroupName = "gorget-bypass"
)

type linuxRouter struct {
	log *slog.Logger

	mu       sync.Mutex
	name     string
	idx      int
	routes   []netip.Prefix
	throws   []netip.Prefix
	rules    bool
	dnsMode  string // "resolved", "file" or ""
	dnsKey   string // last DNS settings applied (skip re-running resolvectl when unchanged)
	resolvBk []byte
	nftOn    map[string]bool
	nftLast  map[string]string // last ruleset loaded per table
	fwdOpen  bool              // legacy FORWARD chain opened for our interface
}

func newRouter(log *slog.Logger) (Router, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("the Gorget daemon must run as root (it configures network interfaces)")
	}
	r := &linuxRouter{log: log, nftOn: map[string]bool{}, nftLast: map[string]string{}}
	r.recoverFromCrash()
	return r, nil
}

// resolvBackup keeps the original /etc/resolv.conf on disk while we override it, so a
// crash or power loss can't leave the machine pointing at a resolver that is gone.
const resolvBackup = "/var/lib/gorget/resolv.conf.orig"

const resolvMark = "# Written by Gorget"

// recoverFromCrash undoes what a previous daemon left behind if it didn't exit
// cleanly: our nftables tables (a stale kill switch would block all traffic) and an
// overridden resolv.conf.
func (r *linuxRouter) recoverFromCrash() {
	for _, t := range []string{nftKill, nftFwd, nftApp} {
		_ = exec.Command("nft", "delete", "table", "inet", t).Run()
	}
	orig, err := os.ReadFile(resolvBackup)
	if err != nil {
		return
	}
	if cur, err := os.ReadFile("/etc/resolv.conf"); err == nil && strings.HasPrefix(string(cur), resolvMark) {
		if err := os.WriteFile("/etc/resolv.conf", orig, 0o644); err == nil {
			r.log.Info("restored /etc/resolv.conf left over from an unclean shutdown")
		}
	}
	_ = os.Remove(resolvBackup)
}

// Protect marks a socket so its traffic uses the main routing table.
func (r *linuxRouter) Protect(fd uintptr) bool {
	return unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, fwmark) == nil
}

func (r *linuxRouter) ApplyTUN(cfg client.TUNConfig) (tun.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(cfg.Addresses) == 0 {
		r.teardownLocked()
		return nil, nil
	}
	var dev tun.Device
	if r.name == "" {
		d, err := tun.CreateTUN(InterfaceName, cfg.MTU)
		if err != nil {
			return nil, fmt.Errorf("create TUN: %w", err)
		}
		name, _ := d.Name()
		r.name, dev = name, d
	}
	link, err := netlink.LinkByName(r.name)
	if err != nil {
		return dev, err
	}
	r.idx = link.Attrs().Index
	if err := netlink.LinkSetMTU(link, cfg.MTU); err != nil {
		return dev, err
	}
	if err := r.setAddresses(link, cfg.Addresses); err != nil {
		return dev, err
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return dev, err
	}
	if err := r.ensureRules(); err != nil {
		return dev, fmt.Errorf("routing rules: %w", err)
	}
	if err := r.setRoutes(cfg.Routes, cfg.ExcludedRoutes); err != nil {
		return dev, fmt.Errorf("routes: %w", err)
	}
	if err := r.setDNS(cfg); err != nil {
		r.log.Warn("DNS configuration failed", "err", err)
	}
	if err := r.setKillSwitch(cfg); err != nil {
		r.log.Warn("kill switch", "err", err)
	}
	if err := r.setForwarding(cfg); err != nil {
		r.log.Warn("forwarding", "err", err)
	}
	return dev, nil
}

func (r *linuxRouter) setAddresses(link netlink.Link, want []netip.Prefix) error {
	have, _ := netlink.AddrList(link, netlink.FAMILY_ALL)
	keep := map[string]bool{}
	for _, p := range want {
		keep[p.String()] = true
	}
	for _, a := range have {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		ones, _ := a.Mask.Size()
		ip, _ := netip.AddrFromSlice(a.IP)
		if !keep[netip.PrefixFrom(ip.Unmap(), ones).String()] {
			_ = netlink.AddrDel(link, &a)
		}
	}
	for _, p := range want {
		ipn := ipNet(p)
		if err := netlink.AddrReplace(link, &netlink.Addr{IPNet: &ipn}); err != nil {
			return fmt.Errorf("address %s: %w", p, err)
		}
	}
	return nil
}

func (r *linuxRouter) ensureRules() error {
	if r.rules {
		return nil
	}
	for _, fam := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		bypass := netlink.NewRule()
		bypass.Priority, bypass.Family, bypass.Table = prioBypass, fam, unix.RT_TABLE_MAIN
		bypass.Mark, bypass.Mask = fwmark, ptr(uint32(fwmarkMask))
		main := netlink.NewRule()
		main.Priority, main.Family, main.Table, main.SuppressPrefixlen = prioMainNoDf, fam, unix.RT_TABLE_MAIN, 0
		ours := netlink.NewRule()
		ours.Priority, ours.Family, ours.Table = prioTable, fam, routeTable
		for _, rule := range []*netlink.Rule{bypass, main, ours} {
			_ = netlink.RuleDel(rule)
			if err := netlink.RuleAdd(rule); err != nil && !errors.Is(err, unix.EEXIST) {
				if fam == netlink.FAMILY_V6 {
					r.log.Debug("IPv6 rule", "err", err)
					continue
				}
				return err
			}
		}
	}
	r.rules = true
	return nil
}

func (r *linuxRouter) setRoutes(routes, excluded []netip.Prefix) error {
	add, del := diffPrefixes(r.routes, routes)
	for _, p := range del {
		ipn := ipNet(p)
		_ = netlink.RouteDel(&netlink.Route{LinkIndex: r.idx, Dst: &ipn, Table: routeTable})
	}
	var errs []error
	for _, p := range add {
		ipn := ipNet(p)
		if err := netlink.RouteReplace(&netlink.Route{LinkIndex: r.idx, Dst: &ipn, Table: routeTable, Scope: netlink.SCOPE_LINK}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
		}
	}
	r.routes = append([]netip.Prefix(nil), routes...)
	// "throw" routes send excluded ranges (LAN access) back to the main table.
	tadd, tdel := diffPrefixes(r.throws, excluded)
	for _, p := range tdel {
		ipn := ipNet(p)
		_ = netlink.RouteDel(&netlink.Route{Dst: &ipn, Table: routeTable, Type: unix.RTN_THROW})
	}
	for _, p := range tadd {
		ipn := ipNet(p)
		if err := netlink.RouteReplace(&netlink.Route{Dst: &ipn, Table: routeTable, Type: unix.RTN_THROW}); err != nil {
			errs = append(errs, fmt.Errorf("throw %s: %w", p, err))
		}
	}
	r.throws = append([]netip.Prefix(nil), excluded...)
	return errors.Join(errs...)
}

// ---------- DNS ----------

func (r *linuxRouter) setDNS(cfg client.TUNConfig) error {
	if len(cfg.DNS) == 0 {
		return r.resetDNS()
	}
	match, search := cleanDomains(cfg.MatchDomains), cleanDomains(cfg.SearchDomains)
	key := fmt.Sprint(r.name, cfg.DNS, match, search, cfg.OverrideDNS)
	if key == r.dnsKey {
		return nil // unchanged
	}
	if _, err := exec.LookPath("resolvectl"); err == nil && isResolvedActive() {
		args := []string{"dns", r.name}
		for _, a := range cfg.DNS {
			args = append(args, a.String())
		}
		if err := run("resolvectl", args...); err != nil {
			return err
		}
		domains := []string{"domain", r.name}
		for _, d := range match {
			domains = append(domains, "~"+d)
		}
		domains = append(domains, search...)
		if cfg.OverrideDNS {
			domains = append(domains, "~.")
		}
		if err := run("resolvectl", domains...); err != nil {
			return err
		}
		_ = run("resolvectl", "default-route", r.name, boolStr(cfg.OverrideDNS))
		r.dnsMode, r.dnsKey = "resolved", key
		return nil
	}
	// Without systemd-resolved only full override is possible (rewrite resolv.conf).
	if !cfg.OverrideDNS {
		_ = r.resetDNS()
		r.log.Info("systemd-resolved not found: device names resolve only when DNS override is on")
		r.dnsKey = key
		return nil
	}
	if r.dnsMode != "file" {
		b, err := os.ReadFile("/etc/resolv.conf")
		if err != nil {
			return err
		}
		r.resolvBk = b
		_ = os.MkdirAll(filepath.Dir(resolvBackup), 0o700)
		if err := os.WriteFile(resolvBackup, b, 0o600); err != nil {
			return fmt.Errorf("back up /etc/resolv.conf: %w", err)
		}
	}
	var sb strings.Builder
	sb.WriteString(resolvMark + "; restored when Gorget disconnects.\n")
	for _, a := range cfg.DNS {
		fmt.Fprintf(&sb, "nameserver %s\n", a)
	}
	if all := cleanDomains(append(append([]string{}, match...), search...)); len(all) > 0 {
		fmt.Fprintf(&sb, "search %s\n", strings.Join(all, " "))
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte(sb.String()), 0o644); err != nil {
		return err
	}
	r.dnsMode, r.dnsKey = "file", key
	return nil
}

func (r *linuxRouter) resetDNS() error {
	switch r.dnsMode {
	case "resolved":
		_ = run("resolvectl", "revert", r.name)
	case "file":
		if r.resolvBk != nil {
			if err := os.WriteFile("/etc/resolv.conf", r.resolvBk, 0o644); err == nil {
				_ = os.Remove(resolvBackup)
			}
		}
	}
	r.dnsMode, r.dnsKey = "", ""
	return nil
}

func isResolvedActive() bool {
	return exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run() == nil
}

// ---------- kill switch & forwarding (nftables) ----------

func (r *linuxRouter) setKillSwitch(cfg client.TUNConfig) error {
	if !(cfg.FullTunnel && cfg.KillSwitch) {
		return r.nftDelete(nftKill)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "table inet %s\ndelete table inet %s\ntable inet %s {\n", nftKill, nftKill, nftKill)
	sb.WriteString("\tchain output {\n\t\ttype filter hook output priority filter; policy accept;\n")
	sb.WriteString("\t\toifname \"lo\" accept\n")
	fmt.Fprintf(&sb, "\t\toifname %q accept\n", r.name)
	fmt.Fprintf(&sb, "\t\tmeta mark & 0x%x == 0x%x accept\n", fwmarkMask, fwmark)
	sb.WriteString("\t\tudp dport { 67, 68 } accept\n\t\tmeta l4proto ipv6-icmp accept\n")
	if cfg.AllowLAN {
		sb.WriteString("\t\tip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 } accept\n")
		sb.WriteString("\t\tip6 daddr { fe80::/10, fc00::/7 } accept\n")
	}
	sb.WriteString("\t\treject\n\t}\n}\n")
	return r.nftApply(nftKill, sb.String())
}

func (r *linuxRouter) setForwarding(cfg client.TUNConfig) error {
	if !cfg.Forward {
		if r.fwdOpen {
			netfw.Revoke(r.name)
			r.fwdOpen = false
		}
		return r.nftDelete(nftFwd)
	}
	if err := os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644); err != nil {
		r.log.Warn("could not enable IPv4 forwarding", "err", err)
	}
	var v4, v6 []string
	for _, p := range cfg.Overlay {
		if p.Addr().Is4() {
			v4 = append(v4, p.String())
		} else {
			v6 = append(v6, p.String())
		}
	}
	if len(v6) > 0 {
		// With forwarding on, the kernel ignores router advertisements unless accept_ra
		// is 2; keep SLAAC working on the uplinks first.
		keepAcceptRA(r.name)
		_ = os.WriteFile("/proc/sys/net/ipv6/conf/all/forwarding", []byte("1"), 0o644)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "table inet %s\ndelete table inet %s\ntable inet %s {\n", nftFwd, nftFwd, nftFwd)
	sb.WriteString("\tchain forward {\n\t\ttype filter hook forward priority filter; policy accept;\n")
	sb.WriteString("\t\ttcp flags syn tcp option maxseg size set rt mtu\n")
	fmt.Fprintf(&sb, "\t\tiifname %q accept\n\t\toifname %q ct state established,related accept\n\t}\n", r.name, r.name)
	sb.WriteString("\tchain postrouting {\n\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	if len(v4) > 0 {
		fmt.Fprintf(&sb, "\t\tip saddr { %s } oifname != %q masquerade\n", strings.Join(v4, ", "), r.name)
	}
	if len(v6) > 0 {
		fmt.Fprintf(&sb, "\t\tip6 saddr { %s } oifname != %q masquerade\n", strings.Join(v6, ", "), r.name)
	}
	sb.WriteString("\t}\n}\n")
	if err := r.nftApply(nftFwd, sb.String()); err != nil {
		return err
	}
	netfw.Allow(r.name)
	r.fwdOpen = true
	return nil
}

func (r *linuxRouter) nftApply(table, rules string) error {
	if r.nftOn[table] && r.nftLast[table] == rules {
		return nil // unchanged: don't spawn nft again
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return errors.New("nftables (nft) is not installed; install the nftables package for the kill switch, subnet routing and exit-node features")
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %s", strings.TrimSpace(string(out)))
	}
	r.nftOn[table], r.nftLast[table] = true, rules
	return nil
}

func (r *linuxRouter) nftDelete(table string) error {
	if !r.nftOn[table] {
		return nil
	}
	_ = exec.Command("nft", "delete", "table", "inet", table).Run()
	delete(r.nftOn, table)
	delete(r.nftLast, table)
	return nil
}

// keepAcceptRA sets accept_ra=2 on interfaces that accept router advertisements, so
// turning on IPv6 forwarding doesn't drop their SLAAC addresses and default route.
func keepAcceptRA(own string) {
	ifs, err := net.Interfaces()
	if err != nil {
		return
	}
	for _, ifc := range ifs {
		if ifc.Name == own || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		p := "/proc/sys/net/ipv6/conf/" + ifc.Name + "/accept_ra"
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == "1" {
			_ = os.WriteFile(p, []byte("2"), 0o644)
		}
	}
}

// ---------- teardown ----------

// BypassApp puts a process into a control group whose sockets are marked like our own,
// so they use the normal route even while an exit node carries everything else.
func (r *linuxRouter) BypassApp(pid int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cg := "/sys/fs/cgroup/" + cgroupName
	if err := os.MkdirAll(cg, 0o755); err != nil {
		return fmt.Errorf("control groups (cgroup v2) are required for per-app routing: %w", err)
	}
	if !r.nftOn[nftApp] {
		rules := fmt.Sprintf("table inet %s\ndelete table inet %s\ntable inet %s {\n\tchain output {\n\t\ttype route hook output priority mangle; policy accept;\n\t\tsocket cgroupv2 level 1 %q meta mark set meta mark | 0x%x\n\t}\n}\n",
			nftApp, nftApp, nftApp, cgroupName, fwmark)
		if err := r.nftApply(nftApp, rules); err != nil {
			return err
		}
	}
	return os.WriteFile(cg+"/cgroup.procs", []byte(fmt.Sprint(pid)), 0o644)
}

func (r *linuxRouter) teardownLocked() {
	_ = r.resetDNS()
	_ = r.nftDelete(nftKill)
	if r.fwdOpen {
		netfw.Revoke(r.name)
		r.fwdOpen = false
	}
	_ = r.nftDelete(nftFwd)
	_ = r.nftDelete(nftApp)
	_ = os.Remove("/sys/fs/cgroup/" + cgroupName) // only succeeds once its processes ended
	_ = r.setRoutes(nil, nil)
	if r.rules {
		for _, fam := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
			for _, prio := range []int{prioBypass, prioMainNoDf, prioTable} {
				rule := netlink.NewRule()
				rule.Priority, rule.Family = prio, fam
				_ = netlink.RuleDel(rule)
			}
		}
		r.rules = false
	}
	// The TUN itself is closed by the engine; forget it so the next connect creates a new one.
	r.name, r.idx = "", 0
}

func (r *linuxRouter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.teardownLocked()
	return nil
}

// ---------- helpers ----------

func ipNet(p netip.Prefix) net.IPNet {
	return net.IPNet{IP: p.Addr().AsSlice(), Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen())}
}

func ptr[T any](v T) *T { return &v }

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
