// Package gorgetcore is the gomobile binding of the Gorget client for Android.
// It exposes a small API made of strings, ints and JSON so it maps cleanly to
// Kotlin (generated as io.gorget.gorgetcore.*).
//
// Build: gomobile bind -target=android -androidapi 26 -javapkg io.gorget -o gorgetcore.aar ./mobile/gorgetcore
package gorgetcore

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/tun"

	"github.com/anand34577/gorget/client"
)

// Platform is implemented in Kotlin by the VpnService.
type Platform interface {
	// Protect excludes a socket from the VPN (VpnService.protect).
	Protect(fd int32) bool
	// EstablishTUN configures the VPN interface from a JSON TUNConfig and returns a
	// detached file descriptor, or -1. An empty "addresses" list means: close the interface.
	EstablishTUN(configJSON string) int32
	// OnStatus receives the JSON status whenever it changes.
	OnStatus(statusJSON string)
	// Log receives log lines (level: 0 debug, 1 info, 2 warn, 3 error).
	Log(level int32, msg string)
}

type Client struct {
	c *client.Client
	p Platform
}

type adapter struct{ p Platform }

func (a adapter) Protect(fd uintptr) bool { return a.p.Protect(int32(fd)) }

func (a adapter) ApplyTUN(cfg client.TUNConfig) (tun.Device, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	fd := a.p.EstablishTUN(string(b))
	if len(cfg.Addresses) == 0 {
		return nil, nil
	}
	if fd < 0 {
		return nil, errors.New("the system refused to create the VPN interface (VPN permission revoked or another VPN is always-on)")
	}
	return tunFromFD(int(fd), cfg.MTU)
}

// NewClient creates the client. dataDir must be the app's private files directory.
func NewClient(dataDir, hostname, osVersion string, p Platform) (*Client, error) {
	log := slog.New(&logHandler{p: p, level: slog.LevelInfo})
	c, err := client.New(client.Options{
		DataDir:       dataDir,
		Platform:      adapter{p: p},
		Log:           log,
		Host:          client.HostInfo{Hostname: hostname, OS: "android", OSVersion: osVersion},
		ManualConnect: true,
	})
	if err != nil {
		return nil, err
	}
	mc := &Client{c: c, p: p}
	c.OnStatus(func(client.Status) { mc.pushStatus() })
	return mc, nil
}

func (m *Client) pushStatus() {
	b, err := json.Marshal(m.c.Status())
	if err == nil {
		m.p.OnStatus(string(b))
	}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// SetServer validates and saves the server address; returns the network name.
func (m *Client) SetServer(url string) (string, error) {
	c, cancel := ctx()
	defer cancel()
	return m.c.SetServer(c, url)
}

// LoginInteractive starts a browser sign-in and returns {"url":..., "code":...}.
func (m *Client) LoginInteractive() (string, error) {
	c, cancel := ctx()
	defer cancel()
	u, code, err := m.c.LoginInteractive(c)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]string{"url": u, "code": code})
	return string(b), nil
}

func (m *Client) LoginWithSetupKey(key string) error {
	c, cancel := ctx()
	defer cancel()
	return m.c.LoginWithSetupKey(c, key)
}

func (m *Client) Up() error { return m.c.Up() }
func (m *Client) Down()     { m.c.Down() }

func (m *Client) Logout() error {
	c, cancel := ctx()
	defer cancel()
	return m.c.Logout(c)
}

// Status returns the full status as JSON.
func (m *Client) Status() string {
	b, _ := json.Marshal(m.c.Status())
	return string(b)
}

func (m *Client) Prefs() string {
	b, _ := json.Marshal(m.c.Prefs())
	return string(b)
}

// SetPrefs replaces preferences from JSON.
func (m *Client) SetPrefs(prefsJSON string) error {
	p := m.c.Prefs()
	if err := json.Unmarshal([]byte(prefsJSON), &p); err != nil {
		return err
	}
	return m.c.SetPrefs(p)
}

// SetExitNode selects an exit node by peer ID ("" for none).
func (m *Client) SetExitNode(id string) error {
	p := m.c.Prefs()
	p.ExitNodeID = id
	return m.c.SetPrefs(p)
}

func (m *Client) NetworkChanged() { m.c.NetworkChanged() }

// RefreshTUN rebuilds the VPN interface (call after changing per-app rules).
func (m *Client) RefreshTUN() error { return m.c.RefreshTUN() }

// SetLocalAddresses passes comma-separated interface addresses (Android hides them from Go).
func (m *Client) SetLocalAddresses(csv string) {
	var out []netip.Addr
	for _, s := range strings.Split(csv, ",") {
		if a, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil {
			out = append(out, a)
		}
	}
	m.c.SetLocalAddresses(out)
}

func (m *Client) WantRunning() bool  { return m.c.WantRunning() }
func (m *Client) IsRegistered() bool { return m.c.IsRegistered() }
func (m *Client) Version() string    { return client.Version }

// NormalizeServerURL is exposed for input validation in the UI.
func NormalizeServerURL(s string) (string, error) { return client.NormalizeServerURL(s) }

// ---------- logging ----------

type logHandler struct {
	p     Platform
	level slog.Level
	attrs []slog.Attr
}

func (h *logHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *logHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
	}
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		return true
	})
	lvl := int32(1)
	switch {
	case r.Level < slog.LevelInfo:
		lvl = 0
	case r.Level >= slog.LevelError:
		lvl = 3
	case r.Level >= slog.LevelWarn:
		lvl = 2
	}
	h.p.Log(lvl, b.String())
	return nil
}

func (h *logHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &logHandler{p: h.p, level: h.level, attrs: append(append([]slog.Attr{}, h.attrs...), as...)}
}

func (h *logHandler) WithGroup(string) slog.Handler { return h }
