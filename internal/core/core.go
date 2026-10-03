// Package core contains Gorget's business logic shared by the admin API, the
// client RPC API and the gateway.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/geoip"
	"github.com/anand34577/gorget/internal/mailer"
	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// Version is set at build time.
var Version = "dev"

// ProtocolVersion of the client control API.
const (
	ProtocolVersion    = 1
	MinProtocolVersion = 1
)

type Core struct {
	Cfg   config.Config
	Store *store.Store
	Box   *secrets.Box
	Log   *slog.Logger
	Bus   *Bus
	Coord *Coordinator
	// cluster is nil unless several instances share the database.
	cluster Cluster

	mu         sync.RWMutex
	settings   AllSettings
	settingsMu sync.Mutex // serialises SaveSettings
	setup      SetupState
	setupToken string
	gatewayID  string
	gwPriv     wgtypes.Key

	// Geo maps public addresses to countries (local database; empty until one is loaded).
	Geo        *geoip.DB
	mailq      chan mailer.Message
	mailStatus mailStatus
	push       pushStatus
	pushq      chan pushMsg
	offMu      sync.Mutex
	offSent    map[string]bool // devices whose absence was announced
	wg         *wgPresence
	login      *loginHistory
}

func New(ctx context.Context, cfg config.Config, st *store.Store, box *secrets.Box, log *slog.Logger) (*Core, error) {
	c := &Core{Cfg: cfg, Store: st, Box: box, Log: log, Bus: NewBus(), mailq: make(chan mailer.Message, 500), wg: newWGPresence(), login: newLoginHistory(), pushq: make(chan pushMsg, 200), offSent: map[string]bool{}}
	c.Geo = geoip.Open(filepath.Join(cfg.DataDir, "geoip"))
	c.Coord = newCoordinator(c)
	if err := c.loadSettings(ctx); err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	if err := st.GetSetting(ctx, keySetup, &c.setup); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	if err := c.initSetupToken(); err != nil {
		return nil, fmt.Errorf("setup token: %w", err)
	}
	if _, err := st.CurrentPolicy(ctx); errors.Is(err, store.ErrNotFound) {
		if _, err := st.SavePolicy(ctx, policy.DefaultAllowAll, "initial policy", "system", 0); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := c.loadGatewayKey(ctx); err != nil {
		return nil, fmt.Errorf("gateway key: %w", err)
	}
	if cfg.Gateway.Enabled {
		if err := c.syncGatewayDevice(ctx); err != nil {
			return nil, fmt.Errorf("gateway device: %w", err)
		}
	}
	if err := c.Coord.rebuild(ctx); err != nil {
		return nil, fmt.Errorf("initial network state: %w", err)
	}
	return c, nil
}

// Run starts background loops until ctx is done.
func (c *Core) Run(ctx context.Context) {
	go c.Coord.run(ctx)
	go c.maintenance(ctx)
	go c.runWebhooks(ctx)
	go c.runMail(ctx)
	go c.runNotifier(ctx)
	go c.runPush(ctx)
	go c.runWGPresence(ctx)
	go c.runStats(ctx)
	go c.runGeoUpdates(ctx)
}

func (c *Core) SetupCompleted() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.setup.Completed
}

// CompleteSetup marks the first-run wizard as done and removes the setup token.
func (c *Core) CompleteSetup(ctx context.Context) error {
	if err := c.markSetupCompleted(ctx); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(c.Cfg.DataDir, "setup-token"))
	return nil
}

// SetupToken returns the one-time token required by the setup wizard ("" once set up).
func (c *Core) SetupToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.setupToken
}

func (c *Core) initSetupToken() error {
	if c.setup.Completed {
		return nil
	}
	path := filepath.Join(c.Cfg.DataDir, "setup-token")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		c.setupToken = strings.TrimSpace(string(b))
	} else {
		c.setupToken = secrets.RandomToken("", 18)
		if err := os.WriteFile(path, []byte(c.setupToken), 0o600); err != nil {
			return err
		}
	}
	c.Log.Warn("first-run setup is pending: open the web console and enter this setup token",
		"url", c.Cfg.PublicURL+"/setup", "setup_token", c.setupToken, "file", path)
	printSetupBanner(c.Cfg.PublicURL, c.setupToken, path)
	return nil
}

// printSetupBanner makes the first-run link impossible to miss in a terminal or in
// `docker logs`. The token travels in the URL fragment (#), which browsers never
// send to the server, so it doesn't end up in proxy or access logs.
func printSetupBanner(publicURL, token, file string) {
	link := strings.TrimSuffix(publicURL, "/") + "/setup#token=" + token
	lines := []string{
		"Gorget is running. Finish setting it up in your browser:",
		"",
		"  " + link,
		"",
		"Setup token: " + token,
		"(also saved in " + file + "; it stops working once setup is done)",
	}
	width := 0
	for _, l := range lines {
		width = max(width, len(l))
	}
	bar := strings.Repeat("=", width+4)
	var b strings.Builder
	b.WriteString("\n" + bar + "\n")
	for _, l := range lines {
		b.WriteString("| " + l + strings.Repeat(" ", width-len(l)) + " |\n")
	}
	b.WriteString(bar + "\n\n")
	_, _ = os.Stderr.WriteString(b.String())
}

func (c *Core) markSetupCompleted(ctx context.Context) error {
	s := SetupState{Completed: true, CompletedAt: store.Now()}
	if err := c.Store.PutSetting(ctx, keySetup, s); err != nil {
		return err
	}
	c.mu.Lock()
	c.setup = s
	c.mu.Unlock()
	return nil
}

// ---------- gateway identity ----------

func (c *Core) loadGatewayKey(ctx context.Context) error {
	var gk gatewayKey
	err := c.Store.GetSetting(ctx, keyGatewayKey, &gk)
	if errors.Is(err, store.ErrNotFound) {
		k, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return err
		}
		sealed, err := c.Box.Seal(k.String())
		if err != nil {
			return err
		}
		if err := c.Store.PutSetting(ctx, keyGatewayKey, gatewayKey{PrivateKey: sealed}); err != nil {
			return err
		}
		c.gwPriv = k
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := c.Box.Open(gk.PrivateKey)
	if err != nil {
		return err
	}
	k, err := wgtypes.ParseKey(raw)
	if err != nil {
		return err
	}
	c.gwPriv = k
	return nil
}

// GatewayPrivateKey returns the gateway's WireGuard private key.
func (c *Core) GatewayPrivateKey() wgtypes.Key { return c.gwPriv }

func (c *Core) GatewayPublicKey() wgtypes.Key { return c.gwPriv.PublicKey() }

func (c *Core) GatewayID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gatewayID
}

// syncGatewayDevice ensures the built-in gateway exists as a device record.
func (c *Core) syncGatewayDevice(ctx context.Context) error {
	if !c.Cfg.Gateway.Enabled {
		return nil
	}
	plan := c.Plan()
	gs := c.Settings().Gateway
	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return err
	}
	var gw *store.Device
	for i := range devs {
		if devs[i].Kind == store.KindGateway {
			gw = &devs[i]
			break
		}
	}
	now := store.Now()
	if gw == nil {
		gw = &store.Device{
			ID:                secrets.RandomID(),
			Name:              uniqueName("gateway", nil),
			Kind:              store.KindGateway,
			WGPublicKey:       c.GatewayPublicKey().String(),
			IPv4:              plan.GatewayIPv4().String(),
			IPv6:              plan.GatewayIPv6().String(),
			StaticIP:          true,
			Tags:              store.StringList{},
			State:             store.StateActive,
			KeyExpiryDisabled: true,
			Hostname:          "gorget-gateway",
			OS:                "linux",
			ClientVersion:     Version,
			Endpoints:         store.StringList{c.Cfg.Gateway.Endpoint},
			ExitAdvertised:    gs.ExitNode,
			ExitApproved:      gs.ExitNode,
			CustomAllowedIPs:  store.StringList{},
			DNSEnabled:        true,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := c.Store.CreateDevice(ctx, gw); err != nil {
			return err
		}
	} else {
		gw.WGPublicKey = c.GatewayPublicKey().String()
		gw.IPv4 = plan.GatewayIPv4().String()
		gw.IPv6 = plan.GatewayIPv6().String()
		gw.Endpoints = store.StringList{c.Cfg.Gateway.Endpoint}
		gw.ExitAdvertised = gs.ExitNode
		gw.ExitApproved = gs.ExitNode
		gw.ClientVersion = Version
		gw.State = store.StateActive
		if err := c.Store.UpdateDevice(ctx, gw); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.gatewayID = gw.ID
	c.mu.Unlock()
	return nil
}

// ---------- audit ----------

// Actor identifies who performed an action.
type Actor struct {
	ID    string
	Name  string
	IP    string
	Role  string
	Token bool // acting via API token
}

var SystemActor = Actor{ID: "system", Name: "system"}

// Audit appends to the tamper-evident audit log. Failures are logged, never fatal.
func (c *Core) Audit(ctx context.Context, a Actor, action, targetType, targetID, targetName string, details any) {
	d := "{}"
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			d = string(b)
		}
	}
	e := &store.AuditEntry{
		TS: store.Now(), ActorID: a.ID, Actor: a.Name, Action: action,
		TargetType: targetType, TargetID: targetID, TargetName: targetName, Details: d, IP: a.IP,
	}
	if err := c.Store.AppendAudit(context.WithoutCancel(ctx), e); err != nil {
		c.Log.Error("audit append failed", "action", action, "err", err)
	}
}

// ---------- maintenance ----------

func (c *Core) maintenance(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	warned := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !c.IsLeader() {
			continue // one instance runs the periodic jobs
		}
		if err := c.Store.DeleteExpired(ctx); err != nil {
			c.Log.Warn("cleanup expired sessions", "err", err)
		}
		c.cleanupDevices(ctx, warned)
	}
}

func (c *Core) cleanupDevices(ctx context.Context, warned map[string]bool) {
	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return
	}
	ds := c.Settings().Devices
	now := time.Now()
	changed := false
	for _, d := range devs {
		online := c.Coord.IsOnline(d.ID)
		lastSeen := time.Unix(d.LastSeenAt, 0)
		switch {
		case d.Kind == store.KindNative && d.Ephemeral && !online && d.LastSeenAt > 0 &&
			now.Sub(lastSeen) > time.Duration(max(ds.EphemeralTimeoutMins, 1))*time.Minute:
			if err := c.Store.DeleteDevice(ctx, d.ID); err == nil {
				c.Audit(ctx, SystemActor, "device.delete", "device", d.ID, d.Name, map[string]any{"reason": "ephemeral device offline"})
				c.Bus.Publish(EvDeviceDeleted, map[string]string{"id": d.ID, "name": d.Name})
				changed = true
			}
		case d.Kind == store.KindNative && ds.InactiveCleanupDays > 0 && !online && d.LastSeenAt > 0 &&
			now.Sub(lastSeen) > time.Duration(ds.InactiveCleanupDays)*24*time.Hour:
			if err := c.Store.DeleteDevice(ctx, d.ID); err == nil {
				c.Audit(ctx, SystemActor, "device.delete", "device", d.ID, d.Name, map[string]any{"reason": "inactive"})
				c.Bus.Publish(EvDeviceDeleted, map[string]string{"id": d.ID, "name": d.Name})
				changed = true
			}
		case d.Kind == store.KindWireGuard && d.ExpiresAt > 0 && now.Unix() > d.ExpiresAt && d.State == store.StateActive:
			d.State = store.StateDisabled
			if err := c.Store.UpdateDevice(ctx, &d); err == nil {
				c.Audit(ctx, SystemActor, "wgconfig.expire", "device", d.ID, d.Name, nil)
				changed = true
			}
		}
		if d.Kind == store.KindNative && !d.KeyExpiryDisabled && d.KeyExpiresAt > 0 && !warned[d.ID] &&
			time.Until(time.Unix(d.KeyExpiresAt, 0)) < 7*24*time.Hour {
			warned[d.ID] = true
			c.Bus.Publish(EvDeviceKeyExpiry, map[string]any{"id": d.ID, "name": d.Name, "expires_at": d.KeyExpiresAt})
		}
	}
	if changed {
		c.Coord.Trigger()
	}
}

// ---------- helpers ----------

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// usedAddrs returns all assigned IPv4 addresses.
func usedAddrs(devs []store.Device) map[netip.Addr]bool {
	m := make(map[netip.Addr]bool, len(devs))
	for _, d := range devs {
		if a, err := netip.ParseAddr(d.IPv4); err == nil {
			m[a] = true
		}
	}
	return m
}
