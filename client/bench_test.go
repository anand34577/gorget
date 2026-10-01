package client_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/anand34577/gorget/client/magicsock"
)

// BenchmarkTunnelThroughput moves bulk data between two Gorget clients (in-process, through
// the real WireGuard engine, NAT traversal and relay code) over a direct path and over the
// relay. Run with:  go test -run '^$' -bench Throughput -benchtime 3x ./client
func BenchmarkTunnelThroughput(b *testing.B) {
	for _, mode := range []struct {
		name        string
		blockDirect bool
	}{{"direct", false}, {"relay", true}} {
		b.Run(mode.name, func(b *testing.B) {
			magicsock.AllowLoopbackEndpoints()
			t := b
			srv := startServer(t)
			a := newNode(t, srv, "bench-a-"+mode.name, mode.blockDirect)
			c := newNode(t, srv, "bench-b-"+mode.name, mode.blockDirect)
			ln, err := c.p.stack(t).ListenTCP(&net.TCPAddr{Port: 7200})
			if err != nil {
				b.Fatal(err)
			}
			defer ln.Close()
			go func() {
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					go func() { defer conn.Close(); _, _ = io.Copy(io.Discard, conn) }()
				}
			}()
			addr := netip.AddrPortFrom(c.ip, 7200).String()
			c.echo(t, 7201) // the bulk listener discards data, so probe connectivity on an echo port
			probe := netip.AddrPortFrom(c.ip, 7201).String()
			deadline := time.Now().Add(30 * time.Second)
			for a.roundTrip(t, probe) != nil { // wait until the path works
				if time.Now().After(deadline) {
					b.Fatal("no connectivity")
				}
				time.Sleep(200 * time.Millisecond)
			}
			const chunk = 32 << 20
			buf := make([]byte, 64<<10)
			b.SetBytes(chunk)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				conn, err := a.p.stack(t).DialContext(ctx, "tcp", addr)
				cancel()
				if err != nil {
					b.Fatal(err)
				}
				for sent := 0; sent < chunk; {
					n, err := conn.Write(buf)
					if err != nil {
						b.Fatal(fmt.Errorf("write: %w", err))
					}
					sent += n
				}
				conn.Close()
			}
		})
	}
}
