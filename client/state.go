package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Prefs are the user's connection preferences.
type Prefs struct {
	// ExitNodeID routes all internet traffic through that peer ("" = none).
	ExitNodeID string `json:"exit_node_id"`
	// AllowLAN keeps the local network reachable while using an exit node.
	AllowLAN bool `json:"allow_lan"`
	// AcceptRoutes installs subnet routes shared by other devices.
	AcceptRoutes bool `json:"accept_routes"`
	// UseDNS uses Gorget DNS (device names, custom records, configured resolvers).
	UseDNS bool `json:"use_dns"`
	// AdvertiseRoutes / AdvertiseExitNode share this device's networks (desktop/servers).
	AdvertiseRoutes   []string `json:"advertise_routes,omitempty"`
	AdvertiseExitNode bool     `json:"advertise_exit_node,omitempty"`
	// KillSwitch blocks traffic outside the tunnel while using an exit node (desktop).
	KillSwitch bool `json:"kill_switch"`
	// WantRunning reconnects automatically on start (always-on).
	WantRunning bool `json:"want_running"`
	// NoPostQuantum turns off the post-quantum pre-shared keys between Gorget devices.
	NoPostQuantum bool `json:"no_post_quantum,omitempty"`
	// NoPortMapping stops Gorget from asking the router to forward its UDP port (UPnP, NAT-PMP, PCP).
	// Takes effect the next time the connection starts.
	NoPortMapping bool `json:"no_port_mapping,omitempty"`
}

func DefaultPrefs() Prefs {
	return Prefs{AllowLAN: true, AcceptRoutes: true, UseDNS: true}
}

// persisted is the on-disk state (0600, in the app's private directory).
type persisted struct {
	ServerURL  string `json:"server_url"`
	MachineKey []byte `json:"machine_key"` // Ed25519 seed
	WGKey      string `json:"wg_key"`
	DeviceID   string `json:"device_id"`
	Registered bool   `json:"registered"`
	Prefs      Prefs  `json:"prefs"`
	ListenPort uint16 `json:"listen_port"`
	// PQKeys holds post-quantum pre-shared keys by peer WireGuard public key (base64).
	PQKeys map[string]pqEntry `json:"pq_keys,omitempty"`
}

// pqEntry is a stored pre-shared key (base64) and the generation of the exchange that made it.
type pqEntry struct {
	Key string `json:"key"`
	Gen uint64 `json:"gen"`
}

type state struct {
	mu   sync.Mutex
	path string
	p    persisted
}

func loadState(dir string) (*state, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &state{path: filepath.Join(dir, "gorget-state.json"), p: persisted{Prefs: DefaultPrefs()}}
	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.p); err != nil {
			return nil, err
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	changed := false
	if len(s.p.MachineKey) != ed25519.SeedSize {
		s.p.MachineKey = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(s.p.MachineKey); err != nil {
			return nil, err
		}
		changed = true
	}
	if _, err := wgtypes.ParseKey(s.p.WGKey); err != nil {
		k, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return nil, err
		}
		s.p.WGKey = k.String()
		changed = true
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *state) saveLocked() error {
	b, err := json.MarshalIndent(s.p, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *state) update(f func(p *persisted)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.p)
	return s.saveLocked()
}

func (s *state) get() persisted {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.p
}

func (s *state) machineKey() ed25519.PrivateKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ed25519.NewKeyFromSeed(s.p.MachineKey)
}

func (s *state) wgKey() wgtypes.Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, _ := wgtypes.ParseKey(s.p.WGKey)
	return k
}

// pqKeys returns a copy of the stored post-quantum keys.
func (s *state) pqKeys() map[string]pqEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]pqEntry, len(s.p.PQKeys))
	for k, v := range s.p.PQKeys {
		out[k] = v
	}
	return out
}

// rotateWGKey replaces the WireGuard key (e.g. after logout).
func (s *state) rotateWGKey() error {
	k, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return err
	}
	// Pre-shared keys are bound to our old key and no longer match anything.
	return s.update(func(p *persisted) { p.WGKey, p.PQKeys = k.String(), nil })
}
