// Package relay forwards already-encrypted WireGuard packets between peers
// that cannot connect directly. It runs over WebSocket (TLS on port 443), so it
// works on networks that block UDP, and over UDP (see udp.go), which is faster.
// A WebSocket client and a UDP client can talk to each other. The relay cannot
// decrypt traffic.
//
// WebSocket wire format (binary messages):
//
//	client -> server  0x01 | dst wg public key (32) | packet
//	server -> client  0x02 | src wg public key (32) | packet
//	either way        0x03 ping | 0x04 pong
//	server -> client  0x05 | dst wg public key (32)   (peer not connected here)
package relay

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"
)

const (
	FrameSend     = 0x01
	FrameRecv     = 0x02
	FramePing     = 0x03
	FramePong     = 0x04
	FramePeerGone = 0x05

	keyLen     = 32
	maxPacket  = 65535
	sendQueue  = 512
	idleTimout = 2 * time.Minute
)

type Key [keyLen]byte

// Identity of an authenticated relay client.
type Identity struct {
	DeviceID string
	Key      Key
}

// Authenticator validates a bearer token and returns the client identity.
type Authenticator func(ctx context.Context, token string) (Identity, error)

// Authorizer reports whether src may send to dst.
type Authorizer func(srcDeviceID string, dst Key) bool

// endpoint is one connected client, whatever its transport.
type endpoint interface {
	identity() Identity
	limiter() *rate.Limiter
	// deliver hands a packet from src to this client; false when it was dropped.
	deliver(src Key, pkt []byte) bool
	// gone tells this client that dst isn't connected to this relay.
	gone(dst Key)
	// closeWith ends the session.
	closeWith(reason string)
}

// Remote forwards packets to clients connected to another server instance (cluster mode).
type Remote interface {
	// Presence tells the other instances that a client connected here (up) or left.
	Presence(key Key, up bool)
	// Send forwards pkt from src to the instance that holds dst; false when nobody does.
	Send(dst, src Key, pkt []byte) bool
}

type Server struct {
	remote    Remote
	log       *slog.Logger
	authn     Authenticator
	authz     Authorizer
	rateBytes int

	mu    sync.RWMutex
	conns map[Key]endpoint

	Connections    atomic.Int64
	UDPSessions    atomic.Int64 // active UDP relay sessions (not included in Connections)
	BytesRelayed   atomic.Uint64
	PacketsDropped atomic.Uint64
}

// conn is a WebSocket client.
type conn struct {
	id   Identity
	ws   *websocket.Conn
	out  chan []byte
	lim  *rate.Limiter
	done chan struct{}
}

func (c *conn) identity() Identity      { return c.id }
func (c *conn) limiter() *rate.Limiter  { return c.lim }
func (c *conn) closeWith(reason string) { c.ws.Close(websocket.StatusPolicyViolation, reason) }

func (c *conn) deliver(src Key, pkt []byte) bool {
	frame := make([]byte, 1+keyLen+len(pkt))
	frame[0] = FrameRecv
	copy(frame[1:], src[:])
	copy(frame[1+keyLen:], pkt)
	return c.enqueue(frame)
}

func (c *conn) gone(dst Key) {
	f := make([]byte, 1+keyLen)
	f[0] = FramePeerGone
	copy(f[1:], dst[:])
	c.enqueue(f)
}

func New(log *slog.Logger, authn Authenticator, authz Authorizer, rateBytes int) *Server {
	return &Server{log: log, authn: authn, authz: authz, rateBytes: rateBytes, conns: map[Key]endpoint{}}
}

// SetRemote connects the relay to the other cluster instances (call before serving).
func (s *Server) SetRemote(r Remote) { s.remote = r }

// DeliverRemote hands a packet forwarded by another instance to the local client for dst.
func (s *Server) DeliverRemote(dst, src Key, pkt []byte) {
	s.mu.RLock()
	d := s.conns[dst]
	s.mu.RUnlock()
	if d != nil && d.deliver(src, pkt) {
		s.BytesRelayed.Add(uint64(len(pkt)))
	} else {
		s.PacketsDropped.Add(1)
	}
}

// register makes e the connection for its key, replacing (and closing) an older one.
func (s *Server) register(e endpoint) {
	k := e.identity().Key
	s.mu.Lock()
	old := s.conns[k]
	s.conns[k] = e
	s.mu.Unlock()
	if old != nil {
		old.closeWith("replaced by newer connection")
	}
	s.Connections.Add(1)
	if s.remote != nil {
		s.remote.Presence(k, true)
	}
}

// unregister removes e if it is still the connection for its key.
func (s *Server) unregister(e endpoint) {
	k := e.identity().Key
	s.mu.Lock()
	gone := s.conns[k] == e
	if gone {
		delete(s.conns, k)
	}
	s.mu.Unlock()
	s.Connections.Add(-1)
	if gone && s.remote != nil {
		s.remote.Presence(k, false)
	}
}

func (s *Server) newLimiter() *rate.Limiter {
	if s.rateBytes > 0 {
		return rate.NewLimiter(rate.Limit(s.rateBytes), s.rateBytes)
	}
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, err := s.authn(r.Context(), token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxPacket + 1 + keyLen)
	c := &conn{id: id, ws: ws, out: make(chan []byte, sendQueue), done: make(chan struct{}), lim: s.newLimiter()}
	s.register(c)

	ctx, cancel := context.WithCancel(r.Context())
	defer func() {
		cancel()
		s.unregister(c)
		close(c.done)
		ws.CloseNow()
	}()
	go s.writer(ctx, c)
	s.reader(ctx, c)
}

func (s *Server) reader(ctx context.Context, c *conn) {
	for {
		rctx, cancel := context.WithTimeout(ctx, idleTimout)
		typ, msg, err := c.ws.Read(rctx)
		cancel()
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary || len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case FramePing:
			c.enqueue([]byte{FramePong})
		case FrameSend:
			if len(msg) < 1+keyLen+1 {
				continue
			}
			var dst Key
			copy(dst[:], msg[1:1+keyLen])
			s.forward(c, dst, msg[1+keyLen:])
		}
	}
}

// forward relays pkt from src to dst, if both are allowed to talk.
func (s *Server) forward(src endpoint, dst Key, pkt []byte) {
	if lim := src.limiter(); lim != nil && !lim.AllowN(time.Now(), len(pkt)) {
		s.PacketsDropped.Add(1)
		return
	}
	if !s.authz(src.identity().DeviceID, dst) {
		s.PacketsDropped.Add(1)
		return
	}
	s.mu.RLock()
	d := s.conns[dst]
	s.mu.RUnlock()
	if d == nil {
		if s.remote != nil && s.remote.Send(dst, src.identity().Key, pkt) {
			s.BytesRelayed.Add(uint64(len(pkt)))
			return
		}
		src.gone(dst)
		return
	}
	if d.deliver(src.identity().Key, pkt) {
		s.BytesRelayed.Add(uint64(len(pkt)))
	} else {
		s.PacketsDropped.Add(1)
	}
}

func (c *conn) enqueue(b []byte) bool {
	select {
	case c.out <- b:
		return true
	case <-c.done:
		return false
	default:
		return false // queue full: drop, like UDP
	}
}

func (s *Server) writer(ctx context.Context, c *conn) {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.ws.Write(wctx, websocket.MessageBinary, b)
			cancel()
			if err != nil {
				c.ws.Close(websocket.StatusGoingAway, "write failed")
				return
			}
		case <-ping.C:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.ws.Write(wctx, websocket.MessageBinary, []byte{FramePing})
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// Disconnect closes the relay session of a device key (e.g. on revocation).
func (s *Server) Disconnect(k Key) {
	s.mu.RLock()
	e := s.conns[k]
	s.mu.RUnlock()
	if e != nil {
		e.closeWith("revoked")
	}
}

// ParseKey decodes a base64 WireGuard key into a relay Key.
func ParseKey(b []byte) (Key, error) {
	var k Key
	if len(b) != keyLen {
		return k, errors.New("bad key length")
	}
	copy(k[:], b)
	return k, nil
}
