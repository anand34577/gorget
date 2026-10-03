package core

import (
	"context"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anand34577/gorget/internal/ipam"
	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/store"
)

// Snapshot is an immutable, fully-resolved view of the network at one point in time.
type Snapshot struct {
	Serial    uint64
	BuiltAt   time.Time
	Settings  AllSettings
	Plan      ipam.Plan
	Devices   map[string]*store.Device
	Users     map[string]*store.User
	Groups    map[string][]string // name -> user IDs
	Routes    map[string][]store.Route
	Policy    *policy.Policy
	PolicyVer int64
	PolicyErr error
	Compiled  *policy.Compiled
	Env       policy.Env
	GatewayID string
	// Primary router per route CIDR (HA failover).
	PrimaryRoute map[netip.Prefix]string
	// Active node IDs (eligible to be in network maps).
	Active map[string]bool
	// Posture lists the rules each non-compliant device breaks (blocked when the mode is enforce).
	Posture map[string][]string
	Online  map[string]bool
	// WireGuard public key (base64) -> device ID.
	ByWGKey  map[string]string
	nextWake time.Time
}

// Watcher receives change notifications for one connected device session.
type Watcher struct {
	DeviceID string
	Notify   chan struct{}
	Signals  chan Signal
	Notices  chan Notice
}

type Signal struct {
	FromID    string
	FromDisco string
	Sealed    []byte
}

type Notice struct {
	Kind    string // disabled, expired, logged_out, pending, message
	Message string
}

type Coordinator struct {
	c       *Core
	trigger chan struct{}

	mu       sync.RWMutex
	snap     *Snapshot
	serial   uint64
	watchers map[string]map[*Watcher]struct{}
	online   map[string]int
	subs     []chan *Snapshot
}

func newCoordinator(c *Core) *Coordinator {
	return &Coordinator{
		c:        c,
		trigger:  make(chan struct{}, 1),
		watchers: map[string]map[*Watcher]struct{}{},
		online:   map[string]int{},
	}
}

// Trigger schedules a rebuild (coalesced) here and on the other cluster instances.
func (co *Coordinator) Trigger() {
	co.triggerLocal()
	if cl := co.c.cluster; cl != nil {
		cl.Changed()
	}
}

// triggerLocal rebuilds only this instance's view (used when another instance made the change).
func (co *Coordinator) triggerLocal() {
	select {
	case co.trigger <- struct{}{}:
	default:
	}
}

// Snapshot returns the current snapshot (never nil after Core.New).
func (co *Coordinator) Snapshot() *Snapshot {
	co.mu.RLock()
	defer co.mu.RUnlock()
	return co.snap
}

// Subscribe returns a channel receiving every new snapshot (latest-wins).
func (co *Coordinator) Subscribe() <-chan *Snapshot {
	ch := make(chan *Snapshot, 1)
	co.mu.Lock()
	co.subs = append(co.subs, ch)
	if co.snap != nil {
		ch <- co.snap
	}
	co.mu.Unlock()
	return ch
}

func (co *Coordinator) run(ctx context.Context) {
	const debounce = 150 * time.Millisecond
	var wake <-chan time.Time
	var timer *time.Timer
	for {
		if s := co.Snapshot(); s != nil && !s.nextWake.IsZero() {
			d := time.Until(s.nextWake)
			if d < time.Second {
				d = time.Second
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(d)
			wake = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-co.trigger:
			// Coalesce bursts of changes.
			time.Sleep(debounce)
			select {
			case <-co.trigger:
			default:
			}
		case <-wake:
			wake = nil
		}
		if err := co.rebuild(ctx); err != nil {
			co.c.Log.Error("network state rebuild failed", "err", err)
		}
	}
}

func (co *Coordinator) rebuild(ctx context.Context) error {
	c := co.c
	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return err
	}
	users, err := c.Store.ListUsers(ctx)
	if err != nil {
		return err
	}
	groups, err := c.Store.ListGroups(ctx)
	if err != nil {
		return err
	}
	routes, err := c.Store.ListRoutes(ctx)
	if err != nil {
		return err
	}
	pv, err := c.Store.CurrentPolicy(ctx)
	if err != nil {
		return err
	}
	settings := c.Settings()
	plan := planFrom(settings.Network)
	now := time.Now()

	s := &Snapshot{
		BuiltAt:      now,
		Settings:     settings,
		Plan:         plan,
		Devices:      map[string]*store.Device{},
		Users:        map[string]*store.User{},
		Groups:       map[string][]string{},
		Routes:       map[string][]store.Route{},
		PolicyVer:    pv.Version,
		GatewayID:    c.GatewayID(),
		PrimaryRoute: map[netip.Prefix]string{},
		Active:       map[string]bool{},
		Posture:      map[string][]string{},
		Online:       map[string]bool{},
		ByWGKey:      map[string]string{},
	}
	for i := range users {
		s.Users[users[i].ID] = &users[i]
	}
	for _, g := range groups {
		s.Groups[g.Name] = g.Members
	}
	for _, r := range routes {
		s.Routes[r.DeviceID] = append(s.Routes[r.DeviceID], r)
	}
	co.mu.RLock()
	for id, n := range co.online {
		if n > 0 {
			s.Online[id] = true
		}
	}
	co.mu.RUnlock()
	if cl := c.cluster; cl != nil {
		for id := range cl.RemoteOnline() {
			s.Online[id] = true
		}
	}
	if s.GatewayID != "" {
		s.Online[s.GatewayID] = true
	}

	s.Policy, s.PolicyErr = policy.Parse(pv.Document)
	if s.PolicyErr != nil {
		c.Log.Error("stored policy is invalid; denying all traffic", "version", pv.Version, "err", s.PolicyErr)
		s.Policy = &policy.Policy{}
	}

	var nodes []policy.Node
	var nextWake time.Time
	wakeAt := func(t time.Time) {
		if t.After(now) && (nextWake.IsZero() || t.Before(nextWake)) {
			nextWake = t
		}
	}
	for i := range devs {
		d := &devs[i]
		s.Devices[d.ID] = d
		s.ByWGKey[d.WGPublicKey] = d.ID
		if d.Kind == store.KindWireGuard && c.wgOnline(d) {
			s.Online[d.ID] = true // so route failover can prefer a router that is really up
		}
		if !deviceActive(d, now) {
			continue
		}
		if d.Kind == store.KindNative && !d.KeyExpiryDisabled && d.KeyExpiresAt > 0 {
			wakeAt(time.Unix(d.KeyExpiresAt, 0))
		}
		if d.Kind == store.KindWireGuard && d.ExpiresAt > 0 {
			wakeAt(time.Unix(d.ExpiresAt, 0))
		}
		if d.Kind == store.KindWireGuard && s.GatewayID == "" {
			continue // no gateway to serve it
		}
		if fails := PostureFailures(settings.Posture, d, co.c.Geo.Country(d.PublicIP)); len(fails) > 0 {
			s.Posture[d.ID] = fails
			if settings.Posture.Mode == PostureEnforce {
				continue
			}
		}
		s.Active[d.ID] = true
		n := policy.Node{ID: d.ID, Name: d.Name, Kind: d.Kind, Tags: d.Tags, ExitNode: d.ExitAdvertised && d.ExitApproved}
		if d.UserID.Valid {
			n.OwnerID = d.UserID.String
			if u := s.Users[n.OwnerID]; u != nil {
				n.OwnerEmail = u.Email
			}
		}
		n.Addrs = deviceAddrs(d, settings.Network.IPv6On)
		for _, r := range s.Routes[d.ID] {
			if r.Approved && r.Enabled && r.Advertised {
				if p, err := netip.ParsePrefix(r.CIDR); err == nil {
					n.Routes = append(n.Routes, p.Masked())
				}
			}
		}
		nodes = append(nodes, n)
	}
	// Approved temporary access becomes policy rules that expire by themselves.
	if grants, err := c.Store.ActiveAccessGrants(ctx, now.Unix()); err == nil && len(grants) > 0 {
		s.Policy.ACLs = append(append([]policy.Rule(nil), s.Policy.ACLs...), accessRules(grants)...)
		for _, g := range grants {
			wakeAt(time.Unix(g.GrantedUntil, 0))
		}
	}
	overlay := []netip.Prefix{plan.IPv4}
	if settings.Network.IPv6On {
		overlay = append(overlay, plan.IPv6)
	}
	s.Env = policy.Env{Nodes: nodes, Groups: s.Groups, Overlay: overlay, Now: now}
	s.Compiled = s.Policy.Compile(s.Env)
	if !s.Compiled.NextExpiry.IsZero() {
		wakeAt(s.Compiled.NextExpiry)
	}
	s.nextWake = nextWake

	// HA route selection: lowest priority value among online routers, then oldest.
	type cand struct {
		dev    string
		prio   int
		online bool
		at     int64
	}
	cands := map[netip.Prefix][]cand{}
	for _, n := range nodes {
		for _, r := range s.Routes[n.ID] {
			if !(r.Approved && r.Enabled && r.Advertised) {
				continue
			}
			p, err := netip.ParsePrefix(r.CIDR)
			if err != nil {
				continue
			}
			cands[p.Masked()] = append(cands[p.Masked()], cand{n.ID, r.Priority, s.Online[n.ID], r.CreatedAt})
		}
	}
	for p, cs := range cands {
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].online != cs[j].online {
				return cs[i].online
			}
			if cs[i].prio != cs[j].prio {
				return cs[i].prio < cs[j].prio
			}
			return cs[i].at < cs[j].at
		})
		s.PrimaryRoute[p] = cs[0].dev
	}

	co.mu.Lock()
	co.serial++
	s.Serial = co.serial
	co.snap = s
	for _, ws := range co.watchers {
		for w := range ws {
			select {
			case w.Notify <- struct{}{}:
			default:
			}
		}
	}
	for _, ch := range co.subs {
		select {
		case <-ch:
		default:
		}
		ch <- s
	}
	co.mu.Unlock()
	return nil
}

func deviceActive(d *store.Device, now time.Time) bool {
	if d.State != store.StateActive {
		return false
	}
	if d.Kind == store.KindNative && !d.KeyExpiryDisabled && d.KeyExpiresAt > 0 && now.Unix() >= d.KeyExpiresAt {
		return false
	}
	if d.Kind == store.KindWireGuard && d.ExpiresAt > 0 && now.Unix() >= d.ExpiresAt {
		return false
	}
	return true
}

func deviceAddrs(d *store.Device, v6 bool) []netip.Prefix {
	var out []netip.Prefix
	if a, err := netip.ParseAddr(d.IPv4); err == nil {
		out = append(out, netip.PrefixFrom(a, 32))
	}
	if v6 {
		if a, err := netip.ParseAddr(d.IPv6); err == nil {
			out = append(out, netip.PrefixFrom(a, 128))
		}
	}
	return out
}

// ---------- watchers & presence ----------

// Watch registers a session for deviceID. The returned cancel must be called.
func (co *Coordinator) Watch(deviceID string) (*Watcher, func()) {
	w := &Watcher{
		DeviceID: deviceID,
		Notify:   make(chan struct{}, 1),
		Signals:  make(chan Signal, 128),
		Notices:  make(chan Notice, 4),
	}
	co.mu.Lock()
	if co.watchers[deviceID] == nil {
		co.watchers[deviceID] = map[*Watcher]struct{}{}
	}
	co.watchers[deviceID][w] = struct{}{}
	co.online[deviceID]++
	wentOnline := co.online[deviceID] == 1
	co.mu.Unlock()
	if wentOnline {
		if cl := co.c.cluster; cl != nil {
			cl.SetPresence(deviceID, true)
		}
		co.c.Bus.Publish(EvDeviceOnline, map[string]string{"id": deviceID})
		co.Trigger()
	}
	var once sync.Once
	return w, func() {
		once.Do(func() {
			co.mu.Lock()
			delete(co.watchers[deviceID], w)
			if len(co.watchers[deviceID]) == 0 {
				delete(co.watchers, deviceID)
			}
			co.online[deviceID]--
			wentOffline := co.online[deviceID] <= 0
			if wentOffline {
				delete(co.online, deviceID)
			}
			co.mu.Unlock()
			if wentOffline {
				if cl := co.c.cluster; cl != nil {
					cl.SetPresence(deviceID, false)
				}
				_ = co.c.Store.SetLastSeen(context.Background(), deviceID, store.Now())
				co.c.Bus.Publish(EvDeviceOffline, map[string]string{"id": deviceID})
				co.Trigger()
			}
		})
	}
}

func (co *Coordinator) IsOnline(deviceID string) bool {
	co.mu.RLock()
	defer co.mu.RUnlock()
	if deviceID == co.c.GatewayID() && deviceID != "" {
		return true
	}
	if co.online[deviceID] > 0 {
		return true
	}
	if cl := co.c.cluster; cl != nil {
		return cl.RemoteOnline()[deviceID]
	}
	return false
}

// OnlineCount returns the number of connected native devices.
func (co *Coordinator) OnlineCount() int {
	co.mu.RLock()
	defer co.mu.RUnlock()
	return len(co.online)
}

// SendSignal delivers a sealed signalling message to all sessions of toID, on this
// instance or (in a cluster) the one that holds the target stream.
func (co *Coordinator) SendSignal(from *store.Device, toID string, sealed []byte) bool {
	sig := Signal{FromID: from.ID, FromDisco: from.DiscoKey, Sealed: sealed}
	if co.deliverSignal(toID, sig) {
		return true
	}
	if cl := co.c.cluster; cl != nil && cl.RemoteOnline()[toID] {
		cl.ForwardSignal(toID, sig)
		return true
	}
	return false
}

func (co *Coordinator) deliverSignal(toID string, sig Signal) bool {
	co.mu.RLock()
	defer co.mu.RUnlock()
	delivered := false
	for w := range co.watchers[toID] {
		select {
		case w.Signals <- sig:
			delivered = true
		default:
		}
	}
	return delivered
}

// Notify sends a notice to all sessions of a device (e.g. when disabled), on any instance.
func (co *Coordinator) Notify(deviceID string, n Notice) {
	co.deliverNotice(deviceID, n)
	if cl := co.c.cluster; cl != nil {
		cl.ForwardNotice(deviceID, n)
	}
}

func (co *Coordinator) deliverNotice(deviceID string, n Notice) {
	co.mu.RLock()
	defer co.mu.RUnlock()
	for w := range co.watchers[deviceID] {
		select {
		case w.Notices <- n:
		default:
		}
	}
}

// ---------- helpers on Snapshot ----------

// FQDN returns the DNS name of a device.
func (s *Snapshot) FQDN(d *store.Device) string {
	return d.Name + "." + s.Settings.Network.Domain
}

// OwnerEmail returns the email of the device owner or "".
func (s *Snapshot) OwnerEmail(d *store.Device) string {
	if d.UserID.Valid {
		if u := s.Users[d.UserID.String]; u != nil {
			return u.Email
		}
	}
	return ""
}

// PrimaryRoutesOf returns routes for which device id is the primary router.
func (s *Snapshot) PrimaryRoutesOf(id string) []netip.Prefix {
	var out []netip.Prefix
	for p, dev := range s.PrimaryRoute {
		if dev == id {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// ExitNodes returns active, approved exit nodes.
func (s *Snapshot) ExitNodes() []*store.Device {
	var out []*store.Device
	for id := range s.Active {
		d := s.Devices[id]
		if d.ExitAdvertised && d.ExitApproved {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
