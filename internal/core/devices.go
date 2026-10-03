package core

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/anand34577/gorget/internal/ipam"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// Errors returned to API layers.
var (
	ErrInvalid   = errors.New("invalid request")
	ErrForbidden = errors.New("forbidden")
)

type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

func invalid(f string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(f, a...)} }

// SanitizeName converts a hostname into a DNS label.
func SanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 50 {
		out = strings.Trim(out[:50], "-")
	}
	if out == "" {
		out = "device"
	}
	return out
}

func uniqueName(base string, taken map[string]bool) string {
	base = SanitizeName(base)
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		n := base + "-" + strconv.Itoa(i)
		if !taken[n] {
			return n
		}
	}
}

// RegisterParams describes a native device registration.
type RegisterParams struct {
	MachineKey string
	WGKey      string
	DiscoKey   string
	Name       string
	Hostname   string
	OS         string
	OSVersion  string
	Version    string
	Arch       string
	// Posture as reported by the client: 0 unknown, 1 yes, 2 no.
	DiskEncrypted int
	FirewallOn    int
	// PublicIP is the address the server sees the device connect from.
	PublicIP  string
	Ephemeral bool
	// Exactly one of SetupKey / User.
	SetupKey *store.SetupKey
	User     *store.User
}

// RegisterNative creates or re-registers a native device.
func (c *Core) RegisterNative(ctx context.Context, p RegisterParams) (*store.Device, error) {
	if p.SetupKey == nil && p.User == nil {
		return nil, invalid("a setup key or user login is required")
	}
	if !validWGKey(p.WGKey) {
		return nil, invalid("invalid WireGuard public key")
	}
	if p.DiscoKey != "" && !validWGKey(p.DiscoKey) {
		return nil, invalid("invalid disco public key")
	}
	settings := c.Settings()
	now := store.Now()

	existing, err := c.Store.GetDeviceByMachineKey(ctx, p.MachineKey)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err == nil {
		// Re-registration of a known machine (e.g. after key expiry or logout).
		d := existing
		d.WGPublicKey = p.WGKey
		d.DiscoKey = p.DiscoKey
		applyHost(d, p)
		if p.User != nil {
			d.UserID = nullString(p.User.ID)
		}
		if p.SetupKey != nil {
			d.Tags = mergeTags(d.Tags, p.SetupKey.Tags)
			d.SetupKeyID = p.SetupKey.ID
		}
		d.KeyExpiresAt = c.keyExpiry(d, now)
		if d.State == store.StatePending && p.SetupKey != nil && p.SetupKey.AutoApprove {
			d.State = store.StateActive
		}
		if err := c.Store.UpdateDevice(ctx, d); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return nil, invalid("this WireGuard key is already used by another device")
			}
			return nil, err
		}
		c.Bus.Publish(EvDeviceUpdated, deviceEvent(d))
		c.Coord.Trigger()
		return d, nil
	}

	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, d := range devs {
		taken[d.Name] = true
		if d.WGPublicKey == p.WGKey {
			return nil, invalid("this WireGuard key is already used by another device")
		}
	}
	name := p.Name
	if name == "" {
		name = p.Hostname
	}
	d := &store.Device{
		ID:               secrets.RandomID(),
		Name:             uniqueName(name, taken),
		Kind:             store.KindNative,
		MachineKey:       nullString(p.MachineKey),
		WGPublicKey:      p.WGKey,
		DiscoKey:         p.DiscoKey,
		Tags:             store.StringList{},
		State:            store.StateActive,
		Ephemeral:        p.Ephemeral,
		Endpoints:        store.StringList{},
		CustomAllowedIPs: store.StringList{},
		DNSEnabled:       true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	applyHost(d, p)
	var ownerGroups []string
	if p.User != nil {
		d.UserID = nullString(p.User.ID)
		ownerGroups = c.userGroupNames(p.User.ID)
		if settings.Devices.ApprovalRequired && !isAdminRole(p.User.Role) {
			d.State = store.StatePending
		}
	}
	if k := p.SetupKey; k != nil {
		d.Tags = append(store.StringList{}, k.Tags...)
		d.SetupKeyID = k.ID
		d.Ephemeral = d.Ephemeral || k.Ephemeral
		if k.CreatedBy != "" && len(k.Tags) == 0 {
			// Untagged setup keys created by a user register devices owned by that user.
			if u, err := c.Store.GetUser(ctx, k.CreatedBy); err == nil {
				d.UserID = nullString(u.ID)
				ownerGroups = c.userGroupNames(u.ID)
			}
		}
		if !k.AutoApprove && settings.Devices.ApprovalRequired {
			d.State = store.StatePending
		}
	}
	// Tagged devices (servers) do not expire by default.
	d.KeyExpiryDisabled = len(d.Tags) > 0
	d.KeyExpiresAt = c.keyExpiry(d, now)

	if err := c.allocate(ctx, d, devs, ownerGroups); err != nil {
		return nil, err
	}
	if err := c.Store.CreateDevice(ctx, d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, invalid("device conflicts with an existing device (name, key or address)")
		}
		return nil, err
	}
	c.Bus.Publish(EvDeviceCreated, deviceEvent(d))
	if d.State == store.StatePending {
		c.Bus.Publish(EvDevicePending, deviceEvent(d))
	}
	c.Coord.Trigger()
	return d, nil
}

func applyHost(d *store.Device, p RegisterParams) {
	if p.Hostname != "" {
		d.Hostname = p.Hostname
	}
	if p.OS != "" {
		d.OS = p.OS
	}
	d.OSVersion = p.OSVersion
	d.ClientVersion = p.Version
	d.Arch = p.Arch
	d.DiskEncrypted = p.DiskEncrypted
	d.FirewallOn = p.FirewallOn
	if p.PublicIP != "" {
		d.PublicIP = p.PublicIP
	}
}

// HostChanged reports whether the reported host details differ from what is stored.
func HostChanged(d *store.Device, p RegisterParams) bool {
	return p.OSVersion != d.OSVersion || p.Version != d.ClientVersion || p.Hostname != d.Hostname ||
		p.DiskEncrypted != d.DiskEncrypted || p.FirewallOn != d.FirewallOn || (p.PublicIP != "" && p.PublicIP != d.PublicIP)
}

func mergeTags(a, b []string) store.StringList {
	out := append(store.StringList{}, a...)
	for _, t := range b {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

func (c *Core) keyExpiry(d *store.Device, now int64) int64 {
	days := c.Settings().Devices.KeyExpiryDays
	if d.KeyExpiryDisabled || days <= 0 {
		return 0
	}
	return now + int64(days)*86400
}

func isAdminRole(r string) bool {
	return r == store.RoleOwner || r == store.RoleAdmin || r == store.RoleNetworkAdmin
}

func (c *Core) userGroupNames(userID string) []string {
	s := c.Coord.Snapshot()
	if s == nil {
		return nil
	}
	var out []string
	for name, members := range s.Groups {
		if slices.Contains(members, userID) {
			out = append(out, name)
		}
	}
	return out
}

// allocate assigns IPv4/IPv6 addresses honouring tag/group pools.
func (c *Core) allocate(ctx context.Context, d *store.Device, devs []store.Device, groups []string) error {
	plan := c.Plan()
	pool := netip.Prefix{}
	pools := c.Settings().Network.Pools
	for _, t := range d.Tags {
		if cidr, ok := pools[t]; ok {
			pool, _ = netip.ParsePrefix(cidr)
			break
		}
	}
	if !pool.IsValid() {
		for _, g := range groups {
			if cidr, ok := pools["group:"+g]; ok {
				pool, _ = netip.ParsePrefix(cidr)
				break
			}
		}
	}
	v4, v6, err := plan.Allocate(usedAddrs(devs), pool)
	if errors.Is(err, ipam.ErrExhausted) && pool.IsValid() {
		c.Log.Warn("address pool exhausted, falling back to the whole network", "pool", pool)
		v4, v6, err = plan.Allocate(usedAddrs(devs), netip.Prefix{})
	}
	if err != nil {
		return fmt.Errorf("allocate address: %w", err)
	}
	d.IPv4, d.IPv6 = v4.String(), v6.String()
	return nil
}

func validWGKey(k string) bool {
	_, err := parseWGKey(k)
	return err == nil
}

func deviceEvent(d *store.Device) map[string]any {
	return map[string]any{"id": d.ID, "name": d.Name, "kind": d.Kind, "state": d.State, "ipv4": d.IPv4}
}

// UpdateStatus records a status report from a native client.
func (c *Core) UpdateStatus(ctx context.Context, d *store.Device, endpoints []string, homeRelay string, routes []string, advertiseExit bool, host *RegisterParams) error {
	changed := false
	eps := sanitizeEndpoints(endpoints)
	if !slices.Equal(eps, d.Endpoints) {
		d.Endpoints = eps
		changed = true
	}
	if homeRelay != d.HomeRelay {
		d.HomeRelay = homeRelay
		changed = true
	}
	if advertiseExit != d.ExitAdvertised {
		d.ExitAdvertised = advertiseExit
		changed = true
		if advertiseExit && !d.ExitApproved {
			s := c.Coord.Snapshot()
			if s.Policy.AutoApproveExitNode(s.Env, c.policyNode(s, d)) {
				d.ExitApproved = true
			}
			c.Bus.Publish(EvRouteAdvertised, map[string]any{"device": d.Name, "exit_node": true, "approved": d.ExitApproved})
		}
	}
	if host != nil && HostChanged(d, *host) {
		applyHost(d, *host)
		changed = true
	}
	var cidrs []string
	for _, r := range routes {
		p, err := netip.ParsePrefix(r)
		if err != nil || p.Bits() == 0 {
			continue
		}
		cidrs = append(cidrs, p.Masked().String())
	}
	if len(cidrs) > 32 {
		cidrs = cidrs[:32]
	}
	s := c.Coord.Snapshot()
	routesChanged, err := c.Store.SyncAdvertisedRoutes(ctx, d.ID, cidrs, func(cidr string) bool {
		p, _ := netip.ParsePrefix(cidr)
		return s.Policy.AutoApproveRoute(s.Env, c.policyNode(s, d), p)
	}, secrets.RandomID)
	if err != nil {
		return err
	}
	if routesChanged {
		c.Bus.Publish(EvRouteAdvertised, map[string]any{"device": d.Name, "routes": cidrs})
	}
	d.LastSeenAt = store.Now()
	if changed {
		if err := c.Store.UpdateDevice(ctx, d); err != nil {
			return err
		}
	} else if err := c.Store.SetLastSeen(ctx, d.ID, d.LastSeenAt); err != nil {
		return err
	}
	if changed || routesChanged {
		c.Coord.Trigger()
	}
	return nil
}

func sanitizeEndpoints(in []string) store.StringList {
	out := store.StringList{}
	for _, e := range in {
		if ap, err := netip.ParseAddrPort(e); err == nil && ap.Port() != 0 && !ap.Addr().IsUnspecified() && !ap.Addr().IsMulticast() {
			out = append(out, ap.String())
		}
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func (c *Core) policyNode(s *Snapshot, d *store.Device) policyNodeT {
	n := policyNodeT{ID: d.ID, Name: d.Name, Kind: d.Kind, Tags: d.Tags}
	if d.UserID.Valid {
		n.OwnerID = d.UserID.String
		if u := s.Users[n.OwnerID]; u != nil {
			n.OwnerEmail = u.Email
		}
	}
	n.Addrs = deviceAddrs(d, true)
	return n
}

// ---------- admin operations ----------

// DeviceUpdate holds optional admin changes to a device.
type DeviceUpdate struct {
	Name              *string
	Tags              *[]string
	IPv4              *string
	KeyExpiryDisabled *bool
	ExitApproved      *bool
	State             *string
	ExpiresAt         *int64
	TunnelMode        *string
	CustomAllowedIPs  *[]string
}

func (c *Core) UpdateDevice(ctx context.Context, a Actor, id string, u DeviceUpdate) (*store.Device, error) {
	d, err := c.Store.GetDevice(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.Kind == store.KindGateway {
		return nil, invalid("the built-in gateway is managed by the server configuration")
	}
	changes := map[string]any{}
	if u.Name != nil {
		n := SanitizeName(*u.Name)
		if n != *u.Name {
			return nil, invalid("name must be lowercase letters, digits and dashes (suggestion: %s)", n)
		}
		if n != d.Name {
			changes["name"] = map[string]string{"from": d.Name, "to": n}
			d.Name = n
		}
	}
	if u.Tags != nil {
		for _, t := range *u.Tags {
			if !strings.HasPrefix(t, "tag:") || SanitizeName(strings.TrimPrefix(t, "tag:")) != strings.TrimPrefix(t, "tag:") {
				return nil, invalid("tag %q must look like tag:<name> (lowercase, digits, dashes)", t)
			}
		}
		changes["tags"] = *u.Tags
		d.Tags = append(store.StringList{}, *u.Tags...)
	}
	if u.IPv4 != nil && *u.IPv4 != d.IPv4 {
		a4, err := netip.ParseAddr(*u.IPv4)
		if err != nil {
			return nil, invalid("invalid IPv4 address")
		}
		devs, err := c.Store.ListDevices(ctx)
		if err != nil {
			return nil, err
		}
		if err := c.Plan().Check(a4, usedAddrs(devs)); err != nil {
			return nil, invalid("%v", err)
		}
		changes["ipv4"] = map[string]string{"from": d.IPv4, "to": a4.String()}
		d.IPv4 = a4.String()
		d.IPv6 = c.Plan().IPv6For(a4).String()
		d.StaticIP = true
	}
	if u.KeyExpiryDisabled != nil {
		d.KeyExpiryDisabled = *u.KeyExpiryDisabled
		if d.KeyExpiryDisabled {
			d.KeyExpiresAt = 0
		} else if d.KeyExpiresAt == 0 {
			d.KeyExpiresAt = c.keyExpiry(d, store.Now())
		}
		changes["key_expiry_disabled"] = d.KeyExpiryDisabled
	}
	if u.ExitApproved != nil {
		d.ExitApproved = *u.ExitApproved
		changes["exit_approved"] = d.ExitApproved
	}
	if u.State != nil && *u.State != d.State {
		switch *u.State {
		case store.StateActive, store.StateDisabled:
		default:
			return nil, invalid("state must be active or disabled")
		}
		changes["state"] = map[string]string{"from": d.State, "to": *u.State}
		d.State = *u.State
		if d.State == store.StateDisabled {
			c.Coord.Notify(d.ID, Notice{Kind: "disabled", Message: "This device was disabled by an administrator."})
		}
	}
	if u.ExpiresAt != nil {
		d.ExpiresAt = *u.ExpiresAt
		changes["expires_at"] = d.ExpiresAt
	}
	if d.Kind == store.KindWireGuard {
		if u.TunnelMode != nil {
			if err := validTunnelMode(*u.TunnelMode); err != nil {
				return nil, err
			}
			d.TunnelMode = *u.TunnelMode
			changes["tunnel_mode"] = d.TunnelMode
		}
		if u.CustomAllowedIPs != nil {
			cidrs, err := parseCIDRList(*u.CustomAllowedIPs)
			if err != nil {
				return nil, err
			}
			d.CustomAllowedIPs = cidrs
		}
	}
	if err := c.Store.UpdateDevice(ctx, d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, invalid("name or address already in use")
		}
		return nil, err
	}
	c.Audit(ctx, a, "device.update", "device", d.ID, d.Name, changes)
	c.Bus.Publish(EvDeviceUpdated, deviceEvent(d))
	c.Coord.Trigger()
	return d, nil
}

func (c *Core) ApproveDevice(ctx context.Context, a Actor, id string) (*store.Device, error) {
	d, err := c.Store.GetDevice(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.State != store.StatePending {
		return nil, invalid("device is not pending approval")
	}
	d.State = store.StateActive
	if err := c.Store.UpdateDevice(ctx, d); err != nil {
		return nil, err
	}
	c.Audit(ctx, a, "device.approve", "device", d.ID, d.Name, nil)
	c.Bus.Publish(EvDeviceUpdated, deviceEvent(d))
	c.Coord.Trigger()
	return d, nil
}

// ExpireDeviceKey forces the device to re-authenticate.
func (c *Core) ExpireDeviceKey(ctx context.Context, a Actor, id string) error {
	d, err := c.Store.GetDevice(ctx, id)
	if err != nil {
		return err
	}
	if d.Kind != store.KindNative {
		return invalid("only native devices have expiring keys")
	}
	d.KeyExpiryDisabled = false
	d.KeyExpiresAt = store.Now()
	if err := c.Store.UpdateDevice(ctx, d); err != nil {
		return err
	}
	c.Coord.Notify(d.ID, Notice{Kind: "expired", Message: "Your device key was expired by an administrator. Please log in again."})
	c.Audit(ctx, a, "device.expire_key", "device", d.ID, d.Name, nil)
	c.Coord.Trigger()
	return nil
}

func (c *Core) DeleteDevice(ctx context.Context, a Actor, id string) error {
	d, err := c.Store.GetDevice(ctx, id)
	if err != nil {
		return err
	}
	if d.Kind == store.KindGateway {
		return invalid("the built-in gateway cannot be deleted; disable it in the server configuration")
	}
	c.Coord.Notify(d.ID, Notice{Kind: "logged_out", Message: "This device was removed from the network."})
	if err := c.Store.DeleteDevice(ctx, id); err != nil {
		return err
	}
	c.Audit(ctx, a, "device.delete", "device", d.ID, d.Name, map[string]any{"kind": d.Kind, "ipv4": d.IPv4})
	c.Bus.Publish(EvDeviceDeleted, map[string]string{"id": d.ID, "name": d.Name})
	c.Coord.Trigger()
	return nil
}

// ---------- re-addressing ----------

type ReaddressItem struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	OldIPv4  string `json:"old_ipv4"`
	NewIPv4  string `json:"new_ipv4"`
	OldIPv6  string `json:"old_ipv6"`
	NewIPv6  string `json:"new_ipv6"`
	Kind     string `json:"kind"`
}

// Readdress moves every device into a new address plan. With dryRun it only returns the mapping.
func (c *Core) Readdress(ctx context.Context, a Actor, newV4, newV6 string, dryRun bool) ([]ReaddressItem, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	s := c.Settings()
	ns := s.Network
	ns.IPv4, ns.IPv6 = newV4, newV6
	if ns.IPv6 == "" {
		ns.IPv6 = s.Network.IPv6
	}
	// Reserved ranges & pools of the old plan don't apply to the new one.
	ns.Reserved, ns.Pools = nil, map[string]string{}
	if err := ValidateNetwork(ns); err != nil {
		return nil, invalid("%v", err)
	}
	oldPlan, newPlan := planFrom(s.Network), planFrom(ns)
	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	if len(devs) > newPlan.Capacity() {
		return nil, invalid("the new range has room for %d devices but %d exist", newPlan.Capacity(), len(devs))
	}
	used := map[netip.Addr]bool{}
	items := make([]ReaddressItem, 0, len(devs))
	var pending []int
	for i, d := range devs {
		it := ReaddressItem{DeviceID: d.ID, Name: d.Name, OldIPv4: d.IPv4, OldIPv6: d.IPv6, Kind: d.Kind}
		if d.Kind == store.KindGateway {
			it.NewIPv4, it.NewIPv6 = newPlan.GatewayIPv4().String(), newPlan.GatewayIPv6().String()
		} else if old, err := netip.ParseAddr(d.IPv4); err == nil {
			if na, ok := oldPlan.Translate(old, newPlan); ok && na != newPlan.GatewayIPv4() && !used[na] {
				used[na] = true
				it.NewIPv4, it.NewIPv6 = na.String(), newPlan.IPv6For(na).String()
			} else {
				pending = append(pending, i)
			}
		}
		items = append(items, it)
	}
	for _, i := range pending {
		na, v6, err := newPlan.Allocate(used, netip.Prefix{})
		if err != nil {
			return nil, invalid("not enough addresses in the new range")
		}
		used[na] = true
		items[i].NewIPv4, items[i].NewIPv6 = na.String(), v6.String()
	}
	if dryRun {
		return items, nil
	}
	err = c.Store.Tx(ctx, func(tx *sqlx.Tx) error {
		// Two phases avoid transient unique-constraint collisions.
		for _, it := range items {
			if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE devices SET ipv4 = ?, ipv6 = ? WHERE id = ?`), "tmp4-"+it.DeviceID, "tmp6-"+it.DeviceID, it.DeviceID); err != nil {
				return err
			}
		}
		for _, it := range items {
			if _, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE devices SET ipv4 = ?, ipv6 = ?, updated_at = ? WHERE id = ?`), it.NewIPv4, it.NewIPv6, store.Now(), it.DeviceID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := c.Store.PutSetting(ctx, keyNetwork, ns); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.settings.Network = ns
	c.mu.Unlock()
	c.Audit(ctx, a, "network.readdress", "network", "", ns.Name, map[string]any{"from": s.Network.IPv4, "to": ns.IPv4, "devices": len(items)})
	c.Bus.Publish(EvSettingsUpdated, map[string]string{"section": "network"})
	c.Coord.Trigger()
	return items, nil
}

func validTunnelMode(m string) error {
	switch m {
	case store.TunnelFull, store.TunnelSplit, store.TunnelCustom:
		return nil
	}
	return invalid("tunnel mode must be full, split or custom")
}

func parseCIDRList(in []string) (store.StringList, error) {
	out := store.StringList{}
	for _, s := range in {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return nil, invalid("invalid CIDR %q", s)
		}
		out = append(out, p.Masked().String())
	}
	return out, nil
}

// DeviceOnline reports presence for API listings.
func (c *Core) DeviceOnline(d *store.Device) bool {
	if d.Kind == store.KindWireGuard {
		// Standard WireGuard peers are online while the gateway keeps seeing traffic from them.
		return c.wgOnline(d)
	}
	return c.Coord.IsOnline(d.ID)
}
