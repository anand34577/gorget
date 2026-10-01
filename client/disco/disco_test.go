package disco

import (
	"crypto/rand"
	"net/netip"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func keys(t *testing.T) (*Key, *Key) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestRoundTrip(t *testing.T) {
	aPub, aPriv := keys(t)
	bPub, bPriv := keys(t)
	msgs := []Message{
		&Ping{TxID: NewTxID(), NodeKey: Key{1, 2, 3}},
		&Pong{TxID: NewTxID(), Src: netip.MustParseAddrPort("203.0.113.5:41641")},
		&Pong{TxID: NewTxID(), Src: netip.MustParseAddrPort("[2001:db8::1]:5000")},
		&CallMeMaybe{Endpoints: []netip.AddrPort{netip.MustParseAddrPort("10.0.0.2:1"), netip.MustParseAddrPort("[fd00::2]:2")}},
	}
	for _, m := range msgs {
		pkt := Seal(m, aPub, aPriv, bPub)
		if !IsDisco(pkt) {
			t.Fatal("not recognised")
		}
		got, sender, err := Open(pkt, bPriv)
		if err != nil {
			t.Fatal(err)
		}
		if sender != *aPub {
			t.Fatal("wrong sender")
		}
		switch w := m.(type) {
		case *Ping:
			if g := got.(*Ping); g.TxID != w.TxID || g.NodeKey != w.NodeKey {
				t.Fatal("ping mismatch")
			}
		case *Pong:
			if g := got.(*Pong); g.TxID != w.TxID || g.Src != w.Src {
				t.Fatalf("pong mismatch %v %v", g.Src, w.Src)
			}
		case *CallMeMaybe:
			if g := got.(*CallMeMaybe); len(g.Endpoints) != 2 || g.Endpoints[1] != w.Endpoints[1] {
				t.Fatal("cmm mismatch")
			}
		}
		// A third party cannot open it.
		_, cPriv := keys(t)
		if _, _, err := Open(pkt, cPriv); err == nil {
			t.Fatal("opened with wrong key")
		}
	}
	if IsDisco([]byte{1, 0, 0, 0, 5, 6, 7}) {
		t.Fatal("wireguard packet treated as disco")
	}
}
