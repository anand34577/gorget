//go:build linux

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

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/ipc"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/netfw"
)

type linuxDP struct {
	cfg     config.GatewayConfig
	log     *slog.Logger
	wg      *wgctrl.Client
	backend string
	// userspace
	dev    *device.Device
	uapi   net.Listener
	routes map[netip.Prefix]bool
}

func newDataplane(cfg config.GatewayConfig, log *slog.Logger) (dataplane, error) {
	if os.Geteuid() != 0 {
		// CAP_NET_ADMIN may still be granted; try anyway but warn.
		log.Warn("gateway is not running as root; it needs CAP_NET_ADMIN")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		return nil, errors.New("nftables (nft) is required for the gateway firewall; install the 'nftables' package")
	}
	wg, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("wgctrl: %w", err)
	}
	return &linuxDP{cfg: cfg, log: log, wg: wg, routes: map[netip.Prefix]bool{}}, nil
}

func (d *linuxDP) Backend() string { return d.backend }

func (d *linuxDP) Setup(addr4, addr6 netip.Prefix, mtu, port int, key wgtypes.Key) error {
	name := d.cfg.Interface
	link, err := netlink.LinkByName(name)
	if err == nil && d.backend == "" {
		// Stale interface from a previous run.
		_ = netlink.LinkDel(link)
		link = nil
	}
	if link == nil || err != nil {
		if err := d.create(name, mtu); err != nil {
			return err
		}
		if link, err = netlink.LinkByName(name); err != nil {
			return err
		}
	}
	if err := netlink.LinkSetMTU(link, mtu); err != nil {
		return fmt.Errorf("set MTU: %w", err)
	}
	// Replace addresses.
	addrs, _ := netlink.AddrList(link, netlink.FAMILY_ALL)
	for _, a := range addrs {
		_ = netlink.AddrDel(link, &a)
	}
	for _, p := range []netip.Prefix{addr4, addr6} {
		if !p.IsValid() {
			continue
		}
		ipn := prefixToIPNet(p)
		if err := netlink.AddrAdd(link, &netlink.Addr{IPNet: &ipn}); err != nil {
			return fmt.Errorf("add address %s: %w", p, err)
		}
	}
	lp := port
	if err := d.wg.ConfigureDevice(name, wgtypes.Config{PrivateKey: &key, ListenPort: &lp}); err != nil {
		return fmt.Errorf("configure %s: %w", name, err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("link up: %w", err)
	}
	if err := sysctl("net/ipv4/ip_forward", "1"); err != nil {
		d.log.Warn("enable IPv4 forwarding", "err", err)
	}
	if addr6.IsValid() {
		if err := sysctl("net/ipv6/conf/all/forwarding", "1"); err != nil {
			d.log.Warn("enable IPv6 forwarding", "err", err)
		}
	}
	d.routes = map[netip.Prefix]bool{}
	return nil
}

func (d *linuxDP) create(name string, mtu int) error {
	if !d.cfg.Userspace {
		la := netlink.NewLinkAttrs()
		la.Name = name
		la.MTU = mtu
		err := netlink.LinkAdd(&netlink.Wireguard{LinkAttrs: la})
		if err == nil {
			d.backend = "kernel"
			d.log.Info("gateway using kernel WireGuard", "interface", name)
			return nil
		}
		d.log.Info("kernel WireGuard unavailable, using wireguard-go", "err", err)
	}
	t, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return fmt.Errorf("create TUN %s: %w", name, err)
	}
	logger := device.NewLogger(device.LogLevelError, "gateway: ")
	dev := device.NewDevice(t, conn.NewDefaultBind(), logger)
	fileUAPI, err := ipc.UAPIOpen(name)
	if err != nil {
		dev.Close()
		return fmt.Errorf("UAPI: %w", err)
	}
	uapi, err := ipc.UAPIListen(name, fileUAPI)
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
	d.dev, d.uapi, d.backend = dev, uapi, "userspace"
	d.log.Info("gateway using wireguard-go (userspace)", "interface", name)
	return nil
}

func (d *linuxDP) ConfigurePeers(peers []wgtypes.PeerConfig) error {
	if len(peers) == 0 {
		return nil
	}
	return d.wg.ConfigureDevice(d.cfg.Interface, wgtypes.Config{Peers: peers})
}

func (d *linuxDP) SetRoutes(routes []netip.Prefix) error {
	link, err := netlink.LinkByName(d.cfg.Interface)
	if err != nil {
		return err
	}
	want := map[netip.Prefix]bool{}
	var errs []error
	for _, p := range routes {
		p = p.Masked()
		want[p] = true
		if d.routes[p] {
			continue
		}
		ipn := prefixToIPNet(p)
		if err := netlink.RouteReplace(&netlink.Route{LinkIndex: link.Attrs().Index, Dst: &ipn, Scope: netlink.SCOPE_LINK}); err != nil {
			errs = append(errs, fmt.Errorf("route %s: %w", p, err))
			continue
		}
		d.routes[p] = true
	}
	for p := range d.routes {
		if !want[p] {
			ipn := prefixToIPNet(p)
			_ = netlink.RouteDel(&netlink.Route{LinkIndex: link.Attrs().Index, Dst: &ipn})
			delete(d.routes, p)
		}
	}
	return errors.Join(errs...)
}

func (d *linuxDP) ApplyFirewall(ruleset string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft: %v: %s", err, strings.TrimSpace(string(out)))
	}
	netfw.Allow(d.cfg.Interface)
	return nil
}

func (d *linuxDP) Peers() ([]wgtypes.Peer, error) {
	dev, err := d.wg.Device(d.cfg.Interface)
	if err != nil {
		return nil, err
	}
	return dev.Peers, nil
}

func (d *linuxDP) Close() error {
	var errs []error
	netfw.Revoke(d.cfg.Interface)
	if out, err := exec.Command("nft", "delete", "table", "inet", "gorget").CombinedOutput(); err != nil && !strings.Contains(string(out), "No such file") {
		errs = append(errs, fmt.Errorf("remove firewall: %s", strings.TrimSpace(string(out))))
	}
	if d.dev != nil {
		d.uapi.Close()
		d.dev.Close()
	} else if link, err := netlink.LinkByName(d.cfg.Interface); err == nil {
		errs = append(errs, netlink.LinkDel(link))
	}
	errs = append(errs, d.wg.Close())
	return errors.Join(errs...)
}

func sysctl(key, val string) error {
	return os.WriteFile("/proc/sys/"+key, []byte(val), 0o644)
}

var _ = unix.IFNAMSIZ
