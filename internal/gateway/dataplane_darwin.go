//go:build darwin

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/core"
)

const pfAnchor = "gorget-gw"

// darwinDP is the macOS gateway: wireguard-go on a utun interface, routes through route(8)
// and the firewall in a pf anchor. Run the server as root.
type darwinDP struct {
	cfg    config.GatewayConfig
	log    *slog.Logger
	wg     *wgctrl.Client
	name   string // utunN
	dev    *device.Device
	uapi   net.Listener
	routes map[netip.Prefix]bool

	pfOn    bool
	pfWasOn bool
	fwdWas  string
}

func newDataplane(cfg config.GatewayConfig, log *slog.Logger) (dataplane, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("the gateway on macOS must run as root (it creates a network interface and loads packet filter rules)")
	}
	if _, err := exec.LookPath("pfctl"); err != nil {
		return nil, errors.New("pfctl was not found")
	}
	wg, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("wgctrl: %w", err)
	}
	return &darwinDP{cfg: cfg, log: log, wg: wg, routes: map[netip.Prefix]bool{}}, nil
}

func (d *darwinDP) Backend() string { return "userspace (macOS)" }

func (d *darwinDP) Setup(addr4, addr6 netip.Prefix, mtu, port int, key wgtypes.Key) error {
	if d.dev == nil {
		t, err := tun.CreateTUN("utun", mtu)
		if err != nil {
			return fmt.Errorf("create utun: %w", err)
		}
		name, err := t.Name()
		if err != nil {
			t.Close()
			return err
		}
		dev := device.NewDevice(t, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "gateway: "))
		f, err := ipc.UAPIOpen(name)
		if err != nil {
			dev.Close()
			return fmt.Errorf("UAPI: %w", err)
		}
		uapi, err := ipc.UAPIListen(name, f)
		if err != nil {
			dev.Close()
			return fmt.Errorf("UAPI listen: %w", err)
		}
		go func() {
			for {
				c, err := uapi.Accept()
				if err != nil {
					return
				}
				go dev.IpcHandle(c)
			}
		}()
		d.dev, d.uapi, d.name = dev, uapi, name
		d.log.Info("gateway using wireguard-go on macOS", "interface", name)
	}
	lp := port
	if err := d.wg.ConfigureDevice(d.name, wgtypes.Config{PrivateKey: &key, ListenPort: &lp}); err != nil {
		return fmt.Errorf("configure %s: %w", d.name, err)
	}
	if err := run("ifconfig", d.name, "mtu", fmt.Sprint(mtu), "up"); err != nil {
		return err
	}
	// utun is point-to-point: give it our address as both ends, and route the network through it.
	if addr4.IsValid() {
		if err := run("ifconfig", d.name, "inet", addr4.Addr().String(), addr4.Addr().String(), "alias"); err != nil {
			return err
		}
		if err := run("route", "-n", "add", "-net", addr4.Masked().String(), "-interface", d.name); err != nil && !strings.Contains(err.Error(), "exists") {
			return err
		}
	}
	if addr6.IsValid() {
		if err := run("ifconfig", d.name, "inet6", addr6.Addr().String(), "prefixlen", "128", "alias"); err != nil {
			return err
		}
		if err := run("route", "-n", "add", "-inet6", "-net", addr6.Masked().String(), "-interface", d.name); err != nil && !strings.Contains(err.Error(), "exists") {
			return err
		}
	}
	if out, err := exec.Command("sysctl", "-n", "net.inet.ip.forwarding").Output(); err == nil && d.fwdWas == "" {
		d.fwdWas = strings.TrimSpace(string(out))
	}
	if err := run("sysctl", "-w", "net.inet.ip.forwarding=1"); err != nil {
		d.log.Warn("enable IPv4 forwarding", "err", err)
	}
	if addr6.IsValid() {
		_ = run("sysctl", "-w", "net.inet6.ip6.forwarding=1")
	}
	d.routes = map[netip.Prefix]bool{}
	return nil
}

func (d *darwinDP) ConfigurePeers(peers []wgtypes.PeerConfig) error {
	if len(peers) == 0 {
		return nil
	}
	return d.wg.ConfigureDevice(d.name, wgtypes.Config{Peers: peers})
}

func (d *darwinDP) SetRoutes(routes []netip.Prefix) error {
	want := map[netip.Prefix]bool{}
	var errs []error
	for _, p := range routes {
		p = p.Masked()
		want[p] = true
		if d.routes[p] {
			continue
		}
		args := []string{"-n", "add"}
		if p.Addr().Is6() {
			args = append(args, "-inet6")
		}
		args = append(args, "-net", p.String(), "-interface", d.name)
		if err := run("route", args...); err != nil && !strings.Contains(err.Error(), "exists") {
			errs = append(errs, err)
			continue
		}
		d.routes[p] = true
	}
	for p := range d.routes {
		if !want[p] {
			args := []string{"-n", "delete"}
			if p.Addr().Is6() {
				args = append(args, "-inet6")
			}
			_ = run("route", append(args, "-net", p.String(), "-interface", d.name)...)
			delete(d.routes, p)
		}
	}
	return errors.Join(errs...)
}

// defaultInterface finds the interface that carries internet traffic (for NAT).
func defaultInterface() string {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "interface:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ApplyFirewall loads pf rules (rendered by RenderFirewall) into the gorget-gw anchor.
func (d *darwinDP) ApplyFirewall(ruleset string) error {
	if err := os.WriteFile("/var/run/gorget-gw-pf.conf", []byte(ruleset), 0o600); err != nil {
		return err
	}
	if err := run("pfctl", "-a", pfAnchor, "-f", "/var/run/gorget-gw-pf.conf"); err != nil {
		return err
	}
	if !d.pfOn {
		out, _ := exec.Command("pfctl", "-s", "info").CombinedOutput()
		d.pfWasOn = strings.Contains(string(out), "Status: Enabled")
		ref := fmt.Sprintf("nat-anchor \"%s\"\nanchor \"%s\"\n", pfAnchor, pfAnchor)
		base, _ := os.ReadFile("/etc/pf.conf")
		if !strings.Contains(string(base), ref) {
			tmp := "/var/run/gorget-gw-main.conf"
			if err := os.WriteFile(tmp, append(base, []byte("\n"+ref)...), 0o600); err != nil {
				return err
			}
			defer os.Remove(tmp)
			if err := run("pfctl", "-f", tmp); err != nil {
				return err
			}
		} else if err := run("pfctl", "-f", "/etc/pf.conf"); err != nil {
			return err
		}
		if !d.pfWasOn {
			_ = run("pfctl", "-e")
		}
		d.pfOn = true
	}
	return nil
}

// RenderFirewall renders the pf rules; the generic gateway code calls it instead of BuildRuleset.
func (d *darwinDP) RenderFirewall(iface, egress string, v *core.GatewayView) string {
	if egress == "" {
		egress = defaultInterface()
	}
	return BuildPFRuleset(d.name, egress, v)
}

func (d *darwinDP) Peers() ([]wgtypes.Peer, error) {
	dev, err := d.wg.Device(d.name)
	if err != nil {
		return nil, err
	}
	return dev.Peers, nil
}

func (d *darwinDP) Close() error {
	var errs []error
	if d.pfOn {
		_ = run("pfctl", "-a", pfAnchor, "-F", "all")
		_ = run("pfctl", "-f", "/etc/pf.conf")
		if !d.pfWasOn {
			_ = run("pfctl", "-d")
		}
		_ = os.Remove("/var/run/gorget-gw-pf.conf")
	}
	if d.fwdWas != "" {
		_ = run("sysctl", "-w", "net.inet.ip.forwarding="+d.fwdWas)
	}
	if d.dev != nil {
		d.uapi.Close()
		d.dev.Close()
	}
	errs = append(errs, d.wg.Close())
	return errors.Join(errs...)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
