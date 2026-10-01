// Package ha lets several gorget-server instances share one PostgreSQL database.
//
// All durable state already lives in the database. This package adds what lives in
// memory elsewhere: which instance holds each device's control stream (presence),
// cross-instance notifications (state changed, peer signals, notices, live events,
// through PostgreSQL LISTEN/NOTIFY), a registry of live instances (each one is also a
// relay), and leader election for singleton jobs.
package ha

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/relay"
	"github.com/anand34577/gorget/internal/store"
)

const (
	channel        = "gorget_ha"
	maxPayload     = 7900 // PostgreSQL limits NOTIFY payloads to 8000 bytes
	beat           = 10 * time.Second
	staleAfter     = 45 * time.Second // presence and instances older than this are dead
	leaderTTL      = 30
	presencePoll   = 5 * time.Second
	changeDebounce = 150 * time.Millisecond
)

// Options configure a Cluster.
type Options struct {
	DSN        string
	Store      *store.Store
	Core       *core.Core
	Log        *slog.Logger
	InstanceID string
	Region     string
	Name       string
	RelayURL   string
	UDPAddr    string
	// PeerListen / PeerAddr: where this instance receives relay packets forwarded by the others
	// (UDP) and the address they use to reach it. MACKey authenticates those packets.
	PeerListen string
	PeerAddr   string
	MACKey     []byte
	// Relay is this instance's relay server (forwarding is off when nil).
	Relay *relay.Server
}

// Cluster implements core.Cluster on PostgreSQL.
type Cluster struct {
	o       Options
	log     *slog.Logger
	started int64

	leader atomic.Bool

	mu        sync.RWMutex
	remote    map[string]bool
	instances []core.ClusterInstance
	local     map[string]bool // devices with a stream here (re-asserted on every heartbeat)

	link        *net.UDPConn
	relayLocal  map[relay.Key]bool
	relayRemote map[relay.Key]string // relay clients on other instances -> instance
	peerAddrs   map[string]netip.AddrPort

	changeMu    sync.Mutex
	changeTimer *time.Timer
}

// SetRelay attaches the relay server whose packets this cluster forwards (call before Run).
func (c *Cluster) SetRelay(r *relay.Server) { c.o.Relay = r }

// New creates the cluster; call Run to start it.
func New(o Options) *Cluster {
	return &Cluster{o: o, log: o.Log, started: store.Now(), remote: map[string]bool{}, local: map[string]bool{},
		relayLocal: map[relay.Key]bool{}, relayRemote: map[relay.Key]string{}, peerAddrs: map[string]netip.AddrPort{}}
}

type message struct {
	T    string     `json:"t"`
	From string     `json:"f"`
	To   string     `json:"to,omitempty"`
	Sig  *signalMsg `json:"s,omitempty"`
	Note *noticeMsg `json:"n,omitempty"`
	Ev   *eventMsg  `json:"e,omitempty"`
	Key  string     `json:"k,omitempty"`
	Up   bool       `json:"u,omitempty"`
}

type signalMsg struct {
	From      string `json:"from"`
	FromDisco string `json:"fd"`
	Sealed    string `json:"sealed"`
}

type noticeMsg struct {
	Kind string `json:"k"`
	Msg  string `json:"m"`
}

type eventMsg struct {
	Type string          `json:"type"`
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ---------- core.Cluster ----------

func (c *Cluster) InstanceID() string { return c.o.InstanceID }
func (c *Cluster) IsLeader() bool     { return c.leader.Load() }

func (c *Cluster) RemoteOnline() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remote // replaced wholesale on refresh, never mutated in place
}

func (c *Cluster) Instances() []core.ClusterInstance {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.instances
}

// Changed tells the other instances to rebuild (coalesced).
func (c *Cluster) Changed() {
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	if c.changeTimer != nil {
		return
	}
	c.changeTimer = time.AfterFunc(changeDebounce, func() {
		c.changeMu.Lock()
		c.changeTimer = nil
		c.changeMu.Unlock()
		c.notify(message{T: "changed"})
	})
}

func (c *Cluster) PublishEvent(ev core.Event) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return
	}
	c.notify(message{T: "event", Ev: &eventMsg{Type: ev.Type, Time: ev.Time, Data: data}})
}

func (c *Cluster) SetPresence(deviceID string, online bool) {
	c.mu.Lock()
	if online {
		c.local[deviceID] = true
	} else {
		delete(c.local, deviceID)
	}
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		if online {
			err = c.o.Store.SetPresence(ctx, deviceID, c.o.InstanceID)
		} else {
			err = c.o.Store.ClearPresence(ctx, deviceID, c.o.InstanceID)
		}
		if err != nil {
			c.log.Warn("presence update failed", "device", deviceID, "err", err)
		}
		c.Changed()
	}()
}

func (c *Cluster) ForwardSignal(toID string, s core.Signal) {
	c.notify(message{T: "signal", To: toID, Sig: &signalMsg{From: s.FromID, FromDisco: s.FromDisco, Sealed: base64.StdEncoding.EncodeToString(s.Sealed)}})
}

func (c *Cluster) ForwardNotice(deviceID string, n core.Notice) {
	c.notify(message{T: "notice", To: deviceID, Note: &noticeMsg{Kind: n.Kind, Msg: n.Message}})
}

// ---------- transport ----------

func (c *Cluster) notify(m message) {
	m.From = c.o.InstanceID
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	if len(b) > maxPayload {
		c.log.Warn("cluster message too large, dropped", "type", m.T, "bytes", len(b))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := c.o.Store.DB().ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, string(b)); err != nil {
			c.log.Debug("cluster notify failed", "err", err)
		}
	}()
}

// Run registers the instance, listens for the others and keeps heartbeats until ctx ends.
func (c *Cluster) Run(ctx context.Context) {
	if c.o.Relay != nil && c.o.PeerListen != "" {
		if err := c.startRelayLink(ctx); err != nil {
			c.log.Error("relay link unavailable: clients on different instances cannot relay to each other", "err", err)
		}
	}
	c.heartbeat(ctx)
	go c.listenLoop(ctx)
	t := time.NewTicker(beat)
	p := time.NewTicker(presencePoll)
	defer t.Stop()
	defer p.Stop()
	for {
		select {
		case <-ctx.Done():
			// Leave cleanly so the others stop using our relay at once.
			dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.o.Store.Unlock(dctx, "leader", c.o.InstanceID)
			_ = c.o.Store.DeleteInstance(dctx, c.o.InstanceID)
			cancel()
			c.notify(message{T: "changed"})
			return
		case <-t.C:
			c.heartbeat(ctx)
		case <-p.C:
			c.refresh(ctx)
		}
	}
}

func (c *Cluster) heartbeat(ctx context.Context) {
	st := c.o.Store
	now := store.Now()
	err := st.UpsertInstance(ctx, &store.Instance{ID: c.o.InstanceID, Region: c.o.Region, Name: c.o.Name, RelayURL: c.o.RelayURL, UDPAddr: c.o.UDPAddr, PeerAddr: c.o.PeerAddr, StartedAt: c.started, LastSeen: now})
	if err != nil {
		c.log.Warn("cluster heartbeat failed", "err", err)
		return
	}
	// Re-assert presence for streams we hold (also repairs rows purged during a database outage).
	c.mu.RLock()
	local := make([]string, 0, len(c.local))
	for id := range c.local {
		local = append(local, id)
	}
	c.mu.RUnlock()
	if len(local) > 0 {
		if err := st.TouchPresence(ctx, c.o.InstanceID); err != nil {
			c.log.Warn("presence heartbeat failed", "err", err)
		}
	}
	c.mu.RLock()
	hasRelay := len(c.relayLocal) > 0
	c.mu.RUnlock()
	if hasRelay {
		_ = st.TouchRelayPresence(ctx, c.o.InstanceID)
	}
	_ = st.PurgeStale(ctx, now-int64(staleAfter.Seconds()))
	won, err := st.TryLock(ctx, "leader", c.o.InstanceID, leaderTTL)
	if err == nil {
		if won != c.leader.Load() {
			c.log.Info("cluster leadership changed", "leader", won)
		}
		c.leader.Store(won)
	}
	c.refresh(ctx)
}

// refresh reloads presence of other instances and the instance list.
func (c *Cluster) refresh(ctx context.Context) {
	since := store.Now() - int64(staleAfter.Seconds())
	pres, err := c.o.Store.ListPresence(ctx, since)
	if err == nil {
		remote := make(map[string]bool, len(pres))
		for dev, inst := range pres {
			if inst != c.o.InstanceID {
				remote[dev] = true
			}
		}
		c.mu.Lock()
		c.remote = remote
		c.mu.Unlock()
	}
	list, err := c.o.Store.ListInstances(ctx, since)
	if err == nil {
		c.refreshRelay(ctx, list)
		out := make([]core.ClusterInstance, 0, len(list))
		for _, in := range list {
			out = append(out, core.ClusterInstance{ID: in.ID, Region: in.Region, Name: in.Name, RelayURL: in.RelayURL, UDPAddr: in.UDPAddr, Self: in.ID == c.o.InstanceID})
		}
		c.mu.Lock()
		changed := len(out) != len(c.instances)
		for i := range out {
			if !changed && i < len(c.instances) && out[i] != c.instances[i] {
				changed = true
			}
		}
		c.instances = out
		c.mu.Unlock()
		if changed {
			c.o.Core.Coord.Trigger() // the network map lists relays
		}
	}
}

// listenLoop holds a dedicated connection that LISTENs for the other instances.
func (c *Cluster) listenLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := c.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		c.log.Warn("cluster listener lost, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
		// Anything could have changed while we were deaf.
		c.refresh(ctx)
		c.o.Core.RemoteChanged(ctx)
	}
}

func (c *Cluster) listen(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, c.o.DSN)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var m message
		if json.Unmarshal([]byte(n.Payload), &m) != nil || m.From == c.o.InstanceID {
			continue
		}
		c.handle(ctx, m)
	}
}

func (c *Cluster) handle(ctx context.Context, m message) {
	switch m.T {
	case "changed":
		c.refresh(ctx)
		c.o.Core.RemoteChanged(ctx)
	case "rp":
		c.applyRelayPresence(m.From, m.Key, m.Up)
	case "signal":
		if m.Sig == nil {
			return
		}
		sealed, err := base64.StdEncoding.DecodeString(m.Sig.Sealed)
		if err != nil {
			return
		}
		c.o.Core.RemoteSignal(m.To, core.Signal{FromID: m.Sig.From, FromDisco: m.Sig.FromDisco, Sealed: sealed})
	case "notice":
		if m.Note != nil {
			c.o.Core.RemoteNotice(m.To, core.Notice{Kind: m.Note.Kind, Message: m.Note.Msg})
		}
	case "event":
		if m.Ev != nil {
			var data any
			if len(m.Ev.Data) > 0 {
				_ = json.Unmarshal(m.Ev.Data, &data)
			}
			c.o.Core.RemoteEvent(core.Event{Type: m.Ev.Type, Time: m.Ev.Time, Data: data})
		}
	}
}
