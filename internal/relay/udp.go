package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// UDP relay datagrams. A client first says hello with its device session token; the
// server answers with a random session id that every later datagram must carry, so
// nobody can inject packets for someone else's session without seeing it.
//
//	client -> server  0x20 | token length (2) | token                    hello
//	server -> client  0x21 | session (8)                                 hello accepted
//	client -> server  0x22 | session (8) | dst wg public key (32) | pkt  send
//	server -> client  0x23 | src wg public key (32) | pkt                receive
//	client -> server  0x24 | session (8)                                 ping (keeps NAT open)
//	server -> client  0x25                                               pong
//	server -> client  0x26 | dst wg public key (32)                      peer not connected here
//	server -> client  0x27                                               unknown session: say hello again
const (
	UDPHello     = 0x20
	UDPHelloOK   = 0x21
	UDPSend      = 0x22
	UDPRecv      = 0x23
	UDPPing      = 0x24
	UDPPong      = 0x25
	UDPPeerGone  = 0x26
	UDPBadSessio = 0x27

	udpSessionLen  = 8
	udpMaxDatagram = 2048
	udpIdle        = 90 * time.Second
)

// udpSession is a UDP client.
type udpSession struct {
	id      Identity
	srv     *UDPServer
	addr    netip.AddrPort
	session [udpSessionLen]byte
	tokHash [32]byte // SHA-256 of the token the session was opened with
	lim     *rate.Limiter

	mu   sync.Mutex
	last time.Time
}

func (u *udpSession) identity() Identity     { return u.id }
func (u *udpSession) limiter() *rate.Limiter { return u.lim }

func (u *udpSession) deliver(src Key, pkt []byte) bool {
	b := make([]byte, 1+keyLen+len(pkt))
	b[0] = UDPRecv
	copy(b[1:], src[:])
	copy(b[1+keyLen:], pkt)
	return u.srv.write(b, u.addr)
}

func (u *udpSession) gone(dst Key) {
	b := make([]byte, 1+keyLen)
	b[0] = UDPPeerGone
	copy(b[1:], dst[:])
	u.srv.write(b, u.addr)
}

func (u *udpSession) closeWith(string) {
	u.srv.drop(u)
}

// UDPServer serves the UDP relay on behalf of a Server (sharing its client registry).
type UDPServer struct {
	s  *Server
	pc net.PacketConn

	mu     sync.Mutex
	byAddr map[netip.AddrPort]*udpSession

	// Token checks hit the database, so they run off the read loop with bounded
	// concurrency; a flood of bogus hellos can't stall relayed traffic.
	authSem   chan struct{}
	helloRate *rate.Limiter
}

// NewUDP creates the UDP side of the relay.
func (s *Server) NewUDP() *UDPServer {
	return &UDPServer{
		s:         s,
		byAddr:    map[netip.AddrPort]*udpSession{},
		authSem:   make(chan struct{}, 32),
		helloRate: rate.NewLimiter(500, 1000), // new sessions per second across all clients
	}
}

func (u *UDPServer) write(b []byte, to netip.AddrPort) bool {
	_, err := u.pc.(*net.UDPConn).WriteToUDPAddrPort(b, to)
	return err == nil
}

func (u *UDPServer) drop(sess *udpSession) {
	u.mu.Lock()
	removed := u.byAddr[sess.addr] == sess
	if removed {
		delete(u.byAddr, sess.addr)
		u.s.UDPSessions.Add(-1)
	}
	u.mu.Unlock()
	u.s.unregister(sess)
}

// Serve reads datagrams until ctx ends.
func (u *UDPServer) Serve(ctx context.Context, listen string) error {
	pc, err := net.ListenPacket("udp", listen)
	if err != nil {
		return err
	}
	u.pc = pc
	go func() { <-ctx.Done(); pc.Close() }()
	go u.sweep(ctx)
	uc := pc.(*net.UDPConn)
	buf := make([]byte, udpMaxDatagram)
	for {
		n, from, err := uc.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		u.handle(ctx, buf[:n], from)
	}
}

// sweep drops sessions that have been silent for too long.
func (u *UDPServer) sweep(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var dead []*udpSession
		u.mu.Lock()
		for _, sess := range u.byAddr {
			sess.mu.Lock()
			if time.Since(sess.last) > udpIdle {
				dead = append(dead, sess)
			}
			sess.mu.Unlock()
		}
		u.mu.Unlock()
		for _, sess := range dead {
			u.drop(sess)
		}
	}
}

func (u *UDPServer) handle(ctx context.Context, b []byte, from netip.AddrPort) {
	if len(b) == 0 {
		return
	}
	if b[0] == UDPHello {
		u.hello(ctx, append([]byte(nil), b...), from) // buf is reused by the read loop
		return
	}
	u.mu.Lock()
	sess := u.byAddr[from]
	u.mu.Unlock()
	if sess == nil || len(b) < 1+udpSessionLen || subtle.ConstantTimeCompare(b[1:1+udpSessionLen], sess.session[:]) != 1 {
		u.write([]byte{UDPBadSessio}, from) // the client lost its session (server restart, expiry)
		return
	}
	sess.mu.Lock()
	sess.last = time.Now()
	sess.mu.Unlock()
	switch b[0] {
	case UDPPing:
		u.write([]byte{UDPPong}, from)
	case UDPSend:
		if len(b) < 1+udpSessionLen+keyLen+1 {
			return
		}
		var dst Key
		copy(dst[:], b[1+udpSessionLen:1+udpSessionLen+keyLen])
		u.s.forward(sess, dst, b[1+udpSessionLen+keyLen:])
	}
}

func (u *UDPServer) hello(ctx context.Context, b []byte, from netip.AddrPort) {
	if len(b) < 3 {
		return
	}
	n := int(binary.BigEndian.Uint16(b[1:3]))
	if n == 0 || n > 1024 || len(b) != 3+n {
		return
	}
	token := b[3:]
	th := sha256.Sum256(token)
	// A repeated hello from the same address with the same token (the client retries)
	// keeps its session. A different token is a new sign-in and is checked below.
	u.mu.Lock()
	if cur := u.byAddr[from]; cur != nil && subtle.ConstantTimeCompare(cur.tokHash[:], th[:]) == 1 {
		cur.mu.Lock()
		cur.last = time.Now()
		cur.mu.Unlock()
		resp := append([]byte{UDPHelloOK}, cur.session[:]...)
		u.mu.Unlock()
		u.write(resp, from)
		return
	}
	u.mu.Unlock()
	if !u.helloRate.Allow() {
		return // overloaded: the client retries
	}
	select {
	case u.authSem <- struct{}{}:
	default:
		return // too many checks in flight: the client retries
	}
	go func() {
		defer func() { <-u.authSem }()
		actx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		id, err := u.s.authn(actx, string(token))
		if err != nil {
			return // no answer: don't help token guessing
		}
		sess := &udpSession{id: id, srv: u, addr: from, tokHash: th, last: time.Now(), lim: u.s.newLimiter()}
		if _, err := rand.Read(sess.session[:]); err != nil {
			return
		}
		u.mu.Lock()
		old := u.byAddr[from]
		u.byAddr[from] = sess
		if old == nil {
			u.s.UDPSessions.Add(1)
		}
		u.mu.Unlock()
		if old != nil {
			u.s.unregister(old) // same address, new sign-in: the old identity is gone
		}
		u.s.register(sess)
		u.write(append([]byte{UDPHelloOK}, sess.session[:]...), from)
	}()
}
