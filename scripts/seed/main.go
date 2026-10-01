// Command seed registers fake Gorget devices (in-memory network stacks, no OS changes) so the
// web console can be developed and demonstrated with realistic data. Development tool only.
//
//	go run ./scripts/seed -server http://localhost:8080 -setup-key gsk_... [-n 12]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/anand34577/gorget/client"
)

type fakeOS struct {
	mu  sync.Mutex
	dev tun.Device
}

func (p *fakeOS) ApplyTUN(cfg client.TUNConfig) (tun.Device, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(cfg.Addresses) == 0 || p.dev != nil {
		return nil, nil
	}
	var addrs []netip.Addr
	for _, a := range cfg.Addresses {
		addrs = append(addrs, a.Addr())
	}
	dev, _, err := netstack.CreateNetTUN(addrs, cfg.DNS, 1280)
	if err != nil {
		return nil, err
	}
	p.dev = dev
	return dev, nil
}

func (p *fakeOS) Protect(uintptr) bool { return true }

type spec struct {
	name, os, version string
	exit              bool
	routes            []string
}

var specs = []spec{
	{"anand-laptop", "windows", "Windows 10.0 (build 26200)", false, nil},
	{"anand-phone", "android", "Android 15", false, nil},
	{"studio-mac", "darwin", "macOS 14.5", false, nil},
	{"build-server", "linux", "Ubuntu 24.04.1 LTS", true, nil},
	{"nas", "linux", "Debian 12", false, []string{"192.168.1.0/24"}},
	{"office-router", "linux", "OpenWrt 23.05", true, []string{"10.20.0.0/16"}},
	{"priya-laptop", "darwin", "macOS 13.6", false, nil},
	{"priya-phone", "android", "Android 14", false, nil},
	{"raspberry-pi", "linux", "Raspbian 12", false, nil},
	{"dev-vm", "linux", "Fedora 40", false, nil},
	{"kiosk-1", "windows", "Windows 10.0 (build 19045)", false, nil},
	{"tv-livingroom", "android", "Android 13", false, nil},
}

func main() {
	server := flag.String("server", "http://localhost:8080", "server URL")
	key := flag.String("setup-key", "", "reusable setup key")
	n := flag.Int("n", len(specs), "how many devices")
	flag.Parse()
	if *key == "" {
		log.Fatal("-setup-key is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	dir, err := os.MkdirTemp("", "gorget-seed-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	var clients []*client.Client
	for i := 0; i < *n && i < len(specs); i++ {
		s := specs[i]
		c, err := client.New(client.Options{
			DataDir:  filepath.Join(dir, s.name),
			Platform: &fakeOS{},
			Log:      logger,
			Host:     client.HostInfo{Hostname: s.name, OS: s.os, OSVersion: s.version},
			// Fake devices must not ask the real router for port forwards.
			DisablePortMapping: true,
		})
		if err != nil {
			log.Fatal(err)
		}
		if _, err := c.SetServer(ctx, *server); err != nil {
			log.Fatal(err)
		}
		if err := c.LoginWithSetupKey(ctx, *key); err != nil {
			log.Fatal(err)
		}
		p := c.Prefs()
		p.AdvertiseExitNode = s.exit
		p.AdvertiseRoutes = s.routes
		if err := c.SetPrefs(p); err != nil {
			log.Fatal(err)
		}
		clients = append(clients, c)
		fmt.Println("registered", s.name)
		time.Sleep(4 * time.Second) // stay under the server rate limit for unauthenticated calls
	}
	fmt.Println("devices are online; press Ctrl+C to stop")
	<-ctx.Done()
	for _, c := range clients {
		c.Shutdown()
	}
}
