package relayclient

import (
	"context"
	"net"
	"net/url"
	"sort"
	"sync"
	"time"
)

// Relay describes a relay offered by the server.
type Relay struct {
	Region  string
	Name    string
	URL     string
	UDPAddr string
}

const (
	idleClose    = 5 * time.Minute
	remeasure    = 10 * time.Minute
	switchFactor = 0.8 // a relay must be this much faster (and 10 ms) to replace the home relay
)

// Router keeps a connection to the nearest ("home") relay and, when a peer lives on
// another relay, a connection to that one too. Packets for a peer go to the relay the
// peer is homed on; packets from any relay are merged into one stream.
type Router struct {
	cfg Config // template: Token, HTTP, ListenPacket, BlockUDP, Log, OnPeerGone

	mu      sync.Mutex
	relays  []Relay
	home    string
	rtt     map[string]time.Duration
	clients map[string]*entry
	ctx     context.Context
	cancel  context.CancelFunc
	recv    chan Packet
	// measuredAt is when relay latencies were last measured.
	measuredAt time.Time
}

type entry struct {
	c      *Client
	cancel context.CancelFunc
	used   time.Time
}

// NewRouter creates a router whose connections live until ctx ends; Run does the upkeep.
// cfg supplies everything except URL and UDPAddr.
func NewRouter(ctx context.Context, cfg Config) *Router {
	r := &Router{cfg: cfg, rtt: map[string]time.Duration{}, clients: map[string]*entry{}, recv: make(chan Packet, 1024)}
	r.ctx, r.cancel = context.WithCancel(ctx)
	return r
}

// Recv delivers packets from every relay.
func (r *Router) Recv() <-chan Packet { return r.recv }

// Run manages connections until ctx ends.
func (r *Router) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			for _, e := range r.clients {
				e.cancel()
			}
			r.clients = map[string]*entry{}
			r.mu.Unlock()
			return
		case <-tick.C:
			r.closeIdle()
			if len(r.relaysSnapshot()) > 1 && time.Since(r.lastMeasure()) > remeasure {
				go r.measure()
			}
		}
	}
}

func (r *Router) lastMeasure() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.measuredAt
}

func (r *Router) relaysSnapshot() []Relay {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Relay(nil), r.relays...)
}

// SetRelays replaces the relay list (from the network map).
func (r *Router) SetRelays(list []Relay) {
	r.mu.Lock()
	r.relays = append([]Relay(nil), list...)
	have := false
	for _, x := range list {
		if x.URL == r.home {
			have = true
		}
	}
	if !have {
		r.home = ""
		if len(list) > 0 {
			r.home = list[0].URL
		}
	}
	home := r.home
	r.mu.Unlock()
	if home != "" {
		r.ensure(home)
	}
	if len(list) > 1 {
		go r.measure()
	}
}

// Home returns the URL of the home relay ("" when there is none).
func (r *Router) Home() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.home
}

// HomeConnected reports whether the home relay is connected, and how.
func (r *Router) HomeConnected() (bool, string) {
	r.mu.Lock()
	e := r.clients[r.home]
	r.mu.Unlock()
	if e == nil {
		return false, ""
	}
	return e.c.Connected.Load(), e.c.Transport()
}

// Send sends pkt to dst, over the relay dst is homed on (peerHome) or ours when that is unknown.
func (r *Router) Send(dst Key, peerHome string, pkt []byte) bool {
	r.mu.Lock()
	target := r.home
	if peerHome != "" && r.known(peerHome) {
		target = peerHome
	}
	r.mu.Unlock()
	if target == "" {
		return false
	}
	e := r.ensure(target)
	if e == nil {
		return false
	}
	r.mu.Lock()
	e.used = time.Now()
	r.mu.Unlock()
	return e.c.Send(dst, pkt)
}

// Stats sums traffic over all relay connections.
func (r *Router) Stats() (tx, rx uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.clients {
		tx += e.c.TxBytes.Load()
		rx += e.c.RxBytes.Load()
	}
	return
}

// Names lists relay URLs currently connected (for diagnostics).
func (r *Router) Connected() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for u, e := range r.clients {
		if e.c.Connected.Load() {
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return out
}

func (r *Router) known(u string) bool {
	for _, x := range r.relays {
		if x.URL == u {
			return true
		}
	}
	return false
}

// ensure returns the connected-or-connecting client for a relay URL, creating it if needed.
func (r *Router) ensure(u string) *entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.clients[u]; e != nil {
		return e
	}
	if r.ctx == nil || !r.known(u) {
		return nil
	}
	var rel Relay
	for _, x := range r.relays {
		if x.URL == u {
			rel = x
		}
	}
	cfg := r.cfg
	cfg.URL, cfg.UDPAddr = rel.URL, rel.UDPAddr
	c := New(cfg)
	ctx, cancel := context.WithCancel(r.ctx)
	e := &entry{c: c, cancel: cancel, used: time.Now()}
	r.clients[u] = e
	go c.Run(ctx)
	go func() { // merge into the shared stream
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-c.Recv():
				select {
				case r.recv <- p:
				default:
				}
			}
		}
	}()
	return e
}

// closeIdle drops connections to non-home relays nobody used for a while.
func (r *Router) closeIdle() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for u, e := range r.clients {
		if u != r.home && time.Since(e.used) > idleClose {
			e.cancel()
			delete(r.clients, u)
		}
	}
}

// measure times a TCP connection to every relay and moves the home relay to a clearly faster one.
func (r *Router) measure() {
	list := r.relaysSnapshot()
	if len(list) < 2 {
		return
	}
	r.mu.Lock()
	r.measuredAt = time.Now()
	r.mu.Unlock()
	res := map[string]time.Duration{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, rel := range list {
		wg.Add(1)
		go func(rel Relay) {
			defer wg.Done()
			d := r.probe(rel.URL)
			mu.Lock()
			res[rel.URL] = d
			mu.Unlock()
		}(rel)
	}
	wg.Wait()
	r.mu.Lock()
	for u, d := range res {
		r.rtt[u] = d
	}
	cur := r.home
	best, bestD := cur, r.rtt[cur]
	for _, rel := range r.relays {
		d := r.rtt[rel.URL]
		if d > 0 && (bestD == 0 || d < bestD) {
			best, bestD = rel.URL, d
		}
	}
	if best != cur {
		curD := r.rtt[cur]
		if curD == 0 || (float64(bestD) < switchFactor*float64(curD) && curD-bestD > 10*time.Millisecond) {
			r.home = best
		}
	}
	home := r.home
	r.mu.Unlock()
	if home != "" {
		r.ensure(home)
	}
}

// probe returns the TCP connect time to the relay's host (0 when unreachable).
func (r *Router) probe(rawURL string) time.Duration {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	host := u.Host
	if u.Port() == "" {
		port := "443"
		if u.Scheme == "ws" {
			port = "80"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	dial := r.cfg.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 3 * time.Second}).DialContext
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	c, err := dial(ctx, "tcp", host)
	if err != nil {
		return 0
	}
	c.Close()
	return time.Since(start)
}
