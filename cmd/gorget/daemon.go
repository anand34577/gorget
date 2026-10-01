package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kardianos/service"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/filetransfer"
	"github.com/anand34577/gorget/client/localapi"
	"github.com/anand34577/gorget/client/osrouter"
)

// daemon is the background service: the client core plus the local API.
type daemon struct {
	socket  string
	dataDir string
	debug   bool

	cancel context.CancelFunc
	done   chan error
}

func (d *daemon) Start(service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.done = cancel, make(chan error, 1)
	go func() {
		err := d.run(ctx)
		d.done <- err
		if err != nil {
			fmt.Fprintln(os.Stderr, "daemon exited:", err)
			os.Exit(1)
		}
	}()
	return nil
}

func (d *daemon) Stop(service.Service) error {
	if d.cancel != nil {
		d.cancel()
		select {
		case <-d.done:
		case <-time.After(15 * time.Second):
		}
	}
	return nil
}

// logger writes to stderr when interactive, otherwise to <data>/gorget.log
// (restarted when it passes 5 MiB, keeping the previous file as gorget.log.1).
func (d *daemon) logger() (*slog.Logger, io.Closer) {
	lvl := slog.LevelInfo
	if d.debug {
		lvl = slog.LevelDebug
	}
	var w io.Writer = os.Stderr
	var closer io.Closer = io.NopCloser(nil)
	if !service.Interactive() {
		_ = os.MkdirAll(d.dataDir, 0o700)
		path := filepath.Join(d.dataDir, "gorget.log")
		if fi, err := os.Stat(path); err == nil && fi.Size() > 5<<20 {
			_ = os.Rename(path, path+".1")
		}
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			w, closer = f, f
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})), closer
}

func (d *daemon) run(ctx context.Context) error {
	if d.dataDir == "" {
		d.dataDir = localapi.DefaultDataDir()
	}
	log, closer := d.logger()
	defer closer.Close()
	log.Info("gorget daemon starting", "version", client.Version)

	router, err := osrouter.New(log)
	if err != nil {
		return err
	}
	defer router.Close()
	hostname, _ := os.Hostname()
	c, err := client.New(client.Options{
		DataDir:  d.dataDir,
		Platform: router,
		Log:      log,
		Host:     client.HostInfo{Hostname: shortHost(hostname), OSVersion: osVersion()},
	})
	if err != nil {
		return err
	}
	addr := d.socket
	if addr == "" {
		addr = localapi.DefaultAddr()
	}
	ln, err := localapi.Listen(addr)
	if err != nil {
		return fmt.Errorf("local API on %s: %w", addr, err)
	}
	api := localapi.New(c, d.dataDir)
	// Receive files from this user's other devices (only on the overlay address).
	inbox := filetransfer.NewReceiver(filepath.Join(d.dataDir, "inbox"), fileIdentity{c}, log)
	defer inbox.Close()
	api.Files = inbox
	if b, ok := router.(osrouter.AppBypasser); ok {
		api.Bypass = b.BypassApp
	}
	syncInbox := func(st client.Status) {
		if st.State == client.StateRunning {
			addrs, _ := c.SelfAddrs()
			inbox.Sync(addrs)
		} else {
			inbox.Sync(nil)
		}
	}
	c.OnStatus(syncInbox)
	srvCtx, stop := context.WithCancel(ctx)
	api.Quit = stop
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := api.Serve(srvCtx, ln); err != nil {
			log.Error("local API stopped", "err", err)
		}
	}()
	go func() { defer wg.Done(); watchNetwork(srvCtx, c, log) }()

	if c.WantRunning() {
		log.Info("restoring connection")
		if err := c.Up(); err != nil {
			log.Warn("could not reconnect", "err", err)
		}
	}
	<-srvCtx.Done()
	log.Info("gorget daemon stopping")
	c.Shutdown() // keeps WantRunning so the next start reconnects
	_ = ln.Close()
	wg.Wait()
	return nil
}

// fileIdentity tells the file receiver who is on the other end of a connection.
type fileIdentity struct{ c *client.Client }

func (f fileIdentity) SenderUser(ip netip.Addr) (string, string, bool) {
	p, ok := f.c.PeerByAddr(ip)
	return p.Name, p.User, ok
}

func (f fileIdentity) SelfUser() string {
	_, u := f.c.SelfAddrs()
	return u
}

func shortHost(h string) string {
	h, _, _ = strings.Cut(h, ".")
	if h == "" {
		return "device"
	}
	return h
}

// watchNetwork polls the machine's addresses and tells the client when they change
// (Wi-Fi ↔ Ethernet, new IP, resume from sleep), so paths are re-probed quickly.
// Polling every 5s is simpler than per-OS change notifications; swap in netlink /
// NotifyAddrChange if the delay ever matters.
func watchNetwork(ctx context.Context, c *client.Client, log *slog.Logger) {
	last := addrFingerprint()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	prev := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			cur := addrFingerprint()
			// A long gap between ticks means the machine was asleep.
			if cur != last || now.Sub(prev) > 30*time.Second {
				log.Info("network changed")
				c.NetworkChanged()
				last = cur
			}
			prev = now
		}
	}
}

func addrFingerprint() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var parts []string
	for _, ifc := range ifs {
		// Skip our own tunnel and anything down or looping back.
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || strings.HasPrefix(ifc.Name, osrouter.InterfaceName) || strings.HasPrefix(ifc.Name, "utun") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			parts = append(parts, ifc.Name+"="+a.String())
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
