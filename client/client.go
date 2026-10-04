// Package client is Gorget's cross-platform client core. It is embedded in the
// Android app (via gomobile) and the desktop daemon. Platforms supply a
// Platform implementation that configures the OS VPN interface and excludes
// the client's own sockets from the tunnel.
package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/anand34577/gorget/client/disco"
	"github.com/anand34577/gorget/client/magicsock"
	"github.com/anand34577/gorget/client/posture"
	"github.com/anand34577/gorget/client/pq"
	"github.com/anand34577/gorget/client/relayclient"
	pb "github.com/anand34577/gorget/gen/gorget/v1"
	"github.com/anand34577/gorget/internal/dnsserver"
)

// Version of the client core.
var Version = "0.5.1"

// Platform adapts the client to an operating system.
type Platform interface {
	// ApplyTUN configures the OS VPN interface. Return a new tun.Device when the
	// platform (re)created the interface, or nil to keep the current one.
	ApplyTUN(cfg TUNConfig) (tun.Device, error)
	// Protect excludes a socket from the VPN so control and peer traffic don't loop.
	Protect(fd uintptr) bool
}

type HostInfo struct {
	Hostname  string
	OS        string
	OSVersion string
	Arch      string
}

type Options struct {
	DataDir  string
	Platform Platform
	Log      *slog.Logger
	Host     HostInfo
	// TLSConfig overrides server certificate verification (internal CA, tests).
	TLSConfig *tls.Config
	// DisablePortMapping never asks the router to forward ports (tests, embedded uses).
	DisablePortMapping bool
	// BlockDirect forces relayed connections (tests / very restrictive networks).
	BlockDirect bool
	Ephemeral   bool
	// ManualConnect stops sign-in from connecting automatically (Android starts the
	// tunnel from its VpnService after the user grants VPN permission).
	ManualConnect bool
}

// States reported to the UI.
const (
	StateNoServer        = "no_server"
	StateNeedsLogin      = "needs_login"
	StateStopped         = "stopped"
	StateConnecting      = "connecting"
	StateRunning         = "running"
	StatePendingApproval = "pending_approval"
	StateExpired         = "expired"
	StateDisabled        = "disabled"
	// StateBlocked: the server refuses network access until the device meets its security rules.
	StateBlocked = "blocked"
)

type SelfView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	FQDN         string `json:"fqdn"`
	IPv4         string `json:"ipv4"`
	IPv6         string `json:"ipv6"`
	User         string `json:"user"`
	KeyExpiresAt int64  `json:"key_expires_at"`
}

type PeerView struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	FQDN          string   `json:"fqdn"`
	IPv4          string   `json:"ipv4"`
	IPv6          string   `json:"ipv6"`
	OS            string   `json:"os"`
	User          string   `json:"user"`
	Tags          []string `json:"tags"`
	Online        bool     `json:"online"`
	LastSeen      int64    `json:"last_seen"`
	Direct        bool     `json:"direct"`
	Endpoint      string   `json:"endpoint,omitempty"`
	LatencyMs     int      `json:"latency_ms"`
	RxBytes       uint64   `json:"rx_bytes"`
	TxBytes       uint64   `json:"tx_bytes"`
	LastHandshake int64    `json:"last_handshake"`
	ExitNode      bool     `json:"exit_node"`
	Gateway       bool     `json:"gateway"`
	Routes        []string `json:"routes"`
	// PostQuantum is true when the connection to this device is protected by a post-quantum key.
	PostQuantum bool `json:"post_quantum"`
}

type Status struct {
	State       string     `json:"state"`
	Error       string     `json:"error,omitempty"`
	Notice      string     `json:"notice,omitempty"`
	ServerURL   string     `json:"server_url"`
	NetworkName string     `json:"network_name"`
	Domain      string     `json:"domain"`
	LoginURL    string     `json:"login_url,omitempty"`
	LoginCode   string     `json:"login_code,omitempty"`
	Self        *SelfView  `json:"self,omitempty"`
	Peers       []PeerView `json:"peers"`
	ExitNodeID  string     `json:"exit_node_id"`
	ExitWarning string     `json:"exit_warning,omitempty"`
	Prefs       Prefs      `json:"prefs"`
	RelayURL    string     `json:"relay_url,omitempty"`
	RelayUp     bool       `json:"relay_connected"`
	// RelayTransport is how the home relay is reached: "udp" or "websocket".
	RelayTransport string   `json:"relay_transport,omitempty"`
	Endpoints      []string `json:"endpoints"`
	CanChooseExit  bool     `json:"can_choose_exit"`
	ForcedExit     bool     `json:"forced_exit"`
	KillSwitch     bool     `json:"kill_switch_enforced"`
	Version        string   `json:"version"`
}

type Client struct {
	opts  Options
	log   *slog.Logger
	state *state

	mu         sync.Mutex
	status     Status
	listeners  []func(Status)
	ctl        *control
	runCancel  context.CancelFunc
	runDone    chan struct{}
	loginStop  context.CancelFunc
	nm         *pb.NetworkMap
	eng        *engine
	magic      *magicsock.Conn
	pqm        *pq.Manager
	router     *relayclient.Router
	relayStop  context.CancelFunc
	localAddr  []netip.Addr
	kick       chan struct{}
	statusKick chan struct{}
}

func New(opts Options) (*Client, error) {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Host.OS == "" {
		opts.Host.OS = runtime.GOOS
	}
	if opts.Host.Arch == "" {
		opts.Host.Arch = runtime.GOARCH
	}
	st, err := loadState(opts.DataDir)
	if err != nil {
		return nil, err
	}
	c := &Client{opts: opts, log: opts.Log, state: st, kick: make(chan struct{}, 1), statusKick: make(chan struct{}, 1)}
	p := st.get()
	c.status = Status{State: StateNoServer, ServerURL: p.ServerURL, Prefs: p.Prefs, Peers: []PeerView{}, Endpoints: []string{}, Version: Version}
	if p.ServerURL != "" {
		c.ctl = newControl(p.ServerURL, newHTTPClient(c.dialer(), opts.TLSConfig), st.machineKey())
		c.status.State = StateNeedsLogin
		if p.Registered {
			c.status.State = StateStopped
		}
	}
	return c, nil
}

// ---------- platform helpers ----------

func (c *Client) protect(network, address string, rc syscall.RawConn) error {
	if c.opts.Platform == nil {
		return nil
	}
	var ok bool
	if err := rc.Control(func(fd uintptr) { ok = c.opts.Platform.Protect(fd) }); err != nil {
		return err
	}
	if !ok {
		return errors.New("could not exclude socket from the VPN")
	}
	return nil
}

func (c *Client) dialer() *net.Dialer {
	return &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: c.protect}
}

func (c *Client) listenPacket(network, address string) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: c.protect}
	return lc.ListenPacket(context.Background(), network, address)
}

func (c *Client) host() *pb.HostInfo {
	h := c.opts.Host
	info := &pb.HostInfo{Hostname: h.Hostname, Os: h.OS, OsVersion: h.OSVersion, ClientVersion: Version, Arch: h.Arch}
	if h.OS == "android" {
		info.DiskEncrypted = pb.Tri_TRI_YES // file-based encryption is mandatory since Android 10 (our minimum is 8; see docs)
		return info
	}
	ps := posture.Detect()
	info.DiskEncrypted, info.FirewallEnabled = pb.Tri(ps.DiskEncrypted), pb.Tri(ps.Firewall)
	return info
}

// ---------- status ----------

// OnStatus registers a listener called on every status change.
func (c *Client) OnStatus(f func(Status)) {
	c.mu.Lock()
	c.listeners = append(c.listeners, f)
	c.mu.Unlock()
}

func (c *Client) setStatus(f func(s *Status)) {
	c.mu.Lock()
	f(&c.status)
	s := c.status
	ls := append([]func(Status){}, c.listeners...)
	c.mu.Unlock()
	for _, l := range ls {
		l(s)
	}
}

// Status returns a snapshot including live peer path information.
func (c *Client) Status() Status {
	c.mu.Lock()
	s := c.status
	nm, eng, magic, router, pqm := c.nm, c.eng, c.magic, c.router, c.pqm
	c.mu.Unlock()
	s.Prefs = c.state.get().Prefs
	s.Peers = []PeerView{}
	s.Endpoints = []string{}
	if nm == nil {
		return s
	}
	var paths map[magicsock.Key]magicsock.PeerStatus
	var stats map[string]PeerStats
	if magic != nil {
		paths = magic.Status()
		for _, ep := range magic.Endpoints() {
			s.Endpoints = append(s.Endpoints, ep.String())
		}
	}
	if eng != nil {
		stats = eng.stats()
		eng.mu.Lock()
		s.ExitWarning = eng.exitWarn
		eng.mu.Unlock()
	}
	if router != nil {
		s.RelayURL = router.Home()
		s.RelayUp, s.RelayTransport = router.HomeConnected()
	}
	if self := nm.Self; self != nil {
		s.Self = &SelfView{ID: self.Id, Name: self.Name, FQDN: self.Fqdn, IPv4: strings.Split(self.Ipv4, "/")[0], IPv6: strings.Split(self.Ipv6, "/")[0], User: self.UserEmail, KeyExpiresAt: self.KeyExpiresAtUnix}
	}
	if d := nm.Dns; d != nil {
		s.Domain = d.Domain
	}
	if st := nm.Settings; st != nil {
		s.CanChooseExit = st.AllowUserExitNodeChoice && st.ForcedExitNodeId == ""
		s.ForcedExit = st.ForcedExitNodeId != ""
		s.KillSwitch = st.KillSwitchEnforced
	}
	exit, _ := exitNode(nm, s.Prefs)
	if exit != nil {
		s.ExitNodeID = exit.Id
	}
	for _, p := range nm.Peers {
		v := PeerView{ID: p.Id, Name: p.Name, FQDN: p.Fqdn, OS: p.Os, User: p.User, Tags: p.Tags, Online: p.Online, LastSeen: p.LastSeenUnix,
			ExitNode: p.IsExitNode, Gateway: p.IsGateway, Routes: p.SubnetRoutes}
		for _, a := range p.Addresses {
			ip := strings.Split(a, "/")[0]
			if strings.Contains(ip, ":") {
				v.IPv6 = ip
			} else {
				v.IPv4 = ip
			}
		}
		if wk, ok := decodeKey(p.WireguardPublicKey); ok {
			v.PostQuantum = pqm != nil && pqm.Has(wk)
			if ps, ok := paths[wk]; ok {
				v.Direct, v.Endpoint, v.LatencyMs = ps.Direct, ps.Endpoint, ps.LatencyMs
			}
			if st, ok := stats[hex.EncodeToString(wk[:])]; ok {
				v.RxBytes, v.TxBytes, v.LastHandshake = st.RxBytes, st.TxBytes, st.LastHandshake
			}
		}
		if v.Tags == nil {
			v.Tags = []string{}
		}
		if v.Routes == nil {
			v.Routes = []string{}
		}
		s.Peers = append(s.Peers, v)
	}
	sort.Slice(s.Peers, func(i, j int) bool {
		if s.Peers[i].Online != s.Peers[j].Online {
			return s.Peers[i].Online
		}
		return s.Peers[i].Name < s.Peers[j].Name
	})
	return s
}

// ---------- server & login ----------

// NormalizeServerURL turns "vpn.example.com" into "https://vpn.example.com".
func NormalizeServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("enter your server's address")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("that doesn't look like a server address")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

// SetServer validates and stores the server URL. Changing servers forgets the old registration.
func (c *Client) SetServer(ctx context.Context, raw string) (string, error) {
	u, err := NormalizeServerURL(raw)
	if err != nil {
		return "", err
	}
	ctl := newControl(u, newHTTPClient(c.dialer(), c.opts.TLSConfig), c.state.machineKey())
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info, err := ctl.serverInfo(cctx)
	if err != nil {
		return "", errors.New(friendly(err))
	}
	c.Down()
	prev := c.state.get().ServerURL
	if err := c.state.update(func(p *persisted) {
		if p.ServerURL != u {
			p.Registered, p.DeviceID = false, ""
		}
		p.ServerURL = u
	}); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.ctl = ctl
	c.mu.Unlock()
	reg := c.state.get().Registered
	c.setStatus(func(s *Status) {
		s.ServerURL, s.NetworkName, s.Error = u, info.NetworkName, ""
		s.State = StateNeedsLogin
		if reg && prev == u {
			s.State = StateStopped
		}
	})
	return info.NetworkName, nil
}

func (c *Client) getControl() (*control, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctl == nil {
		return nil, errors.New("set the server address first")
	}
	return c.ctl, nil
}

// LoginInteractive starts a browser sign-in. It returns the URL to open; when the
// user approves, the device registers and connects automatically.
func (c *Client) LoginInteractive(ctx context.Context) (string, string, error) {
	ctl, err := c.getControl()
	if err != nil {
		return "", "", err
	}
	h := c.opts.Host
	r, err := ctl.rpc.StartInteractiveLogin(ctx, connect.NewRequest(&pb.StartInteractiveLoginRequest{MachineKey: ctl.mkPub, Hostname: h.Hostname, Os: h.OS}))
	if err != nil {
		return "", "", errors.New(friendly(err))
	}
	m := r.Msg
	c.mu.Lock()
	if c.loginStop != nil {
		c.loginStop()
	}
	lctx, cancel := context.WithDeadline(context.Background(), time.Unix(m.ExpiresAtUnix, 0))
	c.loginStop = cancel
	c.mu.Unlock()
	c.setStatus(func(s *Status) {
		s.LoginURL, s.LoginCode, s.State, s.Error = m.LoginUrl, m.UserCode, StateNeedsLogin, ""
	})
	go c.pollLogin(lctx, ctl, m)
	return m.LoginUrl, m.UserCode, nil
}

func (c *Client) pollLogin(ctx context.Context, ctl *control, m *pb.StartInteractiveLoginResponse) {
	interval := time.Duration(max(m.PollIntervalSeconds, 1)) * time.Second
	for {
		select {
		case <-ctx.Done():
			c.setStatus(func(s *Status) {
				if s.LoginURL == m.LoginUrl {
					s.LoginURL, s.LoginCode = "", ""
					if s.State == StateNeedsLogin {
						s.Error = "The sign-in link expired. Start again."
					}
				}
			})
			return
		case <-time.After(interval):
		}
		r, err := ctl.rpc.PollInteractiveLogin(ctx, connect.NewRequest(&pb.PollInteractiveLoginRequest{LoginId: m.LoginId, MachineKey: ctl.mkPub}))
		if err != nil {
			continue
		}
		switch r.Msg.Status {
		case pb.LoginStatus_LOGIN_STATUS_APPROVED:
			err := c.register(ctx, &pb.RegisterRequest{LoginToken: r.Msg.LoginToken})
			c.setStatus(func(s *Status) {
				s.LoginURL, s.LoginCode = "", ""
				if err != nil {
					s.Error = friendly(err)
				}
			})
			if err == nil {
				c.setStatus(func(s *Status) { s.State = StateStopped })
				if !c.opts.ManualConnect {
					_ = c.Up()
				}
			}
			return
		case pb.LoginStatus_LOGIN_STATUS_DENIED:
			c.setStatus(func(s *Status) { s.LoginURL, s.LoginCode, s.Error = "", "", "The sign-in was refused." })
			return
		case pb.LoginStatus_LOGIN_STATUS_EXPIRED:
			c.setStatus(func(s *Status) { s.LoginURL, s.LoginCode, s.Error = "", "", "The sign-in link expired. Start again." })
			return
		}
	}
}

// LoginWithSetupKey registers using a setup key and connects.
func (c *Client) LoginWithSetupKey(ctx context.Context, key string) error {
	if err := c.register(ctx, &pb.RegisterRequest{SetupKey: strings.TrimSpace(key)}); err != nil {
		return errors.New(friendly(err))
	}
	c.setStatus(func(s *Status) { s.State, s.Error = StateStopped, "" })
	if c.opts.ManualConnect {
		return nil
	}
	return c.Up()
}

func (c *Client) register(ctx context.Context, req *pb.RegisterRequest) error {
	ctl, err := c.getControl()
	if err != nil {
		return err
	}
	req.WireguardPublicKey = c.state.wgKey().PublicKey().String()
	req.Host = c.host()
	req.Name = c.opts.Host.Hostname
	req.Ephemeral = c.opts.Ephemeral
	resp, err := ctl.register(ctx, req)
	if err != nil {
		return err
	}
	if err := c.state.update(func(p *persisted) { p.Registered, p.DeviceID = true, resp.DeviceId }); err != nil {
		return err
	}
	c.log.Info("device registered", "id", resp.DeviceId, "ip", resp.Ipv4, "state", resp.State)
	return nil
}

// Logout signs this device out of the network and forgets its keys.
func (c *Client) Logout(ctx context.Context) error {
	ctl, _ := c.getControl()
	if ctl != nil && ctl.Token() != "" {
		_ = ctl.logout(ctx)
	}
	c.Down()
	if err := c.state.update(func(p *persisted) {
		p.Registered, p.DeviceID = false, ""
		p.Prefs.WantRunning = false
	}); err != nil {
		return err
	}
	if err := c.state.rotateWGKey(); err != nil {
		return err
	}
	if ctl != nil {
		ctl.clearToken()
	}
	c.setStatus(func(s *Status) { s.State, s.Error, s.Notice = StateNeedsLogin, "", "" })
	return nil
}

// SetPrefs updates preferences and reapplies them immediately.
func (c *Client) SetPrefs(p Prefs) error {
	if err := c.state.update(func(s *persisted) { s.Prefs = p }); err != nil {
		return err
	}
	c.mu.Lock()
	nm, eng := c.nm, c.eng
	c.mu.Unlock()
	if nm != nil && eng != nil {
		if err := eng.apply(nm, p); err != nil {
			return err
		}
		c.requestStatusUpdate()
	}
	c.setStatus(func(s *Status) { s.Prefs = p })
	return nil
}

// RefreshTUN re-creates the OS VPN interface with the current configuration
// (e.g. after the platform's per-app rules changed).
func (c *Client) RefreshTUN() error {
	c.mu.Lock()
	nm, eng := c.nm, c.eng
	c.mu.Unlock()
	if nm == nil || eng == nil {
		return nil
	}
	eng.mu.Lock()
	eng.tunReady = false
	eng.mu.Unlock()
	return eng.apply(nm, c.state.get().Prefs)
}

// Prefs returns the stored preferences.
func (c *Client) Prefs() Prefs { return c.state.get().Prefs }

// PeerByAddr identifies the peer that owns an overlay address (WireGuard guarantees a
// packet's source address belongs to the peer it came from).
func (c *Client) PeerByAddr(a netip.Addr) (PeerView, bool) {
	c.mu.Lock()
	nm := c.nm
	c.mu.Unlock()
	if nm == nil {
		return PeerView{}, false
	}
	a = a.Unmap()
	for _, p := range nm.Peers {
		for _, s := range p.Addresses {
			if pf, err := netip.ParsePrefix(s); err == nil && pf.Addr() == a {
				return PeerView{ID: p.Id, Name: p.Name, FQDN: p.Fqdn, User: p.User, Online: p.Online}, true
			}
		}
	}
	return PeerView{}, false
}

// SelfAddrs returns this device's overlay addresses and its owner (empty when not connected).
func (c *Client) SelfAddrs() ([]netip.Addr, string) {
	c.mu.Lock()
	nm := c.nm
	c.mu.Unlock()
	if nm == nil || nm.Self == nil {
		return nil, ""
	}
	var out []netip.Addr
	for _, s := range []string{nm.Self.Ipv4, nm.Self.Ipv6} {
		if pf, err := netip.ParsePrefix(s); err == nil {
			out = append(out, pf.Addr())
		}
	}
	return out, nm.Self.UserEmail
}

// IsRegistered reports whether the device has joined a network.
func (c *Client) IsRegistered() bool { return c.state.get().Registered }

// SetLocalAddresses supplies the device's network addresses (Android).
func (c *Client) SetLocalAddresses(addrs []netip.Addr) {
	c.mu.Lock()
	c.localAddr = addrs
	m := c.magic
	c.mu.Unlock()
	if m != nil {
		m.SetLocalAddresses(addrs)
	}
}

// NetworkChanged must be called when the device switches networks (Wi-Fi ↔ mobile data).
func (c *Client) NetworkChanged() {
	c.mu.Lock()
	m, eng := c.magic, c.eng
	c.mu.Unlock()
	if eng != nil && (runtime.GOOS == "windows" || runtime.GOOS == "darwin") {
		eng.rebind()
	}
	if eng != nil && runtime.GOOS == "darwin" {
		go eng.reapplyTUN()
	}
	if m != nil {
		m.NetworkChanged()
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// ---------- run loop ----------

// Up connects to the network (no-op if already running).
func (c *Client) Up() error {
	ctl, err := c.getControl()
	if err != nil {
		return err
	}
	if !c.state.get().Registered {
		return errors.New("sign in first")
	}
	c.mu.Lock()
	if c.runCancel != nil {
		c.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.runCancel = cancel
	done := make(chan struct{})
	c.runDone = done
	c.mu.Unlock()
	_ = c.state.update(func(p *persisted) { p.Prefs.WantRunning = true })
	c.setStatus(func(s *Status) { s.State, s.Error, s.Notice = StateConnecting, "", "" })
	go func() {
		defer close(done)
		c.run(ctx, ctl)
	}()
	return nil
}

// Down disconnects and tears down the tunnel; the user's choice is remembered.
func (c *Client) Down() { c.stop(true) }

// Shutdown stops the tunnel because the process is exiting; the preference to
// stay connected is kept so the next start reconnects.
func (c *Client) Shutdown() { c.stop(false) }

func (c *Client) stop(forget bool) {
	c.mu.Lock()
	cancel, done := c.runCancel, c.runDone
	c.runCancel, c.runDone = nil, nil
	if c.loginStop != nil {
		c.loginStop()
		c.loginStop = nil
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if forget {
		_ = c.state.update(func(p *persisted) { p.Prefs.WantRunning = false })
	}
	c.setStatus(func(s *Status) {
		if s.State == StateRunning || s.State == StateConnecting || s.State == StatePendingApproval || s.State == StateBlocked {
			s.State = StateStopped
		}
	})
}

// WantRunning reports whether the user left the VPN switched on (for always-on restore).
func (c *Client) WantRunning() bool {
	p := c.state.get()
	return p.Registered && p.Prefs.WantRunning
}

func (c *Client) run(ctx context.Context, ctl *control) {
	defer c.teardown()
	backoff := time.Second
	var lastSerial uint64
	statusStarted := false
	for ctx.Err() == nil {
		if err := c.ensureEngine(); err != nil {
			c.setStatus(func(s *Status) { s.Error = err.Error() })
			c.sleep(ctx, &backoff)
			continue
		}
		if ctl.Token() == "" {
			stop, err := c.authenticate(ctx, ctl)
			if stop {
				return
			}
			if err != nil {
				c.setStatus(func(s *Status) { s.State, s.Error = StateConnecting, friendly(err) })
				c.sleep(ctx, &backoff)
				continue
			}
		}
		if !statusStarted {
			// Also while blocked by posture rules: the server needs fresh health reports to unblock us.
			statusStarted = true
			go c.statusLoop(ctx)
		}
		start := time.Now()
		err := ctl.watch(ctx, lastSerial, func(m *pb.WatchNetworkMapResponse) {
			if s := c.handle(ctx, m); s != 0 {
				lastSerial = s
			}
		})
		if ctx.Err() != nil {
			return
		}
		if c.stopped() {
			return
		}
		var ce *connect.Error
		if errors.As(err, &ce) && ce.Code() == connect.CodeUnauthenticated || errors.Is(err, errNoToken) {
			ctl.clearToken()
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		c.log.Debug("control stream ended", "err", err)
		c.setStatus(func(s *Status) {
			if s.State == StateRunning {
				s.Notice = "Reconnecting to the server… existing connections keep working."
			} else {
				s.State = StateConnecting
			}
		})
		c.sleep(ctx, &backoff)
	}
}

func (c *Client) stopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.status.State {
	case StateExpired, StateDisabled, StateNeedsLogin:
		return true
	}
	return false
}

func (c *Client) sleep(ctx context.Context, backoff *time.Duration) {
	d := *backoff + time.Duration(rand.Int64N(int64(*backoff/2)+1))
	select {
	case <-ctx.Done():
	case <-time.After(d):
	case <-c.kick:
	}
	if *backoff < 30*time.Second {
		*backoff *= 2
	}
}

// authenticate returns stop=true when the device can't continue (expired / disabled).
func (c *Client) authenticate(ctx context.Context, ctl *control) (bool, error) {
	c.mu.Lock()
	m := c.magic
	c.mu.Unlock()
	disco := m.DiscoPublic()
	resp, err := ctl.authenticate(ctx, c.state.wgKey().PublicKey().String(), base64.StdEncoding.EncodeToString(disco[:]), c.host())
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) && ce.Code() == connect.CodeNotFound {
			_ = c.state.update(func(p *persisted) { p.Registered = false })
			c.setStatus(func(s *Status) {
				s.State, s.Error = StateNeedsLogin, "This device was removed from the network. Sign in again."
			})
			return true, nil
		}
		return false, err
	}
	switch resp.State {
	case pb.DeviceState_DEVICE_STATE_EXPIRED:
		_ = c.state.update(func(p *persisted) { p.Registered = false })
		c.setStatus(func(s *Status) { s.State, s.Error = StateExpired, "Your sign-in expired. Sign in again to reconnect." })
		return true, nil
	case pb.DeviceState_DEVICE_STATE_DISABLED:
		c.setStatus(func(s *Status) { s.State, s.Error = StateDisabled, "An administrator disabled this device." })
		return true, nil
	case pb.DeviceState_DEVICE_STATE_PENDING_APPROVAL:
		c.setStatus(func(s *Status) { s.State, s.Error = StatePendingApproval, "" })
	}
	return false, nil
}

func (c *Client) ensureEngine() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.eng != nil {
		return nil
	}
	magic, err := magicsock.New(magicsock.Options{
		Log:          c.log,
		Port:         c.state.get().ListenPort,
		ListenPacket: c.listenPacket,
		SendSignal: func(peerID string, sealed []byte) {
			ctl, err := c.getControl()
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = ctl.sendSignal(ctx, peerID, sealed)
		},
		OnEndpointsChanged: func([]netip.AddrPort) { c.requestStatusUpdate() },
		ExcludePrefixes:    c.overlayPrefixes,
		BlockDirect:        c.opts.BlockDirect,
		PortMapping:        !c.opts.DisablePortMapping && !c.state.get().Prefs.NoPortMapping && runtime.GOOS != "android",
		OnPQ: func(peer magicsock.Key, m disco.Message) {
			c.mu.Lock()
			mgr := c.pqm
			c.mu.Unlock()
			if mgr != nil && !c.state.get().Prefs.NoPostQuantum {
				mgr.Handle(peer, m)
			}
		},
	})
	if err != nil {
		return err
	}
	wk := c.state.wgKey()
	magic.SetNodeKey(wk.PublicKey())
	if len(c.localAddr) > 0 {
		magic.SetLocalAddresses(c.localAddr)
	}
	dnsSrv := dnsserver.NewWithDialer(c.log, c.dialer())
	eng, err := newEngine(c.log, c.opts.Platform, magic, dnsSrv, hex.EncodeToString(wk[:]), c.state.get().ListenPort, 1280)
	if err != nil {
		magic.Shutdown()
		return err
	}
	if p := magic.Port(); p != 0 && c.state.get().ListenPort == 0 {
		_ = c.state.update(func(s *persisted) { s.ListenPort = p })
	}
	c.magic, c.eng = magic, eng
	c.startPQ(wk.PublicKey(), magic, eng)
	return nil
}

// startPQ loads stored post-quantum keys into the engine and creates the key-exchange manager.
func (c *Client) startPQ(self wgtypes.Key, magic *magicsock.Conn, eng *engine) {
	var have []pq.Entry
	for peerB64, e := range c.state.pqKeys() {
		peer, ok1 := decodeKey(peerB64)
		psk, ok2 := decodeKey(e.Key)
		if ok1 && ok2 {
			eng.psks[hex.EncodeToString(peer[:])] = hex.EncodeToString(psk[:])
			have = append(have, pq.Entry{Peer: peer, Gen: e.Gen})
		}
	}
	c.pqm = pq.New(self, pq.Callbacks{
		Send: magic.SendPQ,
		Apply: func(peer pq.Key, psk *pq.Key, gen uint64) {
			// Persist first: after a crash the stored key must match what the peer applied.
			err := c.state.update(func(p *persisted) {
				if p.PQKeys == nil {
					p.PQKeys = map[string]pqEntry{}
				}
				p.PQKeys[base64.StdEncoding.EncodeToString(peer[:])] = pqEntry{Key: base64.StdEncoding.EncodeToString(psk[:]), Gen: gen}
			})
			eng.setPSK(peer, *psk)
			c.log.Debug("post-quantum key applied", "peer", hex.EncodeToString(peer[:4]), "gen", gen)
			if err != nil {
				c.log.Warn("could not save the post-quantum key", "err", err)
			}
			c.requestStatusUpdate()
		},
	}, have)
}

// pqEnsure starts key exchanges with online Gorget peers that don't have a key yet.
func (c *Client) pqEnsure() {
	if c.state.get().Prefs.NoPostQuantum {
		return
	}
	c.mu.Lock()
	mgr, nm := c.pqm, c.nm
	c.mu.Unlock()
	if mgr == nil || nm == nil {
		return
	}
	for _, p := range nm.Peers {
		if p.IsGateway || !p.Online || p.DiscoPublicKey == "" {
			continue
		}
		if wk, ok := decodeKey(p.WireguardPublicKey); ok {
			mgr.Ensure(wk)
		}
	}
}

func (c *Client) overlayPrefixes() []netip.Prefix {
	c.mu.Lock()
	nm := c.nm
	c.mu.Unlock()
	var out []netip.Prefix
	if nm != nil && nm.Self != nil {
		out = append(out, prefixes([]string{nm.Self.NetworkCidrV4, nm.Self.NetworkCidrV6})...)
	}
	return append(out, netip.MustParsePrefix("100.100.100.100/32"))
}

func (c *Client) teardown() {
	c.mu.Lock()
	eng, magic, relayStop := c.eng, c.magic, c.relayStop
	c.eng, c.magic, c.router, c.relayStop, c.nm, c.pqm = nil, nil, nil, nil, nil, nil
	c.mu.Unlock()
	if relayStop != nil {
		relayStop()
	}
	if eng != nil {
		eng.close()
	}
	if magic != nil {
		magic.Shutdown()
	}
	if c.opts.Platform != nil {
		_, _ = c.opts.Platform.ApplyTUN(TUNConfig{}) // empty config = tear the interface down
	}
}

// handle processes one stream message; returns the new serial (0 if unchanged).
func (c *Client) handle(ctx context.Context, m *pb.WatchNetworkMapResponse) uint64 {
	switch u := m.Update.(type) {
	case *pb.WatchNetworkMapResponse_Full:
		c.applyMap(ctx, u.Full)
		return u.Full.Serial
	case *pb.WatchNetworkMapResponse_Delta:
		c.mu.Lock()
		cur := c.nm
		c.mu.Unlock()
		if cur == nil {
			return 0
		}
		c.applyMap(ctx, applyDelta(cur, u.Delta))
		return u.Delta.Serial
	case *pb.WatchNetworkMapResponse_Signal:
		c.mu.Lock()
		magic := c.magic
		c.mu.Unlock()
		if magic != nil {
			magic.HandleSignal(u.Signal.Sealed)
		}
	case *pb.WatchNetworkMapResponse_Notice:
		n := u.Notice
		switch n.Kind {
		case pb.ServerNotice_KIND_APPROVAL_PENDING:
			c.setStatus(func(s *Status) { s.State, s.Notice = StatePendingApproval, n.Message })
		case pb.ServerNotice_KIND_POSTURE_FAILED:
			c.setStatus(func(s *Status) { s.State, s.Error = StateBlocked, n.Message })
		case pb.ServerNotice_KIND_DEVICE_DISABLED:
			c.setStatus(func(s *Status) { s.State, s.Error = StateDisabled, n.Message })
		case pb.ServerNotice_KIND_KEY_EXPIRED:
			_ = c.state.update(func(p *persisted) { p.Registered = false })
			c.setStatus(func(s *Status) { s.State, s.Error = StateExpired, n.Message })
		case pb.ServerNotice_KIND_LOGGED_OUT:
			_ = c.state.update(func(p *persisted) { p.Registered = false })
			c.setStatus(func(s *Status) { s.State, s.Error = StateNeedsLogin, n.Message })
		default:
			c.setStatus(func(s *Status) { s.Notice = n.Message })
		}
	}
	return 0
}

func (c *Client) applyMap(ctx context.Context, nm *pb.NetworkMap) {
	c.mu.Lock()
	prev := c.nm
	c.nm = nm
	eng, magic := c.eng, c.magic
	c.mu.Unlock()
	if eng == nil {
		return
	}
	if err := eng.apply(nm, c.state.get().Prefs); err != nil {
		c.log.Error("apply network map", "err", err)
		c.setStatus(func(s *Status) { s.Error = err.Error() })
		return
	}
	if prev == nil || !equalStrings(prev.StunServers, nm.StunServers) {
		magic.SetSTUNServers(nm.StunServers)
	}
	if prev == nil || !relaysEqual(prev.Relays, nm.Relays) {
		c.syncRelays(ctx, nm.Relays)
	}
	go c.pqEnsure()
	c.setStatus(func(s *Status) {
		s.State, s.Error, s.Notice = StateRunning, "", ""
		if nm.Dns != nil {
			s.Domain = nm.Dns.Domain
		}
	})
}

func relaysEqual(a, b []*pb.RelayServer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Url != b[i].Url || a[i].UdpAddr != b[i].UdpAddr || a[i].Region != b[i].Region {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// syncRelays starts the relay router on first use and gives it the server's relay list.
func (c *Client) syncRelays(ctx context.Context, relays []*pb.RelayServer) {
	c.mu.Lock()
	ctl, magic, router := c.ctl, c.magic, c.router
	c.mu.Unlock()
	if ctl == nil || magic == nil {
		return
	}
	if router == nil {
		d := c.dialer()
		rctx, cancel := context.WithCancel(ctx)
		router = relayclient.NewRouter(rctx, relayclient.Config{
			Token:        ctl.Token,
			HTTP:         newHTTPClient(d, c.opts.TLSConfig),
			ListenPacket: c.listenPacket,
			Dial:         d.DialContext,
			BlockUDP:     c.opts.BlockDirect,
			Log:          c.log,
		})
		go router.Run(rctx)
		magic.SetRelay(router)
		c.mu.Lock()
		c.router, c.relayStop = router, cancel
		c.mu.Unlock()
	}
	list := make([]relayclient.Relay, 0, len(relays))
	for _, r := range relays {
		list = append(list, relayclient.Relay{Region: r.Region, Name: r.Name, URL: r.Url, UDPAddr: r.UdpAddr})
	}
	router.SetRelays(list)
}

func (c *Client) requestStatusUpdate() {
	select {
	case c.statusKick <- struct{}{}:
	default:
	}
}

// statusLoop reports endpoints, advertised routes and traffic to the server.
func (c *Client) statusLoop(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	pqT := time.NewTicker(20 * time.Second)
	defer pqT.Stop()
	send := func() {
		c.mu.Lock()
		magic, ctl, eng := c.magic, c.ctl, c.eng
		c.mu.Unlock()
		if magic == nil || ctl == nil || eng == nil {
			return
		}
		prefs := c.state.get().Prefs
		req := &pb.UpdateStatusRequest{Host: c.host(), AdvertisedRoutes: prefs.AdvertiseRoutes, AdvertiseExitNode: prefs.AdvertiseExitNode, SelectedExitNodeId: prefs.ExitNodeID}
		for _, ep := range magic.Endpoints() {
			req.Endpoints = append(req.Endpoints, ep.String())
		}
		c.mu.Lock()
		if c.router != nil {
			req.HomeRelay = c.router.Home() // peers send us traffic through this relay
		}
		nm := c.nm
		c.mu.Unlock()
		paths := magic.Status()
		stats := eng.stats()
		if nm != nil {
			for _, p := range nm.Peers {
				wk, ok := decodeKey(p.WireguardPublicKey)
				if !ok {
					continue
				}
				st := stats[hex.EncodeToString(wk[:])]
				ps := paths[wk]
				req.Peers = append(req.Peers, &pb.PeerStatus{PeerId: p.Id, Direct: ps.Direct, LatencyMs: uint32(ps.LatencyMs), RxBytes: st.RxBytes, TxBytes: st.TxBytes, LastHandshakeUnix: st.LastHandshake})
			}
		}
		uctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := ctl.updateStatus(uctx, req); err != nil {
			c.log.Debug("status update failed", "err", err)
		}
	}
	send()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pqT.C:
			c.pqEnsure()
			continue
		case <-t.C:
		case <-c.statusKick:
			time.Sleep(500 * time.Millisecond) // coalesce bursts
		}
		send()
	}
}
