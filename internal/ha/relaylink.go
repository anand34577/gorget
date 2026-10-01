package ha

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/netip"
	"time"

	"github.com/anand34577/gorget/internal/relay"
	"github.com/anand34577/gorget/internal/store"
)

// Relay link: when a client is connected to a different instance than the peer it sends
// to, the instance forwards the (already encrypted) packet to the right one over UDP.
//
//	datagram: 0x01 | HMAC-SHA256 truncated to 16 bytes | src key (32) | dst key (32) | packet
//
// The HMAC key is derived from the shared master key, so only instances of this cluster
// can inject packets. The sending instance has already checked that the policy allows the
// conversation.
const (
	linkMagic   = 0x01
	linkMACLen  = 16
	linkHdrLen  = 1 + linkMACLen + 32 + 32
	linkMaxSize = 2048
)

// startRelayLink begins listening for packets forwarded by other instances.
func (c *Cluster) startRelayLink(ctx context.Context) error {
	pc, err := net.ListenPacket("udp", c.o.PeerListen)
	if err != nil {
		return err
	}
	c.link = pc.(*net.UDPConn)
	go func() { <-ctx.Done(); pc.Close() }()
	go func() {
		buf := make([]byte, linkMaxSize)
		for {
			n, _, err := c.link.ReadFromUDPAddrPort(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				c.log.Warn("relay link read failed", "err", err)
				continue
			}
			c.receiveLink(buf[:n])
		}
	}()
	return nil
}

func (c *Cluster) mac(src, dst relay.Key, pkt []byte) []byte {
	h := hmac.New(sha256.New, c.o.MACKey)
	h.Write(src[:])
	h.Write(dst[:])
	h.Write(pkt)
	return h.Sum(nil)[:linkMACLen]
}

func (c *Cluster) receiveLink(b []byte) {
	if len(b) <= linkHdrLen || b[0] != linkMagic || c.o.Relay == nil {
		return
	}
	var src, dst relay.Key
	copy(src[:], b[1+linkMACLen:1+linkMACLen+32])
	copy(dst[:], b[1+linkMACLen+32:linkHdrLen])
	pkt := b[linkHdrLen:]
	if !hmac.Equal(b[1:1+linkMACLen], c.mac(src, dst, pkt)) {
		return
	}
	c.o.Relay.DeliverRemote(dst, src, append([]byte(nil), pkt...))
}

// ---------- relay.Remote ----------

// Presence records that a client connected to (or left) this instance's relay.
func (c *Cluster) Presence(key relay.Key, up bool) {
	c.mu.Lock()
	if up {
		c.relayLocal[key] = true
	} else {
		delete(c.relayLocal, key)
	}
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		k := hex.EncodeToString(key[:])
		var err error
		if up {
			err = c.o.Store.SetRelayPresence(ctx, k, c.o.InstanceID)
		} else {
			err = c.o.Store.ClearRelayPresence(ctx, k, c.o.InstanceID)
		}
		if err != nil {
			c.log.Debug("relay presence update failed", "err", err)
		}
		c.notify(message{T: "rp", Key: k, Up: up})
	}()
}

// Send forwards a packet to the instance that holds dst's relay connection.
func (c *Cluster) Send(dst, src relay.Key, pkt []byte) bool {
	if c.link == nil || len(pkt)+linkHdrLen > linkMaxSize {
		return false
	}
	c.mu.RLock()
	inst, ok := c.relayRemote[dst]
	addr := c.peerAddrs[inst]
	c.mu.RUnlock()
	if !ok || !addr.IsValid() {
		return false
	}
	b := make([]byte, linkHdrLen+len(pkt))
	b[0] = linkMagic
	copy(b[1:], c.mac(src, dst, pkt))
	copy(b[1+linkMACLen:], src[:])
	copy(b[1+linkMACLen+32:], dst[:])
	copy(b[linkHdrLen:], pkt)
	_, err := c.link.WriteToUDPAddrPort(b, addr)
	return err == nil
}

// refreshRelay reloads who holds which relay connection (called with the other refreshes).
func (c *Cluster) refreshRelay(ctx context.Context, instances []store.Instance) {
	since := store.Now() - int64(staleAfter.Seconds())
	rows, err := c.o.Store.ListRelayPresence(ctx, since)
	if err != nil {
		return
	}
	remote := make(map[relay.Key]string, len(rows))
	for k, inst := range rows {
		if inst == c.o.InstanceID {
			continue
		}
		b, err := hex.DecodeString(k)
		if err != nil || len(b) != 32 {
			continue
		}
		var key relay.Key
		copy(key[:], b)
		remote[key] = inst
	}
	addrs := map[string]netip.AddrPort{}
	for _, in := range instances {
		if ap, err := netip.ParseAddrPort(in.PeerAddr); err == nil && in.ID != c.o.InstanceID {
			addrs[in.ID] = ap
		}
	}
	c.mu.Lock()
	c.relayRemote, c.peerAddrs = remote, addrs
	c.mu.Unlock()
}

// applyRelayPresence handles an "rp" notification from another instance (instant update between refreshes).
func (c *Cluster) applyRelayPresence(from, keyHex string, up bool) {
	b, err := hex.DecodeString(keyHex)
	if err != nil || len(b) != 32 {
		return
	}
	var key relay.Key
	copy(key[:], b)
	c.mu.Lock()
	next := make(map[relay.Key]string, len(c.relayRemote)+1)
	for k, v := range c.relayRemote {
		next[k] = v
	}
	if up {
		next[key] = from
	} else if next[key] == from {
		delete(next, key)
	}
	c.relayRemote = next
	c.mu.Unlock()
}
