package core

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

type policyNodeT = policy.Node

func parseWGKey(k string) (wgtypes.Key, error) { return wgtypes.ParseKey(strings.TrimSpace(k)) }

// WGConfigParams describes a new standard WireGuard client config.
type WGConfigParams struct {
	Name   string
	UserID string
	// PublicKey generated in the browser. If empty, the server generates a key pair
	// and returns the private key once (never stored).
	PublicKey        string
	TunnelMode       string
	CustomAllowedIPs []string
	ExpiresAt        int64
	PresharedKey     bool
	DNS              bool
	Tags             []string
	IPv4             string
}

// WGConfigResult is returned once after creation.
type WGConfigResult struct {
	Device     *store.Device `json:"device"`
	PrivateKey string        `json:"private_key,omitempty"`
	Config     string        `json:"config"`
}

func (c *Core) CreateWGConfig(ctx context.Context, a Actor, p WGConfigParams) (*WGConfigResult, error) {
	if !c.Cfg.Gateway.Enabled {
		return nil, invalid("the WireGuard gateway is disabled in the server configuration")
	}
	gs := c.Settings().Gateway
	if p.TunnelMode == "" {
		p.TunnelMode = gs.DefaultTunnelMode
	}
	if err := validTunnelMode(p.TunnelMode); err != nil {
		return nil, err
	}
	custom, err := parseCIDRList(p.CustomAllowedIPs)
	if err != nil {
		return nil, err
	}
	if p.TunnelMode == store.TunnelCustom && len(custom) == 0 {
		return nil, invalid("custom mode needs at least one allowed IP range")
	}
	var priv string
	if p.PublicKey == "" {
		k, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return nil, err
		}
		priv, p.PublicKey = k.String(), k.PublicKey().String()
	} else if !validWGKey(p.PublicKey) {
		return nil, invalid("invalid WireGuard public key")
	}
	for _, t := range p.Tags {
		if !strings.HasPrefix(t, "tag:") {
			return nil, invalid("tag %q must look like tag:<name>", t)
		}
	}
	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, d := range devs {
		taken[d.Name] = true
		if d.WGPublicKey == p.PublicKey {
			return nil, invalid("this public key is already in use")
		}
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, invalid("name is required")
	}
	now := store.Now()
	d := &store.Device{
		ID:                secrets.RandomID(),
		Name:              uniqueName(p.Name, taken),
		Kind:              store.KindWireGuard,
		UserID:            nullString(p.UserID),
		WGPublicKey:       p.PublicKey,
		Tags:              append(store.StringList{}, p.Tags...),
		State:             store.StateActive,
		KeyExpiryDisabled: true,
		OS:                "wireguard",
		Endpoints:         store.StringList{},
		TunnelMode:        p.TunnelMode,
		CustomAllowedIPs:  custom,
		DNSEnabled:        p.DNS,
		ExpiresAt:         p.ExpiresAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if p.ExpiresAt == 0 && gs.DefaultExpiryDays > 0 {
		d.ExpiresAt = now + int64(gs.DefaultExpiryDays)*86400
	}
	if p.PresharedKey {
		psk, err := wgtypes.GenerateKey()
		if err != nil {
			return nil, err
		}
		if d.PSK, err = c.Box.Seal(psk.String()); err != nil {
			return nil, err
		}
	}
	if p.IPv4 != "" {
		a4, err := netip.ParseAddr(p.IPv4)
		if err != nil {
			return nil, invalid("invalid IPv4 address")
		}
		if err := c.Plan().Check(a4, usedAddrs(devs)); err != nil {
			return nil, invalid("%v", err)
		}
		d.IPv4, d.IPv6, d.StaticIP = a4.String(), c.Plan().IPv6For(a4).String(), true
	} else if err := c.allocate(ctx, d, devs, c.userGroupNames(p.UserID)); err != nil {
		return nil, err
	}
	if err := c.Store.CreateDevice(ctx, d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, invalid("conflicts with an existing device")
		}
		return nil, err
	}
	c.Audit(ctx, a, "wgconfig.create", "device", d.ID, d.Name, map[string]any{"tunnel_mode": d.TunnelMode, "ipv4": d.IPv4, "server_generated_key": priv != ""})
	c.Bus.Publish(EvDeviceCreated, deviceEvent(d))
	c.Coord.Trigger()
	conf, err := c.RenderWGConfig(d, priv)
	if err != nil {
		return nil, err
	}
	return &WGConfigResult{Device: d, PrivateKey: priv, Config: conf}, nil
}

// RenderWGConfig produces a wg-quick compatible configuration. When priv is
// empty a placeholder is written that the user replaces with their private key.
func (c *Core) RenderWGConfig(d *store.Device, priv string) (string, error) {
	if d.Kind != store.KindWireGuard {
		return "", invalid("not a WireGuard config device")
	}
	s := c.Coord.Snapshot()
	settings := s.Settings
	plan := s.Plan
	var b strings.Builder
	fmt.Fprintf(&b, "# Gorget WireGuard configuration for %s\n", d.Name)
	fmt.Fprintf(&b, "# Network: %s (%s)\n", settings.Network.Name, c.Cfg.PublicURL)
	b.WriteString("[Interface]\n")
	if priv == "" {
		b.WriteString("PrivateKey = <REPLACE_WITH_YOUR_PRIVATE_KEY>\n")
	} else {
		fmt.Fprintf(&b, "PrivateKey = %s\n", priv)
	}
	addrs := []string{d.IPv4 + "/32"}
	if settings.Network.IPv6On {
		addrs = append(addrs, d.IPv6+"/128")
	}
	fmt.Fprintf(&b, "Address = %s\n", strings.Join(addrs, ", "))
	if d.DNSEnabled && c.Cfg.Gateway.DNS {
		dns := []string{plan.GatewayIPv4().String()}
		if settings.Network.IPv6On {
			dns = append(dns, plan.GatewayIPv6().String())
		}
		if settings.DNS.MagicDNS {
			dns = append(dns, settings.Network.Domain)
		}
		fmt.Fprintf(&b, "DNS = %s\n", strings.Join(dns, ", "))
	}
	if c.Cfg.Gateway.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", c.Cfg.Gateway.MTU)
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "# Gorget gateway\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", c.GatewayPublicKey().String())
	if d.PSK != "" {
		psk, err := c.Box.Open(d.PSK)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "PresharedKey = %s\n", psk)
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(c.wgAllowedIPs(s, d), ", "))
	fmt.Fprintf(&b, "Endpoint = %s\n", c.Cfg.Gateway.Endpoint)
	if ka := settings.Gateway.PersistentKeepalive; ka > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", ka)
	}
	return b.String(), nil
}

func (c *Core) wgAllowedIPs(s *Snapshot, d *store.Device) []string {
	switch d.TunnelMode {
	case store.TunnelFull:
		if s.Settings.Network.IPv6On {
			return []string{"0.0.0.0/0", "::/0"}
		}
		return []string{"0.0.0.0/0"}
	case store.TunnelCustom:
		return d.CustomAllowedIPs
	}
	out := []string{s.Plan.IPv4.String()}
	if s.Settings.Network.IPv6On {
		out = append(out, s.Plan.IPv6.String())
	}
	// Include subnet routes this client is allowed to reach.
	if s.Active[d.ID] {
		rules := s.Compiled.OutboundRules(d.ID)
		for p, via := range s.PrimaryRoute {
			if via == d.ID {
				continue // this device's own local network stays local
			}
			for _, r := range rules {
				if overlapsAny(r.Dst, p) && !slices.Contains(out, p.String()) {
					out = append(out, p.String())
					break
				}
			}
		}
	}
	for _, r := range s.Settings.Gateway.SplitRoutes {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	slices.Sort(out[1:])
	return out
}

func overlapsAny(ps []netip.Prefix, p netip.Prefix) bool {
	for _, x := range ps {
		if x.Bits() > 0 && x.Overlaps(p) {
			return true
		}
	}
	return false
}

// RotateWGPresharedKey replaces (or adds) the PSK of a standard config.
func (c *Core) RotateWGPresharedKey(ctx context.Context, a Actor, id string) (*store.Device, error) {
	d, err := c.Store.GetDevice(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.Kind != store.KindWireGuard {
		return nil, invalid("not a WireGuard config")
	}
	psk, err := wgtypes.GenerateKey()
	if err != nil {
		return nil, err
	}
	if d.PSK, err = c.Box.Seal(psk.String()); err != nil {
		return nil, err
	}
	if err := c.Store.UpdateDevice(ctx, d); err != nil {
		return nil, err
	}
	c.Audit(ctx, a, "wgconfig.rotate_psk", "device", d.ID, d.Name, nil)
	c.Coord.Trigger()
	return d, nil
}
