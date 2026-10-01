package disco

import (
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/curve25519"
)

func keyPair(b *testing.B) (pub, priv Key) {
	b.Helper()
	_, _ = rand.Read(priv[:])
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		b.Fatal(err)
	}
	copy(pub[:], p)
	return
}

// BenchmarkSealOpenPing is the cost of one NAT-traversal probe round (seal + open).
func BenchmarkSealOpenPing(b *testing.B) {
	aPub, aPriv := keyPair(b)
	bPub, bPriv := keyPair(b)
	ping := &Ping{TxID: NewTxID()}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pkt := Seal(ping, &aPub, &aPriv, &bPub)
		if _, _, err := Open(pkt, &bPriv); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSealOpenPQInit covers the large post-quantum key-exchange message (about 1.2 KB).
func BenchmarkSealOpenPQInit(b *testing.B) {
	aPub, aPriv := keyPair(b)
	bPub, bPriv := keyPair(b)
	msg := &PQInit{TxID: NewTxID(), Gen: 1, EncapKey: make([]byte, PQEncapKeySize)}
	b.ReportAllocs()
	b.SetBytes(int64(PQEncapKeySize))
	for i := 0; i < b.N; i++ {
		pkt := Seal(msg, &aPub, &aPriv, &bPub)
		if _, _, err := Open(pkt, &bPriv); err != nil {
			b.Fatal(err)
		}
	}
}
