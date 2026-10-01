//go:build darwin

package osrouter

import (
	"encoding/json"
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
	"time"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/anand34577/gorget/client"
)

const (
	resolverDir = "/etc/resolver"
	// resolverMark identifies resolver files we wrote, so we never touch anyone else's.
	resolverMark = "# Managed by Gorget"
	pfAnchor     = "gorget"
	pfRulesFile  = "/var/run/gorget-pf.conf"
)

type darwinRouter struct {
	log *slog.Logger

	mu       sync.Mutex
	name     string
	routes   []netip.Prefix
	addrs    []netip.Prefix
	resolver []string          // files written under /etc/resolver
	savedDNS map[string]string // network service -> original DNS servers ("Empty" when automatic)
	dnsKey   string            // last DNS settings applied
	pfOn     bool
	pfWasOn  bool
	fwdWas   string
	fwdOn    bool

	ifMu    sync.Mutex
	defIf4  uint32
	defIf6  uint32
	ifAt    time.Time // when defIf4/defIf6 were looked up
	pfRules string    // last anchor ruleset loaded
}

// savedDNSFile keeps the network services' original DNS servers on disk while we
// override them, so a crash can't leave the Mac pointing at a resolver that is gone.
const savedDNSFile = "/Library/Application Support/Gorget/saved-dns.json"

func newRouter(log *slog.Logger) (Router, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("the Gorget daemon must run as root (it configures network interfaces)")
	}
	r := &darwinRouter{log: log, savedDNS: map[string]string{}}
	r.recoverFromCrash()
	return r, nil
}

// recoverFromCrash undoes what a daemon that didn't exit cleanly left behind:
// resolver files, overridden DNS servers and the pf anchor (a stale kill switch
// would block all traffic).
func (r *darwinRouter) recoverFromCrash() {
	if entries, err := os.ReadDir(resolverDir); err == nil {
		for _, e := range entries {
			p := filepath.Join(resolverDir, e.Name())
			if b, err := os.ReadFile(p); err == nil && strings.HasPrefix(string(b), resolverMark) {
				_ = os.Remove(p)
			}
		}
	}
	if b, err := os.ReadFile(savedDNSFile); err == nil {
		saved := map[string]string{}
		if json.Unmarshal(b, &saved) == nil && len(saved) > 0 {
			r.savedDNS = saved
			_ = r.restoreServiceDNS()
			r.log.Info("restored DNS settings left over from an unclean shutdown")
		}
		_ = os.Remove(savedDNSFile)
	}
	if _, err := os.Stat(pfRulesFile); err == nil {
		_ = run("pfctl", "-a", pfAnchor, "-F", "all")
		_ = os.Remove(pfRulesFile)
	}
}

// Protect binds a socket to the physical default interface (IP_BOUND_IF) so the
// daemon's own traffic never enters the tunnel.
func (r *darwinRouter) Protect(fd uintptr) bool {
	idx4, idx6 := r.defaultInterfaces()
	ok := true
	if idx4 != 0 {
		// Fails with EINVAL on IPv6-only sockets; try the IPv6 option for those.
		if err := unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, int(idx4)); err != nil && idx6 == 0 {
			ok = false
		}
	}
	if idx6 != 0 {
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, int(idx6))
	}
	return ok
}

// defaultInterfaces finds the interfaces behind the default routes, ignoring our own tunnel.
func (r *darwinRouter) defaultInterfaces() (uint32, uint32) {
	r.mu.Lock()
	own := r.name
	r.mu.Unlock()
	find := func(args ...string) uint32 {
		out, err := exec.Command("route", append([]string{"-n", "get"}, args...)...).Output()
		if err != nil {
			return 0
		}
		for _, line := range strings.Split(string(out), "\n") {
			if f, ok := strings.CutPrefix(strings.TrimSpace(line), "interface:"); ok {
				name := strings.TrimSpace(f)
				if name == own {
					return 0
				}
				if ifc, err := netInterfaceIndex(name); err == nil {
					return ifc
				}
			}
		}
		return 0
	}
	// "default" still resolves to the physical gateway: our tunnel only adds the 0/1 halves.
	r.ifMu.Lock()
	defer r.ifMu.Unlock()
	// Every socket (including each upstream DNS query) asks, and the lookup starts two
	// processes, so reuse a recent answer. Network changes rebind sockets after 5s anyway.
	if time.Since(r.ifAt) < 3*time.Second && (r.defIf4 != 0 || r.defIf6 != 0) {
		return r.defIf4, r.defIf6
	}
	r.ifAt = time.Now()
	if v := find("default"); v != 0 {
		r.defIf4 = v
	}
	if v := find("-inet6", "default"); v != 0 {
		r.defIf6 = v
	}
	return r.defIf4, r.defIf6
}

func netInterfaceIndex(name string) (uint32, error) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return 0, err
	}
	return uint32(ifc.Index), nil
}

func (r *darwinRouter) ApplyTUN(cfg client.TUNConfig) (tun.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(cfg.Addresses) == 0 {
		r.teardownLocked()
		return nil, nil
	}
	var dev tun.Device
	if r.name == "" {
		d, err := tun.CreateTUN("utun", cfg.MTU)
		if err != nil {
			return nil, fmt.Errorf("create utun: %w", err)
		}
		name, _ := d.Name()
		r.name, dev = name, d
	}
	if err := r.setAddresses(cfg); err != nil {
		return dev, fmt.Errorf("addresses: %w", err)
	}
	if err := r.setRoutes(cfg); err != nil {
		return dev, fmt.Errorf("routes: %w", err)
	}
	if err := r.setDNS(cfg); err != nil {
		r.log.Warn("DNS configuration failed", "err", err)
	}
	if err := r.setPF(cfg); err != nil {
		r.log.Warn("packet filter (kill switch / forwarding)", "err", err)
	}
	return dev, nil
}

func (r *darwinRouter) setAddresses(cfg client.TUNConfig) error {
	_ = run("ifconfig", r.name, "mtu", fmt.Sprint(cfg.MTU), "up")
	for _, p := range r.addrs {
		if !contains(cfg.Addresses, p) {
			if p.Addr().Is4() {
				_ = run("ifconfig", r.name, "inet", p.Addr().String(), "-alias")
			} else {
				_ = run("ifconfig", r.name, "inet6", p.Addr().String(), "-alias")
			}
		}
	}
	for _, p := range cfg.Addresses {
		var err error
		if p.Addr().Is4() {
			// A point-to-point utun needs a destination; use our own address, then the
			// network route below carries the traffic.
			err = run("ifconfig", r.name, "inet", p.Addr().String(), p.Addr().String(), "alias")
		} else {
			err = run("ifconfig", r.name, "inet6", p.Addr().String(), "prefixlen", "128", "alias")
		}
		if err != nil {
			return err
		}
	}
	r.addrs = append([]netip.Prefix(nil), cfg.Addresses...)
	return nil
}

func (r *darwinRouter) setRoutes(cfg client.TUNConfig) error {
	want := splitDefault(cfg.Routes)
	add, del := diffPrefixes(r.routes, want)
	for _, p := range del {
		_ = routeCmd("delete", p, r.name)
	}
	installed := map[netip.Prefix]bool{}
	for _, p := range r.routes {
		installed[p] = true
	}
	for _, p := range del {
		delete(installed, p)
	}
	var errs []error
	for _, p := range add {
		if err := routeCmd("add", p, r.name); err != nil {
			// Usually "File exists": the Mac is inside that network right now (at home),
			// and the local route rightly wins. It is retried after the next network change.
			if !strings.Contains(err.Error(), "exists") {
				errs = append(errs, err)
			}
			continue
		}
		installed[p] = true
	}
	r.routes = r.routes[:0]
	for _, p := range want {
		if installed[p] {
			r.routes = append(r.routes, p)
		}
	}
	return errors.Join(errs...)
}

func routeCmd(verb string, p netip.Prefix, iface string) error {
	args := []string{"-n", verb}
	if p.Addr().Is6() {
		args = append(args, "-inet6")
	}
	if p.IsSingleIP() {
		args = append(args, "-host", p.Addr().String())
	} else {
		args = append(args, "-net", p.String())
	}
	return run("route", append(args, "-interface", iface)...)
}

// ---------- DNS ----------

func (r *darwinRouter) setDNS(cfg client.TUNConfig) error {
	key := fmt.Sprint(cfg.DNS, cleanDomains(cfg.MatchDomains), cfg.OverrideDNS)
	if key == r.dnsKey {
		return nil // unchanged: skip rewriting files and running networksetup
	}
	r.dnsKey = key
	r.clearResolvers()
	if len(cfg.DNS) == 0 {
		return r.restoreServiceDNS()
	}
	if err := os.MkdirAll(resolverDir, 0o755); err != nil {
		return err
	}
	var body strings.Builder
	body.WriteString(resolverMark + "\n")
	for _, a := range cfg.DNS {
		fmt.Fprintf(&body, "nameserver %s\n", a)
	}
	// cleanDomains rejects anything with a slash or "..": these names become file paths.
	for _, d := range cleanDomains(cfg.MatchDomains) {
		path := filepath.Join(resolverDir, d)
		if existing, err := os.ReadFile(path); err == nil && !strings.HasPrefix(string(existing), resolverMark) {
			r.log.Warn("not replacing a resolver file that Gorget didn't create", "path", path)
			continue
		}
		if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
			return err
		}
		r.resolver = append(r.resolver, path)
	}
	if cfg.OverrideDNS {
		return r.overrideServiceDNS(cfg.DNS)
	}
	return r.restoreServiceDNS()
}

func (r *darwinRouter) clearResolvers() {
	for _, p := range r.resolver {
		if b, err := os.ReadFile(p); err == nil && strings.HasPrefix(string(b), resolverMark) {
			_ = os.Remove(p)
		}
	}
	r.resolver = nil
}

func networkServices() []string {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil
	}
	var svcs []string
	for i, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// The first line is a notice; disabled services start with "*".
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		svcs = append(svcs, line)
	}
	return svcs
}

func (r *darwinRouter) overrideServiceDNS(servers []netip.Addr) error {
	args := make([]string, 0, len(servers))
	for _, s := range servers {
		args = append(args, s.String())
	}
	var errs []error
	for _, svc := range networkServices() {
		if _, saved := r.savedDNS[svc]; !saved {
			out, err := exec.Command("networksetup", "-getdnsservers", svc).Output()
			if err != nil {
				continue
			}
			cur := strings.Join(strings.Fields(string(out)), " ")
			if strings.Contains(cur, "aren't any DNS Servers") {
				cur = "Empty"
			}
			r.savedDNS[svc] = cur
			if b, err := json.Marshal(r.savedDNS); err == nil {
				_ = os.MkdirAll(filepath.Dir(savedDNSFile), 0o700)
				_ = os.WriteFile(savedDNSFile, b, 0o600)
			}
		}
		if err := run("networksetup", append([]string{"-setdnsservers", svc}, args...)...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *darwinRouter) restoreServiceDNS() error {
	for svc, orig := range r.savedDNS {
		args := []string{"-setdnsservers", svc}
		if orig == "" {
			orig = "Empty"
		}
		args = append(args, strings.Fields(orig)...)
		_ = run("networksetup", args...)
		delete(r.savedDNS, svc)
	}
	_ = os.Remove(savedDNSFile)
	return nil
}

// ---------- kill switch & forwarding (pf) ----------

func (r *darwinRouter) setPF(cfg client.TUNConfig) error {
	killSwitch := cfg.FullTunnel && cfg.KillSwitch
	if !killSwitch && !cfg.Forward {
		return r.pfDisable()
	}
	var sb strings.Builder
	if cfg.Forward {
		if !r.fwdOn {
			if out, err := exec.Command("sysctl", "-n", "net.inet.ip.forwarding").Output(); err == nil {
				r.fwdWas = strings.TrimSpace(string(out))
			}
		}
		_ = run("sysctl", "-w", "net.inet.ip.forwarding=1")
		r.fwdOn = true
		var src []string
		for _, p := range cfg.Overlay {
			if p.Addr().Is4() {
				src = append(src, p.String())
			}
		}
		if len(src) > 0 {
			fmt.Fprintf(&sb, "nat on ! %s inet from { %s } to any -> (egress)\n", r.name, strings.Join(src, ", "))
		}
	} else {
		r.restoreForwarding()
	}
	rules := sb.String()
	if killSwitch {
		rules += "block out all\n"
		rules += fmt.Sprintf("pass out quick on %s all\n", r.name)
		rules += "pass out quick on lo0 all\n"
		rules += "pass out quick proto udp from any port { 67, 68 } to any port { 67, 68 }\n"
		if cfg.AllowLAN {
			rules += "pass out quick to { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16 }\n"
			rules += "pass out quick inet6 to { fe80::/10, fc00::/7 }\n"
		}
		rules += "pass out quick inet6 proto icmp6 all\n"
		// Traffic of the daemon itself (control, relay, peer UDP) is bound to the physical
		// interface; let it out by user id (root).
		rules += "pass out quick user 0\n"
	}
	if r.pfOn && rules == r.pfRules {
		return nil // unchanged
	}
	if err := os.WriteFile(pfRulesFile, []byte(rules), 0o600); err != nil {
		return err
	}
	if err := run("pfctl", "-a", pfAnchor, "-f", pfRulesFile); err != nil {
		return err
	}
	r.pfRules = rules
	if !r.pfOn {
		out, _ := exec.Command("pfctl", "-s", "info").CombinedOutput()
		r.pfWasOn = strings.Contains(string(out), "Status: Enabled")
		// The anchor only runs if the main ruleset references it; load a ruleset that does.
		anchorRef := fmt.Sprintf("nat-anchor \"%s\"\nanchor \"%s\"\n", pfAnchor, pfAnchor)
		if err := pfLoadMain(anchorRef); err != nil {
			return err
		}
		if !r.pfWasOn {
			_ = run("pfctl", "-e")
		}
		r.pfOn = true
	}
	return nil
}

// pfLoadMain appends our anchor reference to the system ruleset (/etc/pf.conf) and loads it.
func pfLoadMain(anchorRef string) error {
	base, _ := os.ReadFile("/etc/pf.conf")
	if strings.Contains(string(base), anchorRef) {
		return run("pfctl", "-f", "/etc/pf.conf")
	}
	tmp := "/var/run/gorget-pf-main.conf"
	if err := os.WriteFile(tmp, append(base, []byte("\n"+anchorRef)...), 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return run("pfctl", "-f", tmp)
}

func (r *darwinRouter) pfDisable() error {
	r.restoreForwarding()
	if !r.pfOn {
		return nil
	}
	_ = run("pfctl", "-a", pfAnchor, "-F", "all")
	_ = run("pfctl", "-f", "/etc/pf.conf") // back to the system ruleset without our anchor
	if !r.pfWasOn {
		_ = run("pfctl", "-d")
	}
	_ = os.Remove(pfRulesFile)
	r.pfOn, r.pfRules = false, ""
	return nil
}

func (r *darwinRouter) restoreForwarding() {
	if !r.fwdOn {
		return
	}
	if r.fwdWas == "" {
		r.fwdWas = "0"
	}
	_ = run("sysctl", "-w", "net.inet.ip.forwarding="+r.fwdWas)
	r.fwdOn = false
}

// ---------- teardown ----------

func (r *darwinRouter) teardownLocked() {
	r.dnsKey = ""
	r.clearResolvers()
	_ = r.restoreServiceDNS()
	_ = r.pfDisable()
	for _, p := range r.routes {
		_ = routeCmd("delete", p, r.name)
	}
	r.routes, r.addrs = nil, nil
	// The utun is closed by the engine; forget it so the next connect creates a new one.
	r.name = ""
}

func (r *darwinRouter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.teardownLocked()
	return nil
}

// splitDefault replaces 0.0.0.0/0 and ::/0 with two halves each, so the
// tunnel wins over the existing default route without deleting it.
func splitDefault(routes []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, r := range routes {
		switch {
		case r.Bits() == 0 && r.Addr().Is4():
			out = append(out, netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1"))
		case r.Bits() == 0:
			out = append(out, netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1"))
		default:
			out = append(out, r)
		}
	}
	return out
}

// ---------- helpers ----------

func contains(list []netip.Prefix, p netip.Prefix) bool {
	for _, q := range list {
		if q == p {
			return true
		}
	}
	return false
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
