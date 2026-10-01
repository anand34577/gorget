package pq

import (
	"crypto/mlkem"
	"testing"
)

// BenchmarkExchangeCrypto is the CPU cost of one post-quantum key exchange (both sides).
func BenchmarkExchangeCrypto(b *testing.B) {
	a, c := Key{1}, Key{2}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dk, err := mlkem.GenerateKey768()
		if err != nil {
			b.Fatal(err)
		}
		ek, err := mlkem.NewEncapsulationKey768(dk.EncapsulationKey().Bytes())
		if err != nil {
			b.Fatal(err)
		}
		ss, ct := ek.Encapsulate()
		ss2, err := dk.Decapsulate(ct)
		if err != nil {
			b.Fatal(err)
		}
		if derive(ss, a, c) != derive(ss2, a, c) {
			b.Fatal("keys differ")
		}
	}
}
