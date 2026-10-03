package core

import (
	"context"
	"sync"
	"time"

	"github.com/anand34577/gorget/internal/store"
)

// wgPresence tracks whether standard WireGuard clients are connected. They have no
// session with the server, so the gateway reports every time it sees traffic from one
// (WireGuard keepalives count) and the client is online while that keeps happening.
type wgPresence struct {
	mu     sync.Mutex
	seen   map[string]time.Time // device ID -> last time traffic or a handshake was observed
	online map[string]bool      // last state announced on the event bus
}

func newWGPresence() *wgPresence {
	return &wgPresence{seen: map[string]time.Time{}, online: map[string]bool{}}
}

// wgWindow is how long a client may be silent before it counts as offline: three keepalive
// intervals, or three minutes (a WireGuard handshake lasts about that long) without keepalives.
func (c *Core) wgWindow() time.Duration {
	if ka := c.Settings().Gateway.PersistentKeepalive; ka > 0 {
		return 3 * time.Duration(ka) * time.Second
	}
	return 3 * time.Minute
}

// NoteWGActivity records that the gateway saw traffic from a standard WireGuard client.
func (c *Core) NoteWGActivity(id string, at time.Time) {
	p := c.wg
	p.mu.Lock()
	if at.After(p.seen[id]) {
		p.seen[id] = at
	}
	p.mu.Unlock()
}

// wgOnline reports whether a standard WireGuard client is connected right now.
func (c *Core) wgOnline(d *store.Device) bool {
	c.wg.mu.Lock()
	at, ok := c.wg.seen[d.ID]
	c.wg.mu.Unlock()
	if !ok && d.LastSeenAt > 0 {
		at = time.Unix(d.LastSeenAt, 0) // before the first sample after a restart
	}
	return !at.IsZero() && time.Since(at) < c.wgWindow()
}

// runWGPresence announces clients that connect or go quiet, so the console and the
// notifications react within seconds instead of waiting for the next poll.
func (c *Core) runWGPresence(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	first := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s := c.Coord.Snapshot()
		if s == nil {
			continue
		}
		for id, d := range s.Devices {
			if d.Kind != store.KindWireGuard {
				continue
			}
			on := s.Active[id] && c.wgOnline(d)
			c.wg.mu.Lock()
			was, known := c.wg.online[id]
			c.wg.online[id] = on
			c.wg.mu.Unlock()
			if first || !known || was == on {
				continue // the first pass only learns the current state
			}
			if on {
				c.Bus.Publish(EvDeviceOnline, map[string]string{"id": id})
			} else {
				_ = c.Store.SetLastSeen(ctx, id, store.Now())
				c.Bus.Publish(EvDeviceOffline, map[string]string{"id": id})
			}
		}
		first = false
	}
}
