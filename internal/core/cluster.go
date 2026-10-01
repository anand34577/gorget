package core

import (
	"context"
	"time"
)

// ClusterInstance is one live server instance (relay location).
type ClusterInstance struct {
	ID       string `json:"id"`
	Region   string `json:"region"`
	Name     string `json:"name"`
	RelayURL string `json:"relay_url"`
	UDPAddr  string `json:"udp_addr"`
	Self     bool   `json:"self"`
}

// Cluster connects this instance to the others that share its database. It is nil
// when the server runs alone. Implemented by package ha.
type Cluster interface {
	// InstanceID identifies this process.
	InstanceID() string
	// Changed tells the other instances that shared state changed (rebuild your network view).
	Changed()
	// PublishEvent forwards a console/webhook-less event so every console sees it live.
	PublishEvent(ev Event)
	// SetPresence records that a device's control stream is (no longer) connected here.
	SetPresence(deviceID string, online bool)
	// RemoteOnline is the set of devices connected to other instances.
	RemoteOnline() map[string]bool
	// ForwardSignal hands a peer signal to the instance that holds the target's stream.
	ForwardSignal(toID string, s Signal)
	// ForwardNotice delivers a notice to a device's stream on any instance.
	ForwardNotice(deviceID string, n Notice)
	// Instances lists live instances, including this one.
	Instances() []ClusterInstance
	// IsLeader is true on the one instance that runs singleton jobs (cleanup, expiry).
	IsLeader() bool
}

// SetCluster attaches the cluster before Run.
func (c *Core) SetCluster(cl Cluster) {
	c.cluster = cl
	if cl != nil {
		c.Bus.onPublish = cl.PublishEvent
	}
}

// ClusterInstances returns live instances (just this one when not clustered).
func (c *Core) ClusterInstances() []ClusterInstance {
	if c.cluster == nil {
		return nil
	}
	return c.cluster.Instances()
}

// IsLeader reports whether this instance should run singleton background jobs.
func (c *Core) IsLeader() bool { return c.cluster == nil || c.cluster.IsLeader() }

// ---------- calls from the cluster layer (state changed elsewhere) ----------

// RemoteChanged reloads shared state after another instance changed it.
func (c *Core) RemoteChanged(ctx context.Context) {
	if err := c.loadSettings(ctx); err != nil {
		c.Log.Warn("reload settings after a change on another instance", "err", err)
	}
	var s SetupState
	if err := c.Store.GetSetting(ctx, keySetup, &s); err == nil {
		c.mu.Lock()
		c.setup = s
		c.mu.Unlock()
	}
	c.Coord.triggerLocal()
}

// RemoteSignal delivers a signal from a peer connected to another instance.
func (c *Core) RemoteSignal(toID string, s Signal) { c.Coord.deliverSignal(toID, s) }

// RemoteNotice delivers a notice for a device connected here.
func (c *Core) RemoteNotice(deviceID string, n Notice) { c.Coord.deliverNotice(deviceID, n) }

// RemoteEvent republishes an event from another instance to local subscribers (console live
// updates). Webhooks ignore it: the originating instance already delivered them.
func (c *Core) RemoteEvent(ev Event) {
	ev.Remote = true
	c.Bus.deliver(ev)
}

// DefaultRelayTimeout is how long an instance may stay silent before clients stop using its relay.
const DefaultRelayTimeout = 45 * time.Second
