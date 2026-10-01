// Package disco implements Gorget's peer discovery messages: small, encrypted
// packets that peers exchange directly over UDP (and via the server's signal
// channel) to find a working path through NATs.
//
// Wire format:
//
//	magic (6) | sender disco public key (32) | nonce (24) | NaCl box(payload)
//
// Payload: type (1) | body. Packets are authenticated by the box, so only the
// holder of a peer's disco private key can produce them.
package disco

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"

	"golang.org/x/crypto/nacl/box"
)

// Magic prefixes every disco packet so it can be told apart from WireGuard traffic
// (WireGuard message types are 1-4 followed by three zero bytes).
var Magic = []byte{'G', 'R', 'G', 'T', 0xd1, 0x5c}

const (
	keyLen    = 32
	nonceLen  = 24
	headerLen = 6 + keyLen + nonceLen
	TxIDLen   = 12
)

type Key = [keyLen]byte

// Message types.
const (
	TypePing        byte = 1
	TypePong        byte = 2
	TypeCallMeMaybe byte = 3
	TypePQInit      byte = 4
	TypePQResp      byte = 5
	TypePQCommit    byte = 6
	TypePQAck       byte = 7
)

// ML-KEM-768 sizes (FIPS 203). Messages carrying them travel through the server's signal
// channel, never over direct UDP, because they are larger than a typical path MTU.
const (
	PQEncapKeySize   = 1184
	PQCiphertextSize = 1088
)

type TxID [TxIDLen]byte

func NewTxID() TxID {
	var t TxID
	_, _ = rand.Read(t[:])
	return t
}

// Ping asks the receiver to reply with a Pong. NodeKey is the sender's WireGuard key.
type Ping struct {
	TxID    TxID
	NodeKey Key
}

// Pong answers a Ping and tells the pinger which address the ping came from.
type Pong struct {
	TxID TxID
	Src  netip.AddrPort
}

// CallMeMaybe (sent via the server) asks the receiver to start pinging the listed endpoints,
// so both sides punch holes in their NATs at the same time.
type CallMeMaybe struct {
	Endpoints []netip.AddrPort
}

// PQInit starts a post-quantum key exchange: the sender's ML-KEM-768 encapsulation key.
type PQInit struct {
	TxID TxID
	// Gen orders exchanges between the same two devices: the highest generation wins.
	Gen      uint64
	EncapKey []byte
}

// PQResp answers a PQInit with the KEM ciphertext for the initiator's key.
type PQResp struct {
	TxID       TxID
	Ciphertext []byte
}

// PQCommit tells the responder that the initiator holds the key: apply it now.
type PQCommit struct {
	TxID TxID
	Gen  uint64
}

// PQAck confirms that the responder applied the committed key.
type PQAck struct{ TxID TxID }

// Message is one of Ping, Pong, CallMeMaybe or a PQ* key exchange message.
type Message interface{ marshal() []byte }

func (p *Ping) marshal() []byte {
	b := make([]byte, 1+TxIDLen+keyLen)
	b[0] = TypePing
	copy(b[1:], p.TxID[:])
	copy(b[1+TxIDLen:], p.NodeKey[:])
	return b
}

func (p *Pong) marshal() []byte {
	b := make([]byte, 1+TxIDLen+18)
	b[0] = TypePong
	copy(b[1:], p.TxID[:])
	putAddrPort(b[1+TxIDLen:], p.Src)
	return b
}

func (c *CallMeMaybe) marshal() []byte {
	n := len(c.Endpoints)
	if n > 32 {
		n = 32
	}
	b := make([]byte, 1+n*18)
	b[0] = TypeCallMeMaybe
	for i := 0; i < n; i++ {
		putAddrPort(b[1+i*18:], c.Endpoints[i])
	}
	return b
}

func (p *PQInit) marshal() []byte {
	b := make([]byte, 1+TxIDLen+8+len(p.EncapKey))
	b[0] = TypePQInit
	copy(b[1:], p.TxID[:])
	binary.BigEndian.PutUint64(b[1+TxIDLen:], p.Gen)
	copy(b[1+TxIDLen+8:], p.EncapKey)
	return b
}

func (p *PQCommit) marshal() []byte {
	b := make([]byte, 1+TxIDLen+8)
	b[0] = TypePQCommit
	copy(b[1:], p.TxID[:])
	binary.BigEndian.PutUint64(b[1+TxIDLen:], p.Gen)
	return b
}

func (p *PQAck) marshal() []byte {
	b := make([]byte, 1+TxIDLen)
	b[0] = TypePQAck
	copy(b[1:], p.TxID[:])
	return b
}

func (p *PQResp) marshal() []byte {
	b := make([]byte, 1+TxIDLen+len(p.Ciphertext))
	b[0] = TypePQResp
	copy(b[1:], p.TxID[:])
	copy(b[1+TxIDLen:], p.Ciphertext)
	return b
}

func putAddrPort(b []byte, ap netip.AddrPort) {
	a := ap.Addr().As16()
	copy(b, a[:])
	binary.BigEndian.PutUint16(b[16:], ap.Port())
}

func getAddrPort(b []byte) netip.AddrPort {
	var a [16]byte
	copy(a[:], b[:16])
	return netip.AddrPortFrom(netip.AddrFrom16(a).Unmap(), binary.BigEndian.Uint16(b[16:18]))
}

// The X25519 step of NaCl box costs far more than the encryption itself, and each
// peer pair always derives the same key, so derived keys are cached. Only keys that
// authenticated a real message (or that we sent with) are stored, so packets with
// made-up sender keys can't fill the cache.
var (
	sharedMu sync.Mutex
	shared   = map[[2 * keyLen]byte]*[keyLen]byte{}
)

const maxShared = 8192

func sharedKey(priv, peer *Key) (*[keyLen]byte, [2 * keyLen]byte) {
	var id [2 * keyLen]byte
	copy(id[:keyLen], priv[:])
	copy(id[keyLen:], peer[:])
	sharedMu.Lock()
	k := shared[id]
	sharedMu.Unlock()
	if k != nil {
		return k, id
	}
	k = new([keyLen]byte)
	box.Precompute(k, (*[keyLen]byte)(peer), (*[keyLen]byte)(priv))
	return k, id
}

func rememberShared(id [2 * keyLen]byte, k *[keyLen]byte) {
	sharedMu.Lock()
	if len(shared) >= maxShared {
		clear(shared) // rare: thousands of peers; they are re-derived on demand
	}
	shared[id] = k
	sharedMu.Unlock()
}

// Seal encrypts m from sender (our disco key pair) to receiver's disco public key.
func Seal(m Message, senderPub, senderPriv *Key, receiverPub *Key) []byte {
	var nonce [nonceLen]byte
	_, _ = rand.Read(nonce[:])
	body := m.marshal()
	out := make([]byte, 0, headerLen+len(body)+box.Overhead)
	out = append(out, Magic...)
	out = append(out, senderPub[:]...)
	out = append(out, nonce[:]...)
	k, id := sharedKey(senderPriv, receiverPub)
	rememberShared(id, k)
	return box.SealAfterPrecomputation(out, body, &nonce, k)
}

// IsDisco reports whether a UDP payload looks like a disco packet.
func IsDisco(b []byte) bool {
	return len(b) >= headerLen+box.Overhead+1 && string(b[:len(Magic)]) == string(Magic)
}

// SenderKey returns the sender's disco public key from a packet header.
func SenderKey(b []byte) (Key, bool) {
	var k Key
	if !IsDisco(b) {
		return k, false
	}
	copy(k[:], b[len(Magic):len(Magic)+keyLen])
	return k, true
}

var ErrInvalid = errors.New("invalid disco message")

// Open decrypts a packet with our private key. The sender key must be known to the caller.
func Open(b []byte, ourPriv *Key) (Message, Key, error) {
	sender, ok := SenderKey(b)
	if !ok {
		return nil, sender, ErrInvalid
	}
	var nonce [nonceLen]byte
	copy(nonce[:], b[len(Magic)+keyLen:headerLen])
	k, id := sharedKey(ourPriv, &sender)
	pt, ok := box.OpenAfterPrecomputation(nil, b[headerLen:], &nonce, k)
	if !ok || len(pt) < 1 {
		return nil, sender, ErrInvalid
	}
	rememberShared(id, k)
	body := pt[1:]
	switch pt[0] {
	case TypePing:
		if len(body) < TxIDLen+keyLen {
			return nil, sender, ErrInvalid
		}
		p := &Ping{}
		copy(p.TxID[:], body)
		copy(p.NodeKey[:], body[TxIDLen:])
		return p, sender, nil
	case TypePong:
		if len(body) < TxIDLen+18 {
			return nil, sender, ErrInvalid
		}
		p := &Pong{}
		copy(p.TxID[:], body)
		p.Src = getAddrPort(body[TxIDLen:])
		return p, sender, nil
	case TypeCallMeMaybe:
		c := &CallMeMaybe{}
		for i := 0; i+18 <= len(body); i += 18 {
			if ap := getAddrPort(body[i:]); ap.IsValid() && ap.Port() != 0 {
				c.Endpoints = append(c.Endpoints, ap)
			}
		}
		return c, sender, nil
	case TypePQInit:
		if len(body) != TxIDLen+8+PQEncapKeySize {
			return nil, sender, ErrInvalid
		}
		p := &PQInit{Gen: binary.BigEndian.Uint64(body[TxIDLen:]), EncapKey: append([]byte(nil), body[TxIDLen+8:]...)}
		copy(p.TxID[:], body)
		return p, sender, nil
	case TypePQCommit:
		if len(body) != TxIDLen+8 {
			return nil, sender, ErrInvalid
		}
		p := &PQCommit{Gen: binary.BigEndian.Uint64(body[TxIDLen:])}
		copy(p.TxID[:], body)
		return p, sender, nil
	case TypePQAck:
		if len(body) != TxIDLen {
			return nil, sender, ErrInvalid
		}
		p := &PQAck{}
		copy(p.TxID[:], body)
		return p, sender, nil
	case TypePQResp:
		if len(body) != TxIDLen+PQCiphertextSize {
			return nil, sender, ErrInvalid
		}
		p := &PQResp{Ciphertext: append([]byte(nil), body[TxIDLen:]...)}
		copy(p.TxID[:], body)
		return p, sender, nil
	}
	return nil, sender, ErrInvalid
}
