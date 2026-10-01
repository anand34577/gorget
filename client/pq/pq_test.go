package pq

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/anand34577/gorget/client/disco"
)

func init() {
	// Fast clocks for tests.
	pendingTimeout, commitRetry, firstBackoff, followerDelay = 300*time.Millisecond, 10*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond
}

// link connects two managers through an unreliable network.
type link struct {
	mu       sync.Mutex
	a, b     *Manager
	ka, kb   Key
	pskA     map[Key]applied
	pskB     map[Key]applied
	dropRate float64
	rng      *rand.Rand
}

type applied struct {
	psk Key
	gen uint64
}

func newLink(ka, kb Key, dropRate float64) *link {
	l := &link{ka: ka, kb: kb, pskA: map[Key]applied{}, pskB: map[Key]applied{}, dropRate: dropRate, rng: rand.New(rand.NewPCG(1, 2))}
	l.a = New(ka, Callbacks{
		Send:  func(peer Key, m disco.Message) { l.deliver(func() { l.b.Handle(ka, m) }) },
		Apply: func(peer Key, k *Key, gen uint64) { l.mu.Lock(); l.pskA[peer] = applied{*k, gen}; l.mu.Unlock() },
	}, nil)
	l.b = New(kb, Callbacks{
		Send:  func(peer Key, m disco.Message) { l.deliver(func() { l.a.Handle(kb, m) }) },
		Apply: func(peer Key, k *Key, gen uint64) { l.mu.Lock(); l.pskB[peer] = applied{*k, gen}; l.mu.Unlock() },
	}, nil)
	return l
}

// deliver drops or delays (reorders) a message.
func (l *link) deliver(f func()) {
	l.mu.Lock()
	drop := l.rng.Float64() < l.dropRate
	delay := time.Duration(l.rng.IntN(5)) * time.Millisecond
	l.mu.Unlock()
	if drop {
		return
	}
	time.AfterFunc(delay, f)
}

func (l *link) converged() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, okA := l.pskA[l.kb]
	b, okB := l.pskB[l.ka]
	return okA && okB && a == b && a.psk != (Key{})
}

// run keeps both sides calling Ensure (as the client does every 20 s) until they agree.
func (l *link) run(t *testing.T, bothStart bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		l.a.Ensure(l.kb)
		if bothStart {
			l.b.Ensure(l.ka)
		}
		if l.converged() {
			time.Sleep(100 * time.Millisecond) // let stragglers land; the result must not change
			if !l.converged() {
				t.Fatal("keys diverged after converging")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t.Fatalf("no agreement: A=%+v B=%+v", l.pskA, l.pskB)
}

func TestBothSidesDeriveTheSameKey(t *testing.T) {
	l := newLink(Key{1}, Key{2}, 0)
	l.run(t, false)
	if !l.a.Has(Key{2}) || !l.b.Has(Key{1}) {
		t.Fatal("Has() should be true on both sides")
	}
}

func TestEitherSideMayStart(t *testing.T) {
	// The larger key also starts (after its grace period) when the smaller never does.
	l := newLink(Key{9}, Key{2}, 0)
	l.run(t, false)
}

func TestSimultaneousStartAndLossyNetworkConverge(t *testing.T) {
	for i, drop := range []float64{0, 0.2, 0.4} {
		for trial := 0; trial < 6; trial++ {
			l := newLink(Key{1}, Key{2}, drop)
			l.rng = rand.New(rand.NewPCG(uint64(i*100+trial), 7))
			l.run(t, true)
		}
	}
}

func TestKeyIsBoundToBothIdentities(t *testing.T) {
	ss := make([]byte, 32)
	if derive(ss, Key{1}, Key{2}) == derive(ss, Key{2}, Key{1}) {
		t.Fatal("swapping initiator and responder must change the key")
	}
	if derive(ss, Key{1}, Key{2}) == derive(ss, Key{1}, Key{3}) {
		t.Fatal("a different peer must change the key")
	}
}

func TestIgnoresUnsolicitedAndInvalidMessages(t *testing.T) {
	l := newLink(Key{1}, Key{2}, 0)
	ka, kb := Key{1}, Key{2}
	l.a.Handle(kb, &disco.PQResp{Ciphertext: make([]byte, disco.PQCiphertextSize)})
	l.a.Handle(kb, &disco.PQCommit{Gen: 5})
	l.a.Handle(kb, &disco.PQAck{})
	bad := make([]byte, disco.PQEncapKeySize)
	for i := range bad {
		bad[i] = 0xff // coefficients above the modulus are not a valid key
	}
	l.b.Handle(ka, &disco.PQInit{Gen: 1, EncapKey: bad})
	time.Sleep(50 * time.Millisecond)
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pskA) != 0 || len(l.pskB) != 0 {
		t.Fatal("an invalid message installed a key")
	}
}

func TestOldGenerationNeverReplacesNewer(t *testing.T) {
	ka, kb := Key{2}, Key{1}
	var got []uint64
	m := New(ka, Callbacks{Send: func(Key, disco.Message) {}, Apply: func(_ Key, _ *Key, g uint64) { got = append(got, g) }}, []Entry{{Peer: kb, Gen: 100}})
	// Stage an old exchange by hand and commit it: must be refused.
	m.mu.Lock()
	m.peer(kb).staged[disco.TxID{1}] = &staged{gen: 50, at: time.Now()}
	m.mu.Unlock()
	m.Handle(kb, &disco.PQCommit{TxID: disco.TxID{1}, Gen: 50})
	if len(got) != 0 {
		t.Fatalf("an older generation was applied: %v", got)
	}
}

func TestEnsureBacksOffAndStopsOnceKeyed(t *testing.T) {
	ka, kb := Key{1}, Key{2}
	sent := 0
	m := New(ka, Callbacks{Send: func(Key, disco.Message) { sent++ }, Apply: func(Key, *Key, uint64) {}}, nil)
	m.Ensure(kb)
	m.Ensure(kb)
	m.Ensure(kb)
	if sent != 1 {
		t.Fatalf("Ensure must rate-limit, sent %d", sent)
	}
	keyed := New(ka, Callbacks{Send: func(Key, disco.Message) { sent++ }, Apply: func(Key, *Key, uint64) {}}, []Entry{{Peer: kb, Gen: 1}})
	keyed.Ensure(kb)
	if sent != 1 {
		t.Fatal("a peer that already has a key must not trigger an exchange")
	}
}
