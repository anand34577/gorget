package core

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"github.com/anand34577/gorget/internal/store"
)

// A Gorget app must get a route for the networks behind a standard WireGuard router (an
// OpenWrt box), through the gateway. Otherwise the router's overlay address answers but the
// LAN behind it can't be reached with the one app.
func TestClientsGetRoutesForNetworksBehindWireGuardRouters(t *testing.T) {
	c := newTestCore(t)
	ctx := context.Background()
	now := store.Now()
	native := &store.Device{ID: "n1", Name: "laptop", Kind: store.KindNative, WGPublicKey: "bmF0aXZlLWtleS1uYXRpdmUta2V5LW5hdGl2ZS1rZXk=", IPv4: "100.80.0.2", IPv6: c.Plan().IPv6For(mustAddr(t, "100.80.0.2")).String(),
		State: store.StateActive, KeyExpiryDisabled: true, Tags: store.StringList{}, Endpoints: store.StringList{}, CustomAllowedIPs: store.StringList{}, CreatedAt: now, UpdatedAt: now}
	router := &store.Device{ID: "r1", Name: "openwrt", Kind: store.KindWireGuard, WGPublicKey: "cm91dGVyLWtleS1yb3V0ZXIta2V5LXJvdXRlci1rZXkta2V5IQ==", IPv4: "100.80.0.3", IPv6: c.Plan().IPv6For(mustAddr(t, "100.80.0.3")).String(),
		State: store.StateActive, KeyExpiryDisabled: true, TunnelMode: store.TunnelSplit, Tags: store.StringList{}, Endpoints: store.StringList{}, CustomAllowedIPs: store.StringList{}, CreatedAt: now, UpdatedAt: now}
	for _, d := range []*store.Device{native, router} {
		if err := c.Store.CreateDevice(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.AddSiteRoute(ctx, SystemActor, "r1", "192.168.1.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := c.Coord.rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	nm := c.NetworkMap(c.Coord.Snapshot(), "n1")
	if nm == nil {
		t.Fatal("no network map for the laptop")
	}
	var gw bool
	for _, p := range nm.Peers {
		if !p.IsGateway {
			continue
		}
		gw = true
		if !slices.Contains(p.AllowedIps, "192.168.1.0/24") || !slices.Contains(p.AllowedIps, "100.80.0.3/32") {
			t.Errorf("gateway peer must carry the router's address and LAN: %v", p.AllowedIps)
		}
		if !slices.Contains(p.SubnetRoutes, "192.168.1.0/24") {
			t.Errorf("the LAN behind the router must be offered as a route: %v", p.SubnetRoutes)
		}
	}
	if !gw {
		t.Fatal("the laptop should reach the router through the gateway")
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
