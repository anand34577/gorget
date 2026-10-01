// Package tunx wraps the OS TUN device for WireGuard: it enforces the inbound
// firewall, answers DNS queries sent to the in-tunnel resolver address, and
// lets the platform swap the underlying TUN (Android re-creates it whenever
// routes change) without restarting WireGuard.
package tunx

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.zx2c4.com/wireguard/tun"
)

// DNS addresses served inside the tunnel.
var (
	DNSv4 = netip.MustParseAddr("100.100.100.100")
	DNSv6 = netip.MustParseAddr("fd00:6764:6e73::53")
)

// Resolver answers a raw DNS query.
type Resolver func(ctx context.Context, query []byte) []byte

type Wrapper struct {
	mu     sync.RWMutex
	dev    tun.Device
	gen    uint64
	swapCh chan struct{}
	closed atomic.Bool

	filter   atomic.Pointer[Filter]
	ct       *Conntrack
	resolver atomic.Pointer[Resolver]
	events   chan tun.Event
	mtu      int

	RxBytes, TxBytes, Dropped atomic.Uint64
}

// New wraps dev (which may be nil until the platform provides a TUN).
func New(dev tun.Device, mtu int) *Wrapper {
	w := &Wrapper{ct: NewConntrack(), events: make(chan tun.Event, 4), swapCh: make(chan struct{}), mtu: mtu}
	if dev != nil {
		w.SetDevice(dev)
	}
	w.events <- tun.EventUp
	return w
}

// SetDevice replaces the underlying TUN; the previous one is closed.
func (w *Wrapper) SetDevice(dev tun.Device) {
	w.mu.Lock()
	old := w.dev
	w.dev = dev
	w.gen++
	close(w.swapCh)
	w.swapCh = make(chan struct{})
	w.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (w *Wrapper) SetFilter(f *Filter)    { w.filter.Store(f) }
func (w *Wrapper) SetResolver(r Resolver) { w.resolver.Store(&r) }

func (w *Wrapper) current() (tun.Device, uint64, chan struct{}) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.dev, w.gen, w.swapCh
}

func (w *Wrapper) File() *os.File { return nil }

func (w *Wrapper) MTU() (int, error) { return w.mtu, nil }

func (w *Wrapper) Name() (string, error) {
	if d, _, _ := w.current(); d != nil {
		return d.Name()
	}
	return "gorget", nil
}

func (w *Wrapper) Events() <-chan tun.Event { return w.events }

func (w *Wrapper) BatchSize() int {
	if d, _, _ := w.current(); d != nil {
		return d.BatchSize()
	}
	return 1
}

func (w *Wrapper) Close() error {
	if w.closed.Swap(true) {
		return nil
	}
	w.mu.Lock()
	d := w.dev
	w.dev = nil
	close(w.swapCh)
	w.swapCh = make(chan struct{})
	w.mu.Unlock()
	close(w.events)
	if d != nil {
		return d.Close()
	}
	return nil
}

// Read returns packets leaving this device towards the tunnel.
func (w *Wrapper) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for {
		if w.closed.Load() {
			return 0, os.ErrClosed
		}
		d, gen, swap := w.current()
		if d == nil {
			select {
			case <-swap:
				continue
			case <-time.After(time.Second):
				continue
			}
		}
		n, err := d.Read(bufs, sizes, offset)
		if err != nil {
			if _, g, _ := w.current(); g != gen {
				continue // device was swapped
			}
			if errors.Is(err, os.ErrClosed) && !w.closed.Load() {
				// The OS closed the TUN (e.g. VPN revoked); wait for a replacement.
				select {
				case <-swap:
				case <-time.After(time.Second):
				}
				continue
			}
			return 0, err
		}
		// Filter in place: drop DNS queries we answer ourselves.
		out := 0
		for i := 0; i < n; i++ {
			pkt := bufs[i][offset : offset+sizes[i]]
			p, ok := parse(pkt)
			if ok && p.proto == protoUDP && p.dport == 53 && (p.dst == DNSv4 || p.dst == DNSv6) {
				w.answerDNS(p, pkt)
				continue
			}
			if ok {
				w.ct.Outbound(p)
			}
			w.TxBytes.Add(uint64(sizes[i]))
			if out != i {
				copy(bufs[out][offset:], pkt)
				sizes[out] = sizes[i]
			}
			out++
		}
		if out > 0 {
			return out, nil
		}
	}
}

// Write delivers packets arriving from peers to the OS, enforcing the firewall.
func (w *Wrapper) Write(bufs [][]byte, offset int) (int, error) {
	f := w.filter.Load()
	allowed := bufs[:0:0]
	for _, b := range bufs {
		pkt := b[offset:]
		p, ok := parse(pkt)
		if !ok || !(w.ct.IsReply(p) || f.allows(p)) {
			w.Dropped.Add(1)
			continue
		}
		w.RxBytes.Add(uint64(len(pkt)))
		allowed = append(allowed, b)
	}
	if len(allowed) == 0 {
		return len(bufs), nil
	}
	d, _, _ := w.current()
	if d == nil {
		return len(bufs), nil
	}
	if _, err := d.Write(allowed, offset); err != nil {
		return 0, err
	}
	return len(bufs), nil
}

func (w *Wrapper) answerDNS(p packet, pkt []byte) {
	rp := w.resolver.Load()
	if rp == nil {
		return
	}
	query := append([]byte(nil), pkt[p.payloadOff+8:]...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		resp := (*rp)(ctx, query)
		if resp == nil {
			return
		}
		reply := buildUDPReply(p.dst, p.src, p.dport, p.sport, resp)
		d, _, _ := w.current()
		if d == nil {
			return
		}
		const off = 16 // wireguard-go devices expect headroom before the packet
		buf := make([]byte, off+len(reply))
		copy(buf[off:], reply)
		_, _ = d.Write([][]byte{buf}, off)
	}()
}
