package core

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

// gwEndpoints resolves the gateway's public endpoint ("vpn.example.com:51820") to
// addresses. Apps only accept ip:port endpoints, and the gateway has no other way to be
// found, so a hostname here would leave every app without a path to it.
type gwEndpoints struct {
	mu      sync.Mutex
	host    string
	port    string
	addrs   []string
	fetched time.Time
	busy    bool
}

const gwEndpointTTL = time.Minute

// GatewayEndpoints returns the gateway endpoint as ip:port strings (IPv4 first). A
// refresh runs in the background, so building a network map never waits for DNS.
func (c *Core) GatewayEndpoints() []string {
	ep := c.Cfg.Gateway.Endpoint
	if ep == "" {
		return nil
	}
	if ap, err := netip.ParseAddrPort(ep); err == nil {
		return []string{ap.String()}
	}
	host, port, err := net.SplitHostPort(ep)
	if err != nil {
		return nil
	}
	g := &c.gwEP
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.host != host || g.port != port {
		g.host, g.port, g.addrs, g.fetched = host, port, nil, time.Time{}
	}
	if time.Since(g.fetched) > gwEndpointTTL && !g.busy {
		g.busy = true
		go c.refreshGatewayEndpoints(host, port)
		if len(g.addrs) == 0 {
			// First use: give the lookup a moment so the very first map already has it.
			g.mu.Unlock()
			time.Sleep(1500 * time.Millisecond)
			g.mu.Lock()
		}
	}
	return append([]string(nil), g.addrs...)
}

func (c *Core) refreshGatewayEndpoints(host, port string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	var v4, v6 []string
	for _, ip := range ips {
		ip = ip.Unmap()
		if ip.Is4() {
			v4 = append(v4, net.JoinHostPort(ip.String(), port))
		} else {
			v6 = append(v6, net.JoinHostPort(ip.String(), port))
		}
	}
	g := &c.gwEP
	g.mu.Lock()
	defer g.mu.Unlock()
	g.busy = false
	g.fetched = time.Now()
	if err != nil || len(ips) == 0 {
		if err != nil {
			c.Log.Warn("cannot resolve the gateway endpoint; apps can't reach the gateway until it resolves", "endpoint", host, "err", err)
		}
		return // keep the previous addresses
	}
	old := g.addrs
	g.addrs = append(v4, v6...)
	if len(old) != len(g.addrs) || (len(old) > 0 && old[0] != g.addrs[0]) {
		c.Coord.Trigger() // the new address reaches every device
	}
}
