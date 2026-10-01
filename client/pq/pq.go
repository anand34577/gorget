// Package pq gives each pair of Gorget devices a post-quantum pre-shared key.
//
// WireGuard's handshake uses Curve25519, which a future quantum computer could
// break (and which can be recorded today and broken later). WireGuard mixes in an
// optional 32-byte pre-shared key; if that key comes from a quantum-safe exchange the
// tunnel stays safe either way. Devices run an ML-KEM-768 key exchange through the
// server's signal channel (sealed end to end between the devices) and use the result,
// bound to both WireGuard keys, as the pre-shared key.
//
// Both devices must switch to a new key at (nearly) the same moment or their WireGuard
// handshakes fail, so the exchange is a four-step commit:
//
//	initiator -> Init(gen, encapsulation key)
//	responder -> Resp(ciphertext)            responder only stages the key
//	initiator -> Commit(gen)                 initiator holds the key (resent until acked)
//	responder -> Ack                         responder applies the key and confirms
//	initiator applies the key on the Ack
//
// A lost message leaves both sides on their old key. Generations order competing
// exchanges so both sides always end up with the highest one.
//
// The exchange happens once per device pair and the key is stored on disk; it is
// redone when either device's key changes. Older clients simply don't answer and
// keep using plain WireGuard.
package pq

import (
	"bytes"
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/sha256"
	"sync"
	"time"

	"github.com/anand34577/gorget/client/disco"
)

type Key = [32]byte

// Entry is a stored key for a peer.
type Entry struct {
	Peer Key
	Gen  uint64
}

// Callbacks connect the manager to the rest of the client.
type Callbacks struct {
	// Send delivers a message to a peer (identified by its WireGuard key).
	Send func(peer Key, m disco.Message)
	// Apply installs the pre-shared key for a peer and persists it with its generation.
	Apply func(peer Key, psk *Key, gen uint64)
}

// Timing (variables so tests can speed them up).
var (
	pendingTimeout = 30 * time.Second
	commitRetry    = 2 * time.Second
	commitTries    = 10
	firstBackoff   = 30 * time.Second
	maxBackoff     = 10 * time.Minute
	// A device with the larger key waits this long for the other side to start first.
	followerDelay = 45 * time.Second
)

type initiated struct {
	dk      *mlkem.DecapsulationKey768
	tx      disco.TxID
	gen     uint64
	at      time.Time
	psk     *Key // set once the Resp arrived
	commits int
}

type staged struct {
	psk Key
	gen uint64
	at  time.Time
}

type peerState struct {
	gen     uint64 // generation of the applied key (0 = none)
	out     *initiated
	staged  map[disco.TxID]*staged
	lastTx  disco.TxID // last exchange we applied as responder (to re-ack a lost Ack)
	backoff time.Duration
	nextTry time.Time
}

type Manager struct {
	self Key // our WireGuard public key
	cb   Callbacks

	mu    sync.Mutex
	peers map[Key]*peerState
}

// New creates a manager. have lists peers that already have a stored key.
func New(self Key, cb Callbacks, have []Entry) *Manager {
	m := &Manager{self: self, cb: cb, peers: map[Key]*peerState{}}
	for _, e := range have {
		m.peer(e.Peer).gen = e.Gen
	}
	return m
}

func (m *Manager) peer(k Key) *peerState {
	p := m.peers[k]
	if p == nil {
		p = &peerState{staged: map[disco.TxID]*staged{}}
		m.peers[k] = p
	}
	return p
}

// Has reports whether the peer is protected by a post-quantum key.
func (m *Manager) Has(peer Key) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.peers[peer]
	return p != nil && p.gen > 0
}

// Ensure starts an exchange with the peer when it has no key yet. Call it regularly
// for every online peer; it rate-limits itself with exponential backoff.
func (m *Manager) Ensure(peer Key) {
	m.mu.Lock()
	p := m.peer(peer)
	now := time.Now()
	if p.gen > 0 {
		m.mu.Unlock()
		return
	}
	if p.nextTry.IsZero() && bytes.Compare(m.self[:], peer[:]) > 0 {
		// The device with the smaller key leads; give it time to start before we do.
		p.nextTry = now.Add(followerDelay)
	}
	if now.Before(p.nextTry) || (p.out != nil && now.Sub(p.out.at) < pendingTimeout) {
		m.mu.Unlock()
		return
	}
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		m.mu.Unlock()
		return
	}
	o := &initiated{dk: dk, tx: disco.NewTxID(), gen: uint64(now.UnixNano()), at: now}
	p.out = o
	switch {
	case p.backoff == 0:
		p.backoff = firstBackoff
	case p.backoff*2 > maxBackoff:
		p.backoff = maxBackoff
	default:
		p.backoff *= 2
	}
	p.nextTry = now.Add(p.backoff)
	init := &disco.PQInit{TxID: o.tx, Gen: o.gen, EncapKey: dk.EncapsulationKey().Bytes()}
	m.mu.Unlock()
	m.cb.Send(peer, init)
}

// Forget drops everything known about a peer (it left the network or changed keys).
func (m *Manager) Forget(peer Key) {
	m.mu.Lock()
	delete(m.peers, peer)
	m.mu.Unlock()
}

// Handle processes a message from a peer.
func (m *Manager) Handle(peer Key, msg disco.Message) {
	switch x := msg.(type) {
	case *disco.PQInit:
		m.handleInit(peer, x)
	case *disco.PQResp:
		m.handleResp(peer, x)
	case *disco.PQCommit:
		m.handleCommit(peer, x)
	case *disco.PQAck:
		m.handleAck(peer, x)
	}
}

// Responder: stage a key and answer.
func (m *Manager) handleInit(peer Key, in *disco.PQInit) {
	ek, err := mlkem.NewEncapsulationKey768(in.EncapKey)
	if err != nil || in.Gen == 0 {
		return
	}
	m.mu.Lock()
	p := m.peer(peer)
	// Both started at once: the smaller key leads, so it ignores the other's start.
	if p.out != nil && time.Since(p.out.at) < pendingTimeout && bytes.Compare(m.self[:], peer[:]) < 0 {
		m.mu.Unlock()
		return
	}
	now := time.Now()
	for tx, s := range p.staged {
		if now.Sub(s.at) > 2*pendingTimeout {
			delete(p.staged, tx)
		}
	}
	if len(p.staged) >= 8 {
		m.mu.Unlock()
		return
	}
	ss, ct := ek.Encapsulate()
	p.staged[in.TxID] = &staged{psk: derive(ss, peer, m.self), gen: in.Gen, at: now} // the initiator is the peer
	m.mu.Unlock()
	m.cb.Send(peer, &disco.PQResp{TxID: in.TxID, Ciphertext: ct})
}

// Initiator: got the ciphertext; tell the responder we hold the key.
func (m *Manager) handleResp(peer Key, r *disco.PQResp) {
	m.mu.Lock()
	p := m.peers[peer]
	if p == nil || p.out == nil || p.out.tx != r.TxID || p.out.psk != nil {
		m.mu.Unlock()
		return
	}
	o := p.out
	m.mu.Unlock()
	ss, err := o.dk.Decapsulate(r.Ciphertext)
	if err != nil {
		return
	}
	psk := derive(ss, m.self, peer) // we are the initiator
	m.mu.Lock()
	if p.out != o {
		m.mu.Unlock()
		return
	}
	o.psk = &psk
	m.mu.Unlock()
	m.sendCommit(peer, o)
}

// sendCommit sends Commit and keeps resending until the Ack arrives (or we give up).
func (m *Manager) sendCommit(peer Key, o *initiated) {
	m.mu.Lock()
	p := m.peers[peer]
	if p == nil || p.out != o || o.commits >= commitTries {
		if p != nil && p.out == o {
			p.out = nil // gave up: nothing was applied here, so both sides stay on the old key
		}
		m.mu.Unlock()
		return
	}
	o.commits++
	m.mu.Unlock()
	m.cb.Send(peer, &disco.PQCommit{TxID: o.tx, Gen: o.gen})
	time.AfterFunc(commitRetry, func() { m.sendCommit(peer, o) })
}

// Responder: the initiator holds the key, apply it.
func (m *Manager) handleCommit(peer Key, c *disco.PQCommit) {
	m.mu.Lock()
	p := m.peers[peer]
	if p == nil {
		m.mu.Unlock()
		return
	}
	if p.lastTx == c.TxID && p.gen == c.Gen {
		m.mu.Unlock()
		m.cb.Send(peer, &disco.PQAck{TxID: c.TxID}) // our Ack was lost
		return
	}
	s := p.staged[c.TxID]
	if s == nil || s.gen != c.Gen {
		m.mu.Unlock()
		return
	}
	delete(p.staged, c.TxID)
	if s.gen <= p.gen {
		m.mu.Unlock()
		return // an exchange with a higher generation already won; let this one lapse
	}
	p.gen, p.lastTx, p.backoff = s.gen, c.TxID, 0
	psk, gen := s.psk, s.gen
	m.mu.Unlock()
	m.cb.Apply(peer, &psk, gen)
	m.cb.Send(peer, &disco.PQAck{TxID: c.TxID})
}

// Initiator: the responder applied the key, now we do too.
func (m *Manager) handleAck(peer Key, a *disco.PQAck) {
	m.mu.Lock()
	p := m.peers[peer]
	if p == nil || p.out == nil || p.out.tx != a.TxID || p.out.psk == nil {
		m.mu.Unlock()
		return
	}
	o := p.out
	p.out = nil
	if o.gen <= p.gen {
		m.mu.Unlock()
		return
	}
	p.gen, p.backoff = o.gen, 0
	m.mu.Unlock()
	m.cb.Apply(peer, o.psk, o.gen)
}

// derive binds the shared secret to both identities (initiator first).
func derive(ss []byte, initiator, responder Key) Key {
	info := append(append([]byte("gorget-pq-psk-v1|"), initiator[:]...), responder[:]...)
	out, err := hkdf.Key(sha256.New, ss, nil, string(info), 32)
	if err != nil {
		panic(err) // only fails for absurd output lengths
	}
	var k Key
	copy(k[:], out)
	return k
}
