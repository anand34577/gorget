// Package relayclient connects to Gorget relays and carries already-encrypted
// WireGuard packets to peers that can't be reached directly.
//
// A Client talks to one relay over UDP when the relay offers it and UDP works,
// and over a WebSocket (HTTPS, port 443) otherwise. A Router (router.go) manages
// the clients for several relays.
package relayclient

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// WebSocket frames.
const (
	frameSend     = 0x01
	frameRecv     = 0x02
	framePing     = 0x03
	framePong     = 0x04
	framePeerGone = 0x05
	keyLen        = 32
)

// UDP datagrams (see internal/relay/udp.go).
const (
	udpHello    = 0x20
	udpHelloOK  = 0x21
	udpSend     = 0x22
	udpRecv     = 0x23
	udpPing     = 0x24
	udpPong     = 0x25
	udpPeerGone = 0x26
	udpBadSess  = 0x27

	udpSessionLen = 8
	udpRetryAfter = 5 * time.Minute
)

type Key = [keyLen]byte

// Packet is a packet received from a peer through the relay.
type Packet struct {
	Src  Key
	Data []byte
}

// Config describes one relay.
type Config struct {
	// URL is the WebSocket relay (wss://host/relay); always available.
	URL string
	// UDPAddr is host:port of the UDP relay ("" = WebSocket only).
	UDPAddr string
	// Token returns the current device session token.
	Token func() string
	// HTTP dials the WebSocket (sockets excluded from the VPN).
	HTTP *http.Client
	// ListenPacket opens UDP sockets (excluded from the VPN).
	ListenPacket func(network, address string) (net.PacketConn, error)
	// Dial opens TCP connections for latency probes (sockets excluded from the VPN).
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// BlockUDP forces WebSocket (tests, restrictive networks).
	BlockUDP   bool
	Log        *slog.Logger
	OnPeerGone func(Key)
}

var errUDPUnavailable = errors.New("UDP relay not reachable")

type Client struct {
	cfg  Config
	log  *slog.Logger
	recv chan Packet
	out  chan []byte // frames in WebSocket format; converted for UDP when written

	udpRetryAt atomic.Int64 // unix nanos; no UDP attempts before this
	transport  atomic.Value // "udp" | "websocket"

	Connected atomic.Bool
	TxBytes   atomic.Uint64
	RxBytes   atomic.Uint64
}

// New creates a relay client; call Run to connect.
func New(cfg Config) *Client {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	c := &Client{cfg: cfg, log: cfg.Log, recv: make(chan Packet, 512), out: make(chan []byte, 512)}
	c.transport.Store("")
	return c
}

func (c *Client) URL() string { return c.cfg.URL }

// Transport reports how the client is connected: "udp", "websocket" or "" when down.
func (c *Client) Transport() string {
	if !c.Connected.Load() {
		return ""
	}
	return c.transport.Load().(string)
}

// Recv delivers packets from peers.
func (c *Client) Recv() <-chan Packet { return c.recv }

// Send queues a packet for dst. It never blocks: when the queue is full the packet
// is dropped, like UDP.
func (c *Client) Send(dst Key, pkt []byte) bool {
	if !c.Connected.Load() {
		return false
	}
	frame := make([]byte, 1+keyLen+len(pkt))
	frame[0] = frameSend
	copy(frame[1:], dst[:])
	copy(frame[1+keyLen:], pkt)
	select {
	case c.out <- frame:
		return true
	default:
		return false
	}
}

func (c *Client) peerGone(k Key) {
	if c.cfg.OnPeerGone != nil {
		c.cfg.OnPeerGone(k)
	}
}

// Run keeps a connection open until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := errUDPUnavailable
		if c.cfg.UDPAddr != "" && !c.cfg.BlockUDP && time.Now().UnixNano() >= c.udpRetryAt.Load() {
			err = c.udpSession(ctx)
			if errors.Is(err, errUDPUnavailable) {
				c.udpRetryAt.Store(time.Now().Add(udpRetryAfter).UnixNano())
			}
		}
		if errors.Is(err, errUDPUnavailable) {
			err = c.wsSession(ctx)
		}
		c.Connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			backoff = time.Second
		}
		c.log.Debug("relay disconnected", "url", c.cfg.URL, "err", err, "retry", backoff)
		jitter := time.Duration(rand.Int64N(int64(backoff / 2)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff + jitter):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// ---------- UDP transport ----------

// udpHandshake opens a socket and says hello; it returns the socket and session id.
func (c *Client) udpHandshake(ctx context.Context) (net.PacketConn, *net.UDPAddr, [udpSessionLen]byte, error) {
	var sess [udpSessionLen]byte
	tok := c.cfg.Token()
	if tok == "" {
		return nil, nil, sess, errors.New("no session token")
	}
	raddr, err := net.ResolveUDPAddr("udp", c.cfg.UDPAddr)
	if err != nil {
		return nil, nil, sess, errUDPUnavailable
	}
	listen := c.cfg.ListenPacket
	if listen == nil {
		listen = func(n, a string) (net.PacketConn, error) { return net.ListenPacket(n, a) }
	}
	network := "udp4"
	if raddr.IP.To4() == nil {
		network = "udp6"
	}
	pc, err := listen(network, ":0")
	if err != nil {
		return nil, nil, sess, errUDPUnavailable
	}
	hello := make([]byte, 3+len(tok))
	hello[0] = udpHello
	binary.BigEndian.PutUint16(hello[1:], uint16(len(tok)))
	copy(hello[3:], tok)
	buf := make([]byte, 64)
	for attempt := 0; attempt < 4 && ctx.Err() == nil; attempt++ {
		if _, err := pc.WriteTo(hello, raddr); err != nil {
			break
		}
		_ = pc.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				break // timeout: send hello again
			}
			if ua, ok := from.(*net.UDPAddr); ok && ua.Port == raddr.Port && n == 1+udpSessionLen && buf[0] == udpHelloOK {
				copy(sess[:], buf[1:n])
				_ = pc.SetReadDeadline(time.Time{})
				return pc, raddr, sess, nil
			}
		}
	}
	pc.Close()
	return nil, nil, sess, errUDPUnavailable
}

func (c *Client) udpSession(ctx context.Context) error {
	pc, raddr, sess, err := c.udpHandshake(ctx)
	if err != nil {
		return err
	}
	defer pc.Close()
	c.transport.Store("udp")
	c.Connected.Store(true)
	c.log.Debug("relay connected", "url", c.cfg.URL, "transport", "udp")

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-sctx.Done(); _ = pc.SetDeadline(time.Now()) }()
	errc := make(chan error, 2)
	var lastRx atomic.Int64
	lastRx.Store(time.Now().UnixNano())

	go func() { // writer
		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()
		for {
			var b []byte
			select {
			case <-sctx.Done():
				errc <- sctx.Err()
				return
			case f := <-c.out:
				if f[0] != frameSend {
					continue
				}
				b = make([]byte, 1+udpSessionLen+len(f)-1)
				b[0] = udpSend
				copy(b[1:], sess[:])
				copy(b[1+udpSessionLen:], f[1:])
				c.TxBytes.Add(uint64(len(f) - 1 - keyLen))
			case <-ping.C:
				if time.Since(time.Unix(0, lastRx.Load())) > 70*time.Second {
					errc <- errors.New("relay stopped answering")
					return
				}
				b = append([]byte{udpPing}, sess[:]...)
			}
			if _, err := pc.WriteTo(b, raddr); err != nil {
				errc <- err
				return
			}
		}
	}()
	go func() { // reader
		buf := make([]byte, 2048)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				errc <- err
				return
			}
			if n == 0 {
				continue
			}
			lastRx.Store(time.Now().UnixNano())
			switch buf[0] {
			case udpRecv:
				if n <= 1+keyLen {
					continue
				}
				var src Key
				copy(src[:], buf[1:1+keyLen])
				data := append([]byte(nil), buf[1+keyLen:n]...)
				c.RxBytes.Add(uint64(len(data)))
				select {
				case c.recv <- Packet{Src: src, Data: data}:
				default:
				}
			case udpPeerGone:
				if n >= 1+keyLen {
					var k Key
					copy(k[:], buf[1:1+keyLen])
					c.peerGone(k)
				}
			case udpBadSess:
				errc <- errors.New("relay forgot this session")
				return
			}
		}
	}()
	select {
	case err = <-errc:
	case <-ctx.Done():
		err = ctx.Err()
	}
	return err
}

// ---------- WebSocket transport ----------

func (c *Client) wsSession(ctx context.Context) error {
	tok := c.cfg.Token()
	if tok == "" {
		return errors.New("no session token")
	}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	ws, _, err := websocket.Dial(dctx, c.cfg.URL, &websocket.DialOptions{
		HTTPClient:      c.cfg.HTTP,
		HTTPHeader:      http.Header{"Authorization": {"Bearer " + tok}},
		CompressionMode: websocket.CompressionDisabled,
	})
	cancel()
	if err != nil {
		return err
	}
	ws.SetReadLimit(1 + keyLen + 65535)
	defer ws.CloseNow()
	c.transport.Store("websocket")
	c.Connected.Store(true)
	c.log.Debug("relay connected", "url", c.cfg.URL, "transport", "websocket")

	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	errc := make(chan error, 3)
	go func() { errc <- c.wsWriter(sctx, ws) }()
	go func() { errc <- c.wsReader(sctx, ws) }()
	if c.cfg.UDPAddr != "" && !c.cfg.BlockUDP {
		go c.probeUDP(sctx, errc)
	}
	select {
	case err = <-errc:
	case <-ctx.Done():
		err = ctx.Err()
	}
	return err
}

// probeUDP periodically checks whether UDP started working; if so it ends the WebSocket
// session so Run reconnects over UDP.
func (c *Client) probeUDP(ctx context.Context, errc chan<- error) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if time.Now().UnixNano() < c.udpRetryAt.Load() {
			continue
		}
		pc, _, _, err := c.udpHandshake(ctx)
		if err == nil {
			pc.Close()
			c.udpRetryAt.Store(0)
			errc <- errors.New("switching to the UDP relay")
			return
		}
		c.udpRetryAt.Store(time.Now().Add(udpRetryAfter).UnixNano())
	}
}

func (c *Client) wsReader(ctx context.Context, ws *websocket.Conn) error {
	for {
		rctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		typ, msg, err := ws.Read(rctx)
		cancel()
		if err != nil {
			return err
		}
		if typ != websocket.MessageBinary || len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case frameRecv:
			if len(msg) <= 1+keyLen {
				continue
			}
			var src Key
			copy(src[:], msg[1:1+keyLen])
			c.RxBytes.Add(uint64(len(msg) - 1 - keyLen))
			select {
			case c.recv <- Packet{Src: src, Data: msg[1+keyLen:]}:
			default: // receiver is behind; drop
			}
		case framePing:
			select {
			case c.out <- []byte{framePong}:
			default:
			}
		case framePeerGone:
			if len(msg) >= 1+keyLen {
				var k Key
				copy(k[:], msg[1:1+keyLen])
				c.peerGone(k)
			}
		}
	}
}

func (c *Client) wsWriter(ctx context.Context, ws *websocket.Conn) error {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		var b []byte
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b = <-c.out:
		case <-ping.C:
			b = []byte{framePing}
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := ws.Write(wctx, websocket.MessageBinary, b)
		cancel()
		if err != nil {
			return err
		}
		if b[0] == frameSend {
			c.TxBytes.Add(uint64(len(b) - 1 - keyLen))
		}
	}
}
