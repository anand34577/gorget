package client_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/magicsock"
	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/relay"
	"github.com/anand34577/gorget/internal/rpcserver"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
	"github.com/anand34577/gorget/internal/stun"
)

type testServer struct {
	url      string
	setupKey string
	core     *core.Core
}

func startServer(t testing.TB) *testServer {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("GORGET_TEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	stunPC, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stunAddr := stunPC.LocalAddr().String()
	stunPC.Close()

	hs := httptest.NewUnstartedServer(nil)
	hs.EnableHTTP2 = true
	cfg := config.Default()
	cfg.PublicURL = "https://" + hs.Listener.Addr().String()
	cfg.DataDir = dir
	cfg.TLS.Mode = config.TLSModeOff
	cfg.Gateway.Enabled = false
	cfg.STUN.Listen = stunAddr
	cfg.STUN.Advertise = stunAddr
	if err := cfg.Finalize(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, "sqlite", filepath.Join(dir, "t.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.NewBox(secrets.RandomBytes(32))
	c, err := core.New(ctx, cfg, st, box, log)
	if err != nil {
		t.Fatal(err)
	}
	c.Run(ctx)
	go func() { _ = stun.New(log).ListenAndServe(ctx, stunAddr) }()

	rpc := rpcserver.New(c, log, func(*http.Request) string { return "127.0.0.1" })
	rl := relay.New(log, rpc.RelayAuthenticator(), rpc.RelayAuthorizer(), 0)
	mux := http.NewServeMux()
	path, h := rpc.Handler()
	mux.Handle(path, h)
	mux.Handle("/relay", rl)
	hs.Config.Handler = mux
	hs.StartTLS()
	t.Cleanup(hs.Close)

	plain := secrets.RandomToken("gsk_", 24)
	k := &store.SetupKey{ID: secrets.RandomID(), Name: "e2e", KeyHash: secrets.HashToken(plain), KeyPrefix: plain[:10], Reusable: true, AutoApprove: true, Tags: store.StringList{}, CreatedAt: store.Now()}
	if err := st.CreateSetupKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	return &testServer{url: hs.URL, setupKey: plain, core: c}
}

// nsPlatform provides a userspace (gVisor) network stack as the "OS" TUN.
type nsPlatform struct {
	mu  sync.Mutex
	net *netstack.Net
	dev tun.Device
	cfg client.TUNConfig
	ch  chan struct{}
}

func (p *nsPlatform) ApplyTUN(cfg client.TUNConfig) (tun.Device, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	if len(cfg.Addresses) == 0 || p.dev != nil {
		return nil, nil
	}
	var addrs []netip.Addr
	for _, a := range cfg.Addresses {
		addrs = append(addrs, a.Addr())
	}
	dev, n, err := netstack.CreateNetTUN(addrs, cfg.DNS, 1280)
	if err != nil {
		return nil, err
	}
	p.dev, p.net = dev, n
	close(p.ch)
	return dev, nil
}

func (p *nsPlatform) Protect(uintptr) bool { return true }

func (p *nsPlatform) stack(t testing.TB) *netstack.Net {
	select {
	case <-p.ch:
	case <-time.After(20 * time.Second):
		t.Fatal("TUN never configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.net
}

type node struct {
	c  *client.Client
	p  *nsPlatform
	ip netip.Addr
}

func newNode(t testing.TB, srv *testServer, name string, blockDirect bool) *node {
	t.Helper()
	p := &nsPlatform{ch: make(chan struct{})}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("GORGET_TEST_VERBOSE") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})).With("node", name)
	}
	c, err := client.New(client.Options{
		DataDir:     t.TempDir(),
		Platform:    p,
		Log:         log,
		Host:        client.HostInfo{Hostname: name, OS: "linux"},
		TLSConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server
		BlockDirect: blockDirect,
		// Tests must never open ports on the developer's router.
		DisablePortMapping: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Down)
	ctx := context.Background()
	if _, err := c.SetServer(ctx, srv.url); err != nil {
		t.Fatal(err)
	}
	if err := c.LoginWithSetupKey(ctx, srv.setupKey); err != nil {
		t.Fatal(err)
	}
	n := &node{c: c, p: p}
	waitFor(t, 20*time.Second, func() bool {
		st := c.Status()
		if st.State == client.StateRunning && st.Self != nil {
			n.ip = netip.MustParseAddr(st.Self.IPv4)
			return true
		}
		return false
	}, name+" running")
	return n
}

func waitFor(t testing.TB, d time.Duration, f func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// echo serves one TCP echo listener on the node's stack.
func (n *node) echo(t testing.TB, port int) {
	ln, err := n.p.stack(t).ListenTCP(&net.TCPAddr{Port: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
}

func (n *node) roundTrip(t testing.TB, addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := n.p.stack(t).DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	msg := "hello over gorget " + addr
	if _, err := c.Write([]byte(msg)); err != nil {
		return err
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return fmt.Errorf("echo mismatch: %q", buf)
	}
	return nil
}

func peerPath(n *node, peerName string) (client.PeerView, bool) {
	for _, p := range n.c.Status().Peers {
		if p.Name == peerName {
			return p, true
		}
	}
	return client.PeerView{}, false
}

func TestEndToEndDirect(t *testing.T) {
	magicsock.AllowLoopbackEndpoints()
	srv := startServer(t)
	a := newNode(t, srv, "alpha", false)
	b := newNode(t, srv, "bravo", false)
	waitFor(t, 15*time.Second, func() bool { _, ok := peerPath(a, "bravo"); return ok }, "alpha sees bravo")
	b.echo(t, 7000)

	var lastErr error
	waitFor(t, 30*time.Second, func() bool {
		lastErr = a.roundTrip(t, netip.AddrPortFrom(b.ip, 7000).String())
		return lastErr == nil
	}, "TCP alpha→bravo")

	// Traffic must upgrade to a direct UDP path.
	waitFor(t, 30*time.Second, func() bool {
		_ = a.roundTrip(t, netip.AddrPortFrom(b.ip, 7000).String())
		p, _ := peerPath(a, "bravo")
		return p.Direct
	}, "direct path")
	p, _ := peerPath(a, "bravo")
	t.Logf("alpha→bravo direct via %s (%d ms), rx=%d tx=%d", p.Endpoint, p.LatencyMs, p.RxBytes, p.TxBytes)

	// Device names resolve through the in-tunnel DNS.
	waitFor(t, 10*time.Second, func() bool {
		addrs, err := a.p.stack(t).LookupHost("bravo.gorget.internal")
		if err != nil {
			t.Logf("lookup: %v", err)
		}
		for _, x := range addrs {
			if x == b.ip.String() {
				return true
			}
		}
		return false
	}, "DNS bravo.gorget.internal")
	if err := a.roundTrip(t, "bravo.gorget.internal:7000"); err != nil {
		t.Fatalf("dial by name: %v", err)
	}
}

func TestEndToEndRelay(t *testing.T) {
	srv := startServer(t)
	a := newNode(t, srv, "relay-a", true)
	b := newNode(t, srv, "relay-b", true)
	b.echo(t, 7001)
	var lastErr error
	waitFor(t, 30*time.Second, func() bool {
		lastErr = a.roundTrip(t, netip.AddrPortFrom(b.ip, 7001).String())
		return lastErr == nil
	}, "TCP over relay")
	if p, _ := peerPath(a, "relay-b"); p.Direct {
		t.Fatal("expected relayed path")
	}
	if st := a.c.Status(); !st.RelayUp {
		t.Fatal("relay not connected")
	}
}

func TestPolicyBlocksTraffic(t *testing.T) {
	srv := startServer(t)
	a := newNode(t, srv, "pa", false)
	b := newNode(t, srv, "pb", false)
	b.echo(t, 22)
	b.echo(t, 80)
	waitFor(t, 30*time.Second, func() bool { return a.roundTrip(t, netip.AddrPortFrom(b.ip, 22).String()) == nil }, "baseline")
	// Only allow port 22 to pb.
	doc := `{"acls":[{"action":"accept","src":["*"],"dst":["device:pb:22"]}]}`
	if _, err := srv.core.Store.SavePolicy(context.Background(), doc, "test", "test", 0); err != nil {
		t.Fatal(err)
	}
	srv.core.Coord.Trigger()
	waitFor(t, 15*time.Second, func() bool { return a.roundTrip(t, netip.AddrPortFrom(b.ip, 80).String()) != nil }, "port 80 blocked")
	if err := a.roundTrip(t, netip.AddrPortFrom(b.ip, 22).String()); err != nil {
		t.Fatalf("port 22 should still work: %v", err)
	}
	// Reverse direction is not allowed at all: pb → pa:22.
	a.echo(t, 22)
	if err := b.roundTrip(t, netip.AddrPortFrom(a.ip, 22).String()); err == nil || !strings.Contains(err.Error(), "") {
		t.Fatalf("reverse direction should be blocked, got %v", err)
	}
}
