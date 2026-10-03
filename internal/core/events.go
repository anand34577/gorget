package core

import (
	"sync"
	"time"
)

// Event types published on the bus. Webhooks subscribe to the same names.
const (
	EvDeviceCreated   = "device.created"
	EvDeviceUpdated   = "device.updated"
	EvDeviceDeleted   = "device.deleted"
	EvDeviceOnline    = "device.online"
	EvDeviceOffline   = "device.offline"
	EvDevicePending   = "device.approval_required"
	EvDeviceKeyExpiry = "device.key_expiring"
	EvRouteAdvertised = "route.advertised"
	EvRouteUpdated    = "route.updated"
	EvPolicyUpdated   = "policy.updated"
	EvUserCreated     = "user.created"
	EvUserUpdated     = "user.updated"
	EvUserDeleted     = "user.deleted"
	EvSettingsUpdated = "settings.updated"
	EvLoginFailed     = "auth.login_failed"
	EvLogin           = "auth.login"
	EvAccessRequested = "access.requested"
	EvAccessApproved  = "access.approved"
	EvAccessDenied    = "access.denied"
	// EvDeviceNewCountry: a device connected from a country it hasn't used recently.
	EvDeviceNewCountry = "device.new_country"
	// EvDeviceBlocked: a device newly fails enforced security rules.
	EvDeviceBlocked = "device.blocked"
)

var WebhookEvents = []string{
	EvDeviceCreated, EvDeviceUpdated, EvDeviceDeleted, EvDeviceOnline, EvDeviceOffline,
	EvDevicePending, EvDeviceKeyExpiry, EvRouteAdvertised, EvRouteUpdated, EvPolicyUpdated,
	EvUserCreated, EvUserUpdated, EvUserDeleted, EvSettingsUpdated, EvLoginFailed, EvLogin,
	EvAccessRequested, EvAccessApproved, EvAccessDenied, EvDeviceNewCountry, EvDeviceBlocked,
}

type Event struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
	// Remote marks an event that happened on another instance (not delivered to webhooks again).
	Remote bool `json:"-"`
}

// Bus is a simple non-blocking fan-out pub/sub. Slow subscribers drop events.
type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
	// onPublish forwards locally generated events to the other cluster instances.
	onPublish func(Event)
}

func NewBus() *Bus { return &Bus{subs: map[chan Event]struct{}{}} }

func (b *Bus) Publish(typ string, data any) {
	ev := Event{Type: typ, Time: time.Now().UTC(), Data: data}
	b.deliver(ev)
	if b.onPublish != nil {
		b.onPublish(ev)
	}
}

func (b *Bus) deliver(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe returns a channel of events and a cancel function.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
}
