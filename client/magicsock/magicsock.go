// Package magicsock is Gorget's WireGuard transport. It implements
// wireguard-go's conn.Bind and decides, per packet, whether a peer is reached
// over a direct UDP path (found by NAT traversal) or through the relay.
//
// WireGuard is configured with a virtual endpoint per peer ("gk:<hex key>");
// roaming is disabled, so WireGuard always hands packets to us and we pick the
// path. Direct paths are discovered and kept alive with encrypted disco
// ping/pong messages; peers coordinate simultaneous hole punching by sending
// each other CallMeMaybe messages through the coordination server.
package magicsock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/net/ipv4"
	"golang.zx2c4.com/wireguard/conn"

	"github.com/anand34577/gorget/client/disco"
	"github.com/anand34577/gorget/client/portmap"
	"github.com/anand34577/gorget/client/relayclient"
	"github.com/anand34577/gorget/internal/stun"
)

type Key = [32]byte

// Timing (inspired by proven mesh VPN designs, tuned for battery on mobile).
const (
	trustBestFor     = 15 * time.Second // a direct path stays valid this long after its last pong
	heartbeat        = 5 * time.Second  // re-validate an active direct path
	discoverInterval = 4 * time.Second  // ping candidates while there is no direct path
	callMeMaybeEvery = 12 * time.Second
	activeFor        = 45 * time.Second // a peer is "active" if traffic flowed recently
	pingTimeout      = 5 * time.Second
	stunEvery        = 30 * time.Second
	maxCandidates    = 24
)

// PeerInfo describes a peer from the network map.
type PeerInfo struct {
	ID        string
	Name      string
	WGKey     Key
	DiscoKey  Key
	Endpoints []netip.AddrPort
	// HomeRelay is the URL of the relay the peer is connected to ("" = unknown).
	HomeRelay string
}

// PeerStatus is the live path state of a peer.
type PeerStatus struct {
	ID        string `json:"id"`
	Direct    bool   `json:"direct"`
	Endpoint  string `json:"endpoint,omitempty"`
	LatencyMs int    `json:"latency_ms"`
	Relayed   bool   `json:"relayed"`
	LastPong  int64  `json:"last_pong_unix,omitempty"`
}

type Options struct {
	Log  *slog.Logger
	Port uint16
	// ListenPacket opens UDP sockets (lets mobile platforms exclude them from the VPN).
	ListenPacket func(network, address string) (net.PacketConn, error)
	// SendSignal delivers a sealed disco message to a peer via the coordination server.
	SendSignal func(peerID string, sealed []byte)
	// OnEndpointsChanged is called when our candidate endpoints change.
	OnEndpointsChanged func([]netip.AddrPort)
	// ExcludePrefixes are never advertised as local endpoints (e.g. the overlay network).
	ExcludePrefixes func() []netip.Prefix
	// BlockDirect disables direct UDP between peers (testing / restrictive networks).
	BlockDirect bool
	// PortMapping asks the router (PCP, NAT-PMP, UPnP) to forward our UDP port, which makes
	// direct connections more likely behind home routers.
	PortMapping bool
	// OnPQ receives post-quantum key exchange messages from a peer (identified by WireGuard key).
	OnPQ func(peer Key, m disco.Message)
}

type Conn struct {
	opts      Options
	log       *slog.Logger
	discoPub  Key
	discoPriv Key
	nodeKey   atomic.Pointer[Key]

	mu       sync.Mutex
	peers    map[Key]*peer
	byDisco  map[Key]*peer
	pc4, pc6 *net.UDPConn
	bc4, bc6 *batchConn // nil where batching is unavailable
	port     uint16
	closed   chan struct{}
	open     bool

	relayMu  sync.RWMutex
	relay    *relayclient.Router
	relayIn  chan relayclient.Packet
	relayGen chan struct{}

	stunServers   []string
	stunTx        map[[12]byte]time.Time
	localAddrs    []netip.Addr // supplied by the platform when interface enumeration is unavailable
	reflexive     map[netip.AddrPort]time.Time
	mapped        netip.AddrPort // public address of the router's port mapping (zero = none)
	mapper        *portmap.Mapper
	mapCancel     context.CancelFunc // stops the current port-mapping loop (and removes the mapping)
	lastEndpoints []netip.AddrPort

	ctx    context.Context
	cancel context.CancelFunc

	TxDirect, RxDirect, TxRelay, RxRelay atomic.Uint64
}

type candidate struct {
	addr      netip.AddrPort
	fromSrv   bool
	lastPing  time.Time
	lastPong  time.Time
	latency   time.Duration
	learnedAt time.Time
}

type pendingPing struct {
	addr netip.AddrPort
	at   time.Time
}

type peer struct {
	info            PeerInfo
	cands           map[netip.AddrPort]*candidate
	best            netip.AddrPort
	bestLatency     time.Duration
	bestPong        time.Time
	lastActive      time.Time
	lastCallMeMaybe time.Time
	lastDiscover    time.Time
	pending         map[disco.TxID]pendingPing
}

// New creates a Conn with a fresh disco key pair.
func New(opts Options) (*Conn, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.ListenPacket == nil {
		opts.ListenPacket = func(network, address string) (net.PacketConn, error) { return net.ListenPacket(network, address) }
	}
	c := &Conn{
		opts:      opts,
		log:       opts.Log,
		peers:     map[Key]*peer{},
		byDisco:   map[Key]*peer{},
		relayIn:   make(chan relayclient.Packet, 512),
		stunTx:    map[[12]byte]time.Time{},
		reflexive: map[netip.AddrPort]time.Time{},
		port:      opts.Port,
	}
	if _, err := rand.Read(c.discoPriv[:]); err != nil {
		return nil, err
	}
	c.discoPriv[0] &= 248
	c.discoPriv[31] = (c.discoPriv[31] & 127) | 64
	pub, err := curve25519.X25519(c.discoPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, err
	}
	copy(c.discoPub[:], pub)
	c.ctx, c.cancel = context.WithCancel(context.Background())
	go c.loop()
	return c, nil
}

// DiscoPublic returns our disco public key (published to peers via the server).
func (c *Conn) DiscoPublic() Key { return c.discoPub }

// SetNodeKey sets our WireGuard public key (sent in pings so peers can verify us).
func (c *Conn) SetNodeKey(k Key) { c.nodeKey.Store(&k) }

// SetSTUNServers configures STUN servers ("host:port").
func (c *Conn) SetSTUNServers(s []string) {
	c.mu.Lock()
	c.stunServers = append([]string(nil), s...)
	c.mu.Unlock()
	go c.discoverEndpoints()
}

// SetLocalAddresses lets the platform supply interface addresses (Android blocks enumeration).
func (c *Conn) SetLocalAddresses(addrs []netip.Addr) {
	c.mu.Lock()
	c.localAddrs = addrs
	c.mu.Unlock()
	go c.discoverEndpoints()
}

// SetRelay switches the relay used for peers without a direct path.
func (c *Conn) SetRelay(r *relayclient.Router) {
	c.relayMu.Lock()
	if c.relayGen != nil {
		close(c.relayGen)
	}
	c.relay = r
	gen := make(chan struct{})
	c.relayGen = gen
	c.relayMu.Unlock()
	if r == nil {
		return
	}
	go func() {
		for {
			select {
			case <-gen:
				return
			case p := <-r.Recv():
				select {
				case c.relayIn <- p:
				default:
				}
			}
		}
	}()
}

func (c *Conn) currentRelay() *relayclient.Router {
	c.relayMu.RLock()
	defer c.relayMu.RUnlock()
	return c.relay
}

// SetPeers replaces the peer set, preserving path state for peers that remain.
func (c *Conn) SetPeers(infos []PeerInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := map[Key]bool{}
	for _, in := range infos {
		keep[in.WGKey] = true
		p := c.peers[in.WGKey]
		if p == nil {
			p = &peer{cands: map[netip.AddrPort]*candidate{}, pending: map[disco.TxID]pendingPing{}}
			c.peers[in.WGKey] = p
		}
		if p.info.DiscoKey != in.DiscoKey {
			delete(c.byDisco, p.info.DiscoKey)
		}
		p.info = in
		if in.DiscoKey != (Key{}) {
			c.byDisco[in.DiscoKey] = p
		}
		srv := map[netip.AddrPort]bool{}
		for _, ep := range in.Endpoints {
			ep = netip.AddrPortFrom(ep.Addr().Unmap(), ep.Port())
			srv[ep] = true
			if cd := p.cands[ep]; cd != nil {
				cd.fromSrv = true
			} else if len(p.cands) < maxCandidates {
				p.cands[ep] = &candidate{addr: ep, fromSrv: true}
			}
		}
		for ap, cd := range p.cands {
			if cd.fromSrv && !srv[ap] && time.Since(cd.lastPong) > trustBestFor {
				delete(p.cands, ap)
			}
		}
	}
	for k, p := range c.peers {
		if !keep[k] {
			delete(c.byDisco, p.info.DiscoKey)
			delete(c.peers, k)
		}
	}
}

// Status returns the path state of every peer.
func (c *Conn) Status() map[Key]PeerStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	out := make(map[Key]PeerStatus, len(c.peers))
	for k, p := range c.peers {
		st := PeerStatus{ID: p.info.ID}
		if ap, ok := p.direct(now); ok {
			st.Direct, st.Endpoint, st.LatencyMs = true, ap.String(), int(p.bestLatency.Milliseconds())
			st.LastPong = p.bestPong.Unix()
		} else {
			st.Relayed = true
		}
		out[k] = st
	}
	return out
}

// Endpoints returns our current candidate endpoints.
func (c *Conn) Endpoints() []netip.AddrPort {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]netip.AddrPort(nil), c.lastEndpoints...)
}

// NetworkChanged forgets direct paths and re-discovers endpoints (call on roaming).
func (c *Conn) NetworkChanged() {
	c.mu.Lock()
	for _, p := range c.peers {
		p.best = netip.AddrPort{}
		p.bestPong = time.Time{}
		for ap, cd := range p.cands {
			if !cd.fromSrv {
				delete(p.cands, ap)
			}
		}
	}
	c.reflexive = map[netip.AddrPort]time.Time{}
	mapper := c.mapper
	c.mu.Unlock()
	if mapper != nil {
		mapper.NetworkChanged() // a new network usually means a new router
	}
	go c.discoverEndpoints()
}

// Shutdown stops background work. Close (from wireguard-go) closes sockets.
func (c *Conn) Shutdown() {
	c.cancel()
	c.SetRelay(nil)
	_ = c.Close()
}

// static returns the fixed address of a peer that has no disco key: the server's
// gateway, which is plain WireGuard. There is nothing to probe, so the address the
// server published is the path (IPv4 first). Callers hold c.mu.
func (p *peer) static() (netip.AddrPort, bool) {
	if p.info.DiscoKey != (Key{}) {
		return netip.AddrPort{}, false
	}
	var v6 netip.AddrPort
	for _, ep := range p.info.Endpoints {
		ep = netip.AddrPortFrom(ep.Addr().Unmap(), ep.Port())
		switch {
		case !ep.IsValid():
		case ep.Addr().Is4():
			return ep, true
		case !v6.IsValid():
			v6 = ep
		}
	}
	return v6, v6.IsValid()
}

func (p *peer) direct(now time.Time) (netip.AddrPort, bool) {
	if ap, ok := p.static(); ok {
		return ap, true
	}
	if p.best.IsValid() && now.Sub(p.bestPong) < trustBestFor {
		return p.best, true
	}
	return netip.AddrPort{}, false
}

// ---------- conn.Bind ----------

type peerEP struct{ key Key }

func (e *peerEP) ClearSrc()           {}
func (e *peerEP) SrcToString() string { return "" }
func (e *peerEP) DstToString() string { return "gk:" + hex.EncodeToString(e.key[:]) }
func (e *peerEP) DstToBytes() []byte  { return e.key[:] }
func (e *peerEP) DstIP() netip.Addr   { return netip.Addr{} }
func (e *peerEP) SrcIP() netip.Addr   { return netip.Addr{} }

type udpEP struct{ ap netip.AddrPort }

func (e *udpEP) ClearSrc()           {}
func (e *udpEP) SrcToString() string { return "" }
func (e *udpEP) DstToString() string { return e.ap.String() }
func (e *udpEP) DstToBytes() []byte  { b, _ := e.ap.MarshalBinary(); return b }
func (e *udpEP) DstIP() netip.Addr   { return e.ap.Addr() }
func (e *udpEP) SrcIP() netip.Addr   { return netip.Addr{} }

// EndpointString is the WireGuard endpoint string for a peer key.
func EndpointString(k Key) string { return "gk:" + hex.EncodeToString(k[:]) }

func (c *Conn) ParseEndpoint(s string) (conn.Endpoint, error) {
	if strings.HasPrefix(s, "gk:") {
		b, err := hex.DecodeString(s[3:])
		if err != nil || len(b) != 32 {
			return nil, errors.New("invalid peer endpoint")
		}
		var k Key
		copy(k[:], b)
		return &peerEP{key: k}, nil
	}
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return &udpEP{ap: ap}, nil
}

func (c *Conn) BatchSize() int       { return batchSize }
func (c *Conn) SetMark(uint32) error { return nil }

func (c *Conn) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open {
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	if port == 0 {
		port = c.port
	}
	pc4, actual, err := c.listen("udp4", port)
	if err != nil && port != 0 {
		pc4, actual, err = c.listen("udp4", 0)
	}
	if err != nil {
		return nil, 0, err
	}
	pc6, _, err6 := c.listen("udp6", actual)
	if err6 != nil {
		c.log.Debug("IPv6 UDP unavailable", "err", err6)
		pc6 = nil
	}
	c.pc4, c.pc6, c.port, c.open = pc4, pc6, actual, true
	c.bc4, c.bc6 = newBatchConn(pc4, false), nil
	if pc6 != nil {
		c.bc6 = newBatchConn(pc6, true)
	}
	c.closed = make(chan struct{})
	fns := []conn.ReceiveFunc{c.receiveUDP(pc4, c.bc4), c.receiveRelay(c.closed)}
	if pc6 != nil {
		fns = append(fns, c.receiveUDP(pc6, c.bc6))
	}
	go c.discoverEndpoints()
	if c.mapCancel != nil {
		c.mapCancel() // the bind was reopened (possibly on another port)
	}
	if c.opts.PortMapping && !c.opts.BlockDirect {
		mctx, mcancel := context.WithCancel(c.ctx)
		c.mapCancel = mcancel
		c.mapper = portmap.New(c.log)
		go c.mapper.Run(mctx, actual, func(ext netip.AddrPort) {
			c.mu.Lock()
			c.mapped = ext
			c.mu.Unlock()
			if ext.IsValid() {
				c.log.Info("router forwards our port", "public", ext, "via", c.mapper.Method())
			}
			c.publishEndpoints()
		})
	}
	return fns, actual, nil
}

func (c *Conn) listen(network string, port uint16) (*net.UDPConn, uint16, error) {
	addr := ":" + itoa(int(port))
	pc, err := c.opts.ListenPacket(network, addr)
	if err != nil {
		return nil, 0, err
	}
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, 0, errors.New("ListenPacket must return *net.UDPConn")
	}
	_ = uc.SetReadBuffer(7 << 20)
	_ = uc.SetWriteBuffer(7 << 20)
	return uc, uint16(uc.LocalAddr().(*net.UDPAddr).Port), nil
}

func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return nil
	}
	c.open = false
	if c.mapCancel != nil {
		c.mapCancel()
		c.mapCancel = nil
	}
	close(c.closed)
	var err error
	if c.pc4 != nil {
		err = c.pc4.Close()
	}
	if c.pc6 != nil {
		_ = c.pc6.Close()
	}
	return err
}

// Port returns the local UDP port.
func (c *Conn) Port() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.port
}

func (c *Conn) receiveUDP(pc *net.UDPConn, bc *batchConn) conn.ReceiveFunc {
	if bc != nil {
		return c.receiveUDPBatch(bc)
	}
	return func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		for {
			n, src, err := pc.ReadFromUDPAddrPort(bufs[0])
			if err != nil {
				return 0, err
			}
			src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
			b := bufs[0][:n]
			if isSTUNResponse(b) {
				c.handleSTUN(b)
				continue
			}
			if disco.IsDisco(b) {
				c.handleDisco(b, src, nil)
				continue
			}
			if c.opts.BlockDirect {
				continue
			}
			c.RxDirect.Add(uint64(n))
			sizes[0] = n
			eps[0] = c.endpointForSrc(src)
			return 1, nil
		}
	}
}

func (c *Conn) receiveRelay(closed chan struct{}) conn.ReceiveFunc {
	return func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		for {
			select {
			case <-closed:
				return 0, net.ErrClosed
			case p := <-c.relayIn:
				if disco.IsDisco(p.Data) {
					src := p.Src
					c.handleDisco(p.Data, netip.AddrPort{}, &src)
					continue
				}
				n := copy(bufs[0], p.Data)
				c.RxRelay.Add(uint64(n))
				sizes[0] = n
				eps[0] = &peerEP{key: p.Src}
				c.markActive(p.Src)
				return 1, nil
			}
		}
	}
}

// endpointForSrc maps a source address to the peer whose direct path it is.
func (c *Conn) endpointForSrc(src netip.AddrPort) conn.Endpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, p := range c.peers {
		if st, ok := p.static(); p.best == src || (ok && st == src) {
			p.lastActive = time.Now()
			return &peerEP{key: k}
		}
	}
	return &udpEP{ap: src}
}

func (c *Conn) markActive(k Key) {
	c.mu.Lock()
	if p := c.peers[k]; p != nil {
		p.lastActive = time.Now()
	}
	c.mu.Unlock()
}

func (c *Conn) Send(bufs [][]byte, ep conn.Endpoint) error {
	switch e := ep.(type) {
	case *udpEP:
		return c.writeUDP(e.ap, bufs)
	case *peerEP:
		now := time.Now()
		c.mu.Lock()
		p := c.peers[e.key]
		var addr netip.AddrPort
		var direct bool
		home := ""
		if p != nil {
			home = p.info.HomeRelay
			p.lastActive = now
			if !c.opts.BlockDirect {
				addr, direct = p.direct(now)
			}
			if !direct && now.Sub(p.lastDiscover) > discoverInterval {
				p.lastDiscover = now
				go c.discover(e.key)
			}
		}
		c.mu.Unlock()
		if p == nil {
			return nil // unknown peer: drop silently
		}
		if direct {
			if err := c.writeUDP(addr, bufs); err == nil {
				return nil
			}
		}
		r := c.currentRelay()
		if r == nil {
			return errors.New("no path to peer")
		}
		for _, b := range bufs {
			if r.Send(e.key, home, b) {
				c.TxRelay.Add(uint64(len(b)))
			}
		}
		return nil
	}
	return conn.ErrWrongEndpointType
}

func (c *Conn) writeUDP(ap netip.AddrPort, bufs [][]byte) error {
	c.mu.Lock()
	pc, bc := c.pc4, c.bc4
	if ap.Addr().Is6() {
		pc, bc = c.pc6, c.bc6
	}
	c.mu.Unlock()
	if pc == nil {
		return errors.New("socket closed")
	}
	if bc != nil && len(bufs) > 1 {
		return c.writeBatch(bc, ap, bufs)
	}
	for _, b := range bufs {
		if _, err := pc.WriteToUDPAddrPort(b, ap); err != nil {
			return err
		}
		c.TxDirect.Add(uint64(len(b)))
	}
	return nil
}

// ---------- disco ----------

func (c *Conn) sendDisco(p *peer, m disco.Message, to netip.AddrPort) {
	c.mu.Lock()
	info := p.info // a copy: SetPeers may replace it concurrently
	c.mu.Unlock()
	pkt := disco.Seal(m, &c.discoPub, &c.discoPriv, &info.DiscoKey)
	if to.IsValid() {
		if c.opts.BlockDirect {
			return
		}
		_ = c.writeUDP(to, [][]byte{pkt})
		return
	}
	if r := c.currentRelay(); r != nil {
		r.Send(info.WGKey, info.HomeRelay, pkt)
	}
}

// discover pings all candidates of a peer and asks it (via the server) to ping us back.
func (c *Conn) discover(k Key) {
	if c.opts.BlockDirect {
		return
	}
	now := time.Now()
	nk := c.nodeKey.Load()
	if nk == nil {
		return
	}
	c.mu.Lock()
	p := c.peers[k]
	if p == nil || p.info.DiscoKey == (Key{}) {
		c.mu.Unlock()
		return
	}
	type job struct {
		to netip.AddrPort
		m  *disco.Ping
	}
	var jobs []job
	for ap, cd := range p.cands {
		if now.Sub(cd.lastPing) < time.Second {
			continue
		}
		cd.lastPing = now
		tx := disco.NewTxID()
		p.pending[tx] = pendingPing{addr: ap, at: now}
		jobs = append(jobs, job{to: ap, m: &disco.Ping{TxID: tx, NodeKey: *nk}})
	}
	for tx, pp := range p.pending {
		if now.Sub(pp.at) > pingTimeout {
			delete(p.pending, tx)
		}
	}
	sendCMM := now.Sub(p.lastCallMeMaybe) > callMeMaybeEvery && c.opts.SendSignal != nil && len(c.lastEndpoints) > 0
	var cmm []byte
	if sendCMM {
		p.lastCallMeMaybe = now
		cmm = disco.Seal(&disco.CallMeMaybe{Endpoints: c.lastEndpoints}, &c.discoPub, &c.discoPriv, &p.info.DiscoKey)
	}
	peerID := p.info.ID
	c.mu.Unlock()
	for _, j := range jobs {
		c.sendDisco(p, j.m, j.to)
	}
	if cmm != nil {
		c.opts.SendSignal(peerID, cmm)
	}
}

// SendPQ sends a key-exchange message to a peer through the coordination server
// (these are too large for a direct UDP datagram and need no low latency).
func (c *Conn) SendPQ(peer Key, m disco.Message) {
	c.mu.Lock()
	p := c.peers[peer]
	var pkt []byte
	var id string
	if p != nil && p.info.DiscoKey != (Key{}) {
		pkt = disco.Seal(m, &c.discoPub, &c.discoPriv, &p.info.DiscoKey)
		id = p.info.ID
	}
	c.mu.Unlock()
	if pkt != nil && c.opts.SendSignal != nil {
		c.opts.SendSignal(id, pkt)
	}
}

// HandleSignal processes a disco message relayed by the coordination server.
func (c *Conn) HandleSignal(sealed []byte) {
	if disco.IsDisco(sealed) {
		c.handleDisco(sealed, netip.AddrPort{}, nil)
	}
}

// handleDisco processes a disco packet received directly (src set) or via relay/server (src unset).
func (c *Conn) handleDisco(b []byte, src netip.AddrPort, relaySrc *Key) {
	sender, ok := disco.SenderKey(b)
	if !ok {
		return
	}
	c.mu.Lock()
	p := c.byDisco[sender]
	c.mu.Unlock()
	if p == nil {
		return // not a known peer: ignore
	}
	m, _, err := disco.Open(b, &c.discoPriv)
	if err != nil {
		return
	}
	now := time.Now()
	switch msg := m.(type) {
	case *disco.Ping:
		if msg.NodeKey != p.info.WGKey {
			return
		}
		pong := &disco.Pong{TxID: msg.TxID, Src: src}
		c.sendDisco(p, pong, src)
		if src.IsValid() {
			c.mu.Lock()
			if _, ok := p.cands[src]; !ok && len(p.cands) < maxCandidates {
				p.cands[src] = &candidate{addr: src, learnedAt: now}
			}
			needPath := !p.best.IsValid() || now.Sub(p.bestPong) > trustBestFor
			c.mu.Unlock()
			if needPath {
				go c.discover(p.info.WGKey)
			}
		}
	case *disco.Pong:
		if !src.IsValid() {
			return // only pongs received over UDP prove a direct path
		}
		c.mu.Lock()
		pp, ok := p.pending[msg.TxID]
		if ok {
			delete(p.pending, msg.TxID)
		}
		if !ok || (src.IsValid() && pp.addr != src) {
			c.mu.Unlock()
			return
		}
		lat := now.Sub(pp.at)
		cd := p.cands[pp.addr]
		if cd == nil {
			cd = &candidate{addr: pp.addr}
			p.cands[pp.addr] = cd
		}
		cd.lastPong, cd.latency = now, lat
		// Choose the best (lowest latency) recently-validated candidate.
		prev := p.best
		best, bestLat := pp.addr, lat
		for ap, x := range p.cands {
			if now.Sub(x.lastPong) < trustBestFor && x.latency < bestLat {
				best, bestLat = ap, x.latency
			}
		}
		p.best, p.bestLatency, p.bestPong = best, bestLat, now
		if msg.Src.IsValid() {
			c.reflexive[msg.Src] = now
		}
		name := p.info.Name
		c.mu.Unlock()
		if prev != best {
			c.log.Info("direct path established", "peer", name, "endpoint", best, "latency", lat.Round(time.Millisecond))
		}
	case *disco.PQInit, *disco.PQResp, *disco.PQCommit, *disco.PQAck:
		// Only accepted through the signal channel, where the server authenticated the sender.
		if !src.IsValid() && relaySrc == nil && c.opts.OnPQ != nil {
			c.opts.OnPQ(p.info.WGKey, m)
		}
	case *disco.CallMeMaybe:
		c.mu.Lock()
		for _, ep := range msg.Endpoints {
			if _, ok := p.cands[ep]; !ok && len(p.cands) < maxCandidates {
				p.cands[ep] = &candidate{addr: ep, learnedAt: now}
			}
		}
		p.lastActive = now
		c.mu.Unlock()
		go c.discover(p.info.WGKey)
	}
}

// ---------- background ----------

func (c *Conn) loop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	lastSTUN := time.Time{}
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		if now.Sub(lastSTUN) > stunEvery {
			lastSTUN = now
			go c.discoverEndpoints()
		}
		var heartbeats, discovers []Key
		c.mu.Lock()
		for k, p := range c.peers {
			if now.Sub(p.lastActive) > activeFor {
				continue // idle: don't spend battery
			}
			if _, ok := p.direct(now); ok {
				if p.info.DiscoKey == (Key{}) {
					continue // a fixed path needs no probing
				}
				if now.Sub(p.bestPong) > heartbeat && now.Sub(p.lastDiscover) > heartbeat {
					p.lastDiscover = now
					heartbeats = append(heartbeats, k)
				}
			} else if now.Sub(p.lastDiscover) > discoverInterval {
				p.lastDiscover = now
				discovers = append(discovers, k)
			}
		}
		c.mu.Unlock()
		for _, k := range heartbeats {
			c.pingBest(k)
		}
		for _, k := range discovers {
			c.discover(k)
		}
	}
}

func (c *Conn) pingBest(k Key) {
	nk := c.nodeKey.Load()
	if nk == nil {
		return
	}
	c.mu.Lock()
	p := c.peers[k]
	if p == nil || !p.best.IsValid() {
		c.mu.Unlock()
		return
	}
	tx := disco.NewTxID()
	p.pending[tx] = pendingPing{addr: p.best, at: time.Now()}
	to := p.best
	c.mu.Unlock()
	c.sendDisco(p, &disco.Ping{TxID: tx, NodeKey: *nk}, to)
}

// ---------- endpoint discovery ----------

func isSTUNResponse(b []byte) bool {
	return len(b) >= 20 && b[0] == 0x01 && b[1] == 0x01 && b[4] == 0x21 && b[5] == 0x12 && b[6] == 0xA4 && b[7] == 0x42
}

func (c *Conn) handleSTUN(b []byte) {
	ap, err := stun.ParseResponse(b)
	if err != nil {
		return
	}
	var tx [12]byte
	copy(tx[:], b[8:20])
	c.mu.Lock()
	_, ok := c.stunTx[tx]
	delete(c.stunTx, tx)
	if ok {
		c.reflexive[netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())] = time.Now()
	}
	c.mu.Unlock()
	if ok {
		c.publishEndpoints()
	}
}

func (c *Conn) discoverEndpoints() {
	c.mu.Lock()
	servers := append([]string(nil), c.stunServers...)
	pc4, pc6 := c.pc4, c.pc6
	for tx, at := range c.stunTx {
		if time.Since(at) > 10*time.Second {
			delete(c.stunTx, tx)
		}
	}
	c.mu.Unlock()
	if pc4 == nil {
		return
	}
	for _, s := range servers {
		addrs, err := net.DefaultResolver.LookupNetIP(c.ctx, "ip", hostOnly(s))
		if err != nil {
			continue
		}
		port := portOf(s, 3478)
		for _, a := range addrs {
			var tx [12]byte
			_, _ = rand.Read(tx[:])
			c.mu.Lock()
			c.stunTx[tx] = time.Now()
			c.mu.Unlock()
			to := netip.AddrPortFrom(a.Unmap(), port)
			pc := pc4
			if to.Addr().Is6() {
				if pc6 == nil {
					continue
				}
				pc = pc6
			}
			_, _ = pc.WriteToUDPAddrPort(stun.Request(tx), to)
		}
	}
	c.publishEndpoints()
}

func (c *Conn) publishEndpoints() {
	c.mu.Lock()
	port := c.port
	set := map[netip.AddrPort]bool{}
	for ap, at := range c.reflexive {
		if time.Since(at) < 3*stunEvery {
			set[ap] = true
		}
	}
	local := c.localAddrs
	if c.mapped.IsValid() {
		set[c.mapped] = true
	}
	c.mu.Unlock()
	if len(local) == 0 {
		local = interfaceAddrs()
	}
	var excl []netip.Prefix
	if c.opts.ExcludePrefixes != nil {
		excl = c.opts.ExcludePrefixes()
	}
	for _, a := range local {
		if !usableLocal(a, excl) || port == 0 {
			continue
		}
		set[netip.AddrPortFrom(a, port)] = true
	}
	eps := make([]netip.AddrPort, 0, len(set))
	for ap := range set {
		eps = append(eps, ap)
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].String() < eps[j].String() })
	c.mu.Lock()
	changed := !equalEndpoints(eps, c.lastEndpoints)
	c.lastEndpoints = eps
	c.mu.Unlock()
	if changed && c.opts.OnEndpointsChanged != nil {
		c.opts.OnEndpointsChanged(eps)
	}
}

func usableLocal(a netip.Addr, excl []netip.Prefix) bool {
	if !a.IsValid() || a.IsLoopback() && !testLoopback || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range excl {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// testLoopback allows loopback endpoints (tests run peers on one host).
var testLoopback = false

// AllowLoopbackEndpoints is for tests running several peers on one machine.
func AllowLoopbackEndpoints() { testLoopback = true }

func interfaceAddrs() []netip.Addr {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		if ifc.Flags&net.FlagLoopback != 0 && !testLoopback {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(ipn.IP); ok {
					out = append(out, ip.Unmap())
				}
			}
		}
	}
	return out
}

func equalEndpoints(a, b []netip.AddrPort) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hostOnly(hp string) string {
	if h, _, err := net.SplitHostPort(hp); err == nil {
		return h
	}
	return hp
}

func portOf(hp string, def uint16) uint16 {
	if _, p, err := net.SplitHostPort(hp); err == nil {
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return def
			}
			n = n*10 + int(ch-'0')
		}
		if n > 0 && n < 65536 {
			return uint16(n)
		}
	}
	return def
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// receiveUDPBatch reads up to len(bufs) datagrams per system call. Discovery and STUN
// packets are handled here and dropped from the batch, so only WireGuard packets reach
// wireguard-go.
func (c *Conn) receiveUDPBatch(bc *batchConn) conn.ReceiveFunc {
	var msgs []ipv4.Message
	return func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		if len(msgs) < len(bufs) {
			msgs = make([]ipv4.Message, len(bufs))
			for i := range msgs {
				msgs[i].Buffers = make([][]byte, 1)
			}
		}
		for {
			for i := range bufs {
				msgs[i].Buffers[0] = bufs[i]
			}
			n, err := bc.read(msgs[:len(bufs)], 0)
			if err != nil {
				return 0, err
			}
			out := 0
			var lastSrc netip.AddrPort
			var lastEP conn.Endpoint
			for i := 0; i < n; i++ {
				m := &msgs[i]
				ua, ok := m.Addr.(*net.UDPAddr)
				if !ok || m.N == 0 {
					continue
				}
				src := ua.AddrPort()
				src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
				b := bufs[i][:m.N]
				if isSTUNResponse(b) {
					c.handleSTUN(b)
					continue
				}
				if disco.IsDisco(b) {
					c.handleDisco(b, src, nil)
					continue
				}
				if c.opts.BlockDirect {
					continue
				}
				if out != i {
					copy(bufs[out], b)
				}
				sizes[out] = m.N
				if lastEP == nil || src != lastSrc {
					lastEP, lastSrc = c.endpointForSrc(src), src // a batch usually comes from one peer
				}
				eps[out] = lastEP
				c.RxDirect.Add(uint64(m.N))
				out++
			}
			if out > 0 {
				return out, nil
			}
		}
	}
}

// writeBatch sends several datagrams to one address with as few system calls as possible.
func (c *Conn) writeBatch(bc *batchConn, ap netip.AddrPort, bufs [][]byte) error {
	to := net.UDPAddrFromAddrPort(ap)
	msgs := make([]ipv4.Message, len(bufs))
	for i, b := range bufs {
		msgs[i] = ipv4.Message{Buffers: [][]byte{b}, Addr: to}
	}
	for sent := 0; sent < len(msgs); {
		n, err := bc.write(msgs[sent:], 0)
		if err != nil {
			return err
		}
		for _, m := range msgs[sent : sent+n] {
			c.TxDirect.Add(uint64(len(m.Buffers[0])))
		}
		sent += n
	}
	return nil
}
