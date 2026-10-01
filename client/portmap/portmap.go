// Package portmap asks the home router to forward a UDP port to this device, so peers
// can reach it directly even behind a NAT that would otherwise need hole punching or
// the relay. It speaks PCP (RFC 6887), NAT-PMP (RFC 6886) and UPnP IGD, in that order,
// and renews the mapping while it runs.
package portmap

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/jackpal/gateway"
)

// Lease is how long each mapping is requested for (renewed at half time).
const Lease = time.Hour

// Mapper keeps one UDP port mapped.
type Mapper struct {
	log *slog.Logger

	mu      sync.Mutex
	method  string // "pcp", "natpmp", "upnp" or "" when none works
	current netip.AddrPort
	upnp    *upnpClient
	kick    chan struct{}
}

func New(log *slog.Logger) *Mapper {
	if log == nil {
		log = slog.Default()
	}
	return &Mapper{log: log, kick: make(chan struct{}, 1)}
}

// Method reports how the current mapping was made ("" = none).
func (m *Mapper) Method() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.method
}

// NetworkChanged makes the mapper retry at once (new router, new address).
func (m *Mapper) NetworkChanged() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run maps localPort until ctx ends and calls onChange whenever the public address changes
// (a zero AddrPort means the mapping is gone). It removes the mapping when it stops.
func (m *Mapper) Run(ctx context.Context, localPort uint16, onChange func(netip.AddrPort)) {
	defer m.release(localPort)
	backoff := 30 * time.Second
	for ctx.Err() == nil {
		ext, err := m.acquire(ctx, localPort)
		wait := Lease / 2
		switch {
		case err != nil:
			m.log.Debug("no port mapping", "err", err)
			m.set(netip.AddrPort{}, "", onChange)
			wait = backoff
			if backoff < 30*time.Minute {
				backoff *= 2
			}
		default:
			backoff = 30 * time.Second
			m.set(ext, m.Method(), onChange)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-m.kick:
			backoff = 30 * time.Second
		}
	}
}

func (m *Mapper) set(ext netip.AddrPort, method string, onChange func(netip.AddrPort)) {
	m.mu.Lock()
	changed := m.current != ext
	m.current, m.method = ext, method
	m.mu.Unlock()
	if changed && onChange != nil {
		onChange(ext)
	}
}

// acquire tries the protocols, starting with the one that worked last time.
func (m *Mapper) acquire(ctx context.Context, port uint16) (netip.AddrPort, error) {
	gw, err := gateway.DiscoverGateway()
	if err != nil {
		return netip.AddrPort{}, err
	}
	gwAddr, ok := netip.AddrFromSlice(gw.To4())
	if !ok {
		return netip.AddrPort{}, errors.New("IPv6-only gateway")
	}
	local, err := localAddrToward(gwAddr)
	if err != nil {
		return netip.AddrPort{}, err
	}
	type attempt struct {
		name string
		f    func() (netip.AddrPort, error)
	}
	tries := []attempt{
		{"pcp", func() (netip.AddrPort, error) { return pcpMap(ctx, gwAddr, local, port, Lease) }},
		{"natpmp", func() (netip.AddrPort, error) { return natpmpMap(ctx, gwAddr, port, Lease) }},
		{"upnp", func() (netip.AddrPort, error) { return m.upnpMap(ctx, local, port) }},
	}
	// Try the method that worked before first.
	m.mu.Lock()
	prev := m.method
	m.mu.Unlock()
	for i, t := range tries {
		if t.name == prev && i > 0 {
			tries[0], tries[i] = tries[i], tries[0]
		}
	}
	var errs []error
	for _, t := range tries {
		ext, err := t.f()
		if err == nil && ext.IsValid() {
			m.mu.Lock()
			m.method = t.name
			m.mu.Unlock()
			return ext, nil
		}
		errs = append(errs, err)
	}
	return netip.AddrPort{}, errors.Join(errs...)
}

// release removes the mapping (best effort).
func (m *Mapper) release(port uint16) {
	m.mu.Lock()
	method, cur, up := m.method, m.current, m.upnp
	m.mu.Unlock()
	if !cur.IsValid() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gw, err := gateway.DiscoverGateway()
	if err != nil {
		return
	}
	gwAddr, _ := netip.AddrFromSlice(gw.To4())
	switch method {
	case "natpmp":
		_, _ = natpmpMap(ctx, gwAddr, port, 0)
	case "pcp":
		if local, err := localAddrToward(gwAddr); err == nil {
			_, _ = pcpMap(ctx, gwAddr, local, port, 0)
		}
	case "upnp":
		if up != nil {
			_ = up.deleteMapping(ctx, cur.Port())
		}
	}
}

// localAddrToward returns our IPv4 address on the network that reaches the gateway.
func localAddrToward(gw netip.Addr) (netip.Addr, error) {
	c, err := net.Dial("udp4", net.JoinHostPort(gw.String(), "9"))
	if err != nil {
		return netip.Addr{}, err
	}
	defer c.Close()
	ua, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, errors.New("no local address")
	}
	a, _ := netip.AddrFromSlice(ua.IP.To4())
	return a, nil
}

// exchange sends a request to gw:port and returns the first matching reply, retrying with the
// timeouts the RFCs suggest (250 ms doubling, three tries).
func exchange(ctx context.Context, gw netip.Addr, port int, req []byte, accept func(resp []byte) bool) ([]byte, error) {
	c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(gw, uint16(port))))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	buf := make([]byte, 1100)
	timeout := 250 * time.Millisecond
	for try := 0; try < 3; try++ {
		if _, err := c.Write(req); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(timeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = c.SetReadDeadline(deadline)
		for {
			n, err := c.Read(buf)
			if err != nil {
				break
			}
			if accept(buf[:n]) {
				return append([]byte(nil), buf[:n]...), nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		timeout *= 2
	}
	return nil, errors.New("no answer from the router")
}
