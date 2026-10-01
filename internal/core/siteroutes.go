package core

import (
	"context"
	"net/netip"
	"strings"

	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// AddSiteRoute lets a standard WireGuard device (typically a router such as
// OpenWrt) carry a local network for the rest of the Gorget network. Gorget apps
// share their networks themselves (gorget set -advertise-routes); standard
// WireGuard devices can't, so an administrator adds the network here. The route is
// approved right away because an administrator created it.
func (c *Core) AddSiteRoute(ctx context.Context, a Actor, deviceID, cidr string) (*store.Route, error) {
	d, err := c.Store.GetDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if d.Kind != store.KindWireGuard {
		return nil, invalid("Gorget apps share networks themselves: on that device run gorget set -advertise-routes %s, then approve it here", strings.TrimSpace(cidr))
	}
	p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return nil, invalid("%q is not a network like 192.168.1.0/24", cidr)
	}
	p = p.Masked()
	switch {
	case p.Bits() == 0:
		return nil, invalid("use an exit node for all internet traffic; a site route must be a specific network")
	case p.Addr().IsLoopback(), p.Addr().IsMulticast(), p.Addr().IsLinkLocalUnicast():
		return nil, invalid("%s can't be routed", p)
	}
	plan := c.Plan()
	if p.Overlaps(plan.IPv4) || (plan.IPv6.IsValid() && p.Overlaps(plan.IPv6)) {
		return nil, invalid("%s overlaps the Gorget address range %s", p, plan.IPv4)
	}
	snap := c.Coord.Snapshot()
	for _, r := range snap.Routes[d.ID] {
		if q, err := netip.ParsePrefix(r.CIDR); err == nil && q.Masked() == p {
			return nil, invalid("%s already goes through %s", p, d.Name)
		}
	}
	rt := &store.Route{ID: secrets.RandomID(), DeviceID: d.ID, CIDR: p.String(), Advertised: true, Approved: true, Enabled: true, CreatedAt: store.Now()}
	if err := c.Store.CreateRoute(ctx, rt); err != nil {
		return nil, err
	}
	c.Audit(ctx, a, "route.create", "route", rt.ID, rt.CIDR, map[string]any{"device": d.Name, "site_router": true})
	c.Bus.Publish(EvRouteUpdated, map[string]any{"device": d.Name, "cidr": rt.CIDR})
	c.Coord.Trigger()
	return rt, nil
}
