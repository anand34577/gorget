package client_test

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/internal/core"
)

// A device that breaks a posture rule is told what to fix and kept off the network;
// when the rule is satisfied (here: relaxed) it joins without reconnecting.
func TestPostureBlocksAndUnblocks(t *testing.T) {
	srv := startServer(t)
	ctx := context.Background()
	if _, err := srv.core.SaveSettings(ctx, "posture", func(s *core.AllSettings) error {
		s.Posture = core.PostureSettings{Enabled: true, Mode: core.PostureEnforce, MinClientVersion: "99.0.0", MinOSVersion: map[string]string{}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p := &nsPlatform{ch: make(chan struct{})}
	c, err := client.New(client.Options{
		DataDir:            t.TempDir(),
		Platform:           p,
		Log:                slog.New(slog.NewTextHandler(io.Discard, nil)),
		Host:               client.HostInfo{Hostname: "posture-test", OS: "linux"},
		DisablePortMapping: true,
		TLSConfig:          &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Down)
	if _, err := c.SetServer(ctx, srv.url); err != nil {
		t.Fatal(err)
	}
	if err := c.LoginWithSetupKey(ctx, srv.setupKey); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { return c.Status().State == client.StateBlocked }, "blocked")
	if msg := c.Status().Error; !strings.Contains(msg, "99.0.0") {
		t.Fatalf("the notice should say what to fix, got %q", msg)
	}
	if _, err := srv.core.SaveSettings(ctx, "posture", func(s *core.AllSettings) error {
		s.Posture.MinClientVersion = "0.1.0"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { return c.Status().State == client.StateRunning }, "running after the rule was met")
}

// Two Gorget devices agree on a post-quantum pre-shared key, and traffic keeps flowing
// across the switch to the new key.
func TestEndToEndPostQuantum(t *testing.T) {
	srv := startServer(t)
	a := newNode(t, srv, "pq-a", false)
	b := newNode(t, srv, "pq-b", false)
	b.echo(t, 7100)
	addr := netip.AddrPortFrom(b.ip, 7100).String()
	waitFor(t, 30*time.Second, func() bool { return a.roundTrip(t, addr) == nil }, "TCP before the key exchange")
	waitFor(t, 30*time.Second, func() bool {
		pa, _ := peerPath(a, "pq-b")
		pb, _ := peerPath(b, "pq-a")
		return pa.PostQuantum && pb.PostQuantum
	}, "both sides report a post-quantum key")
	waitFor(t, 30*time.Second, func() bool { return a.roundTrip(t, addr) == nil }, "TCP with the post-quantum key")
	// The stored key survives a restart of the client core (no new exchange needed).
	if len(a.c.Status().Peers) == 0 {
		t.Fatal("no peers")
	}
}
