package store

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
)

// ---------- short-lived shared state (replaces in-memory maps so any instance can answer) ----------

// PutPending stores a short-lived value (login challenges, OIDC and passkey ceremonies).
func (s *Store) PutPending(ctx context.Context, key, value string, ttlSeconds int64) error {
	_, err := s.exec(ctx, s.db, `INSERT INTO pending_state (key, value, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`, key, value, Now()+ttlSeconds)
	return err
}

// TakePending returns and removes a value exactly once; expired or missing values give ok=false.
func (s *Store) TakePending(ctx context.Context, key string) (string, bool, error) {
	var v struct {
		Value     string `db:"value"`
		ExpiresAt int64  `db:"expires_at"`
	}
	// DELETE ... RETURNING is atomic on both SQLite (3.35+) and PostgreSQL: exactly one caller wins.
	err := s.get(ctx, s.db, &v, `DELETE FROM pending_state WHERE key = ? RETURNING value, expires_at`, key)
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v.Value, v.ExpiresAt >= Now(), nil
}

// ---------- locks and leadership ----------

// TryLock takes the named lock for ttlSeconds, or renews it when owner already holds it.
func (s *Store) TryLock(ctx context.Context, name, owner string, ttlSeconds int64) (bool, error) {
	now := Now()
	res, err := s.exec(ctx, s.db, `INSERT INTO locks (name, owner, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET owner = excluded.owner, expires_at = excluded.expires_at
		WHERE locks.owner = excluded.owner OR locks.expires_at < ?`, name, owner, now+ttlSeconds, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Unlock releases a lock held by owner.
func (s *Store) Unlock(ctx context.Context, name, owner string) error {
	_, err := s.exec(ctx, s.db, `DELETE FROM locks WHERE name = ? AND owner = ?`, name, owner)
	return err
}

// ---------- presence and instances ----------

// Instance is a running server process sharing the database.
type Instance struct {
	ID        string `db:"id" json:"id"`
	Region    string `db:"region" json:"region"`
	Name      string `db:"name" json:"name"`
	RelayURL  string `db:"relay_url" json:"relay_url"`
	UDPAddr   string `db:"udp_addr" json:"udp_addr"`
	PeerAddr  string `db:"peer_addr" json:"peer_addr"` // where other instances send forwarded relay packets
	StartedAt int64  `db:"started_at" json:"started_at"`
	LastSeen  int64  `db:"last_seen" json:"last_seen"`
}

func (s *Store) UpsertInstance(ctx context.Context, i *Instance) error {
	_, err := s.exec(ctx, s.db, `INSERT INTO instances (id, region, name, relay_url, udp_addr, peer_addr, started_at, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET region = excluded.region, name = excluded.name, relay_url = excluded.relay_url,
		udp_addr = excluded.udp_addr, peer_addr = excluded.peer_addr, last_seen = excluded.last_seen`, i.ID, i.Region, i.Name, i.RelayURL, i.UDPAddr, i.PeerAddr, i.StartedAt, i.LastSeen)
	return err
}

// ListInstances returns instances seen since the given time.
func (s *Store) ListInstances(ctx context.Context, since int64) ([]Instance, error) {
	var out []Instance
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM instances WHERE last_seen >= ? ORDER BY region, id`, since)
}

func (s *Store) DeleteInstance(ctx context.Context, id string) error {
	if _, err := s.exec(ctx, s.db, `DELETE FROM presence WHERE instance_id = ?`, id); err != nil {
		return err
	}
	if _, err := s.exec(ctx, s.db, `DELETE FROM relay_presence WHERE instance_id = ?`, id); err != nil {
		return err
	}
	_, err := s.exec(ctx, s.db, `DELETE FROM instances WHERE id = ?`, id)
	return err
}

func (s *Store) SetPresence(ctx context.Context, deviceID, instanceID string) error {
	_, err := s.exec(ctx, s.db, `INSERT INTO presence (device_id, instance_id, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (device_id) DO UPDATE SET instance_id = excluded.instance_id, updated_at = excluded.updated_at`, deviceID, instanceID, Now())
	return err
}

func (s *Store) ClearPresence(ctx context.Context, deviceID, instanceID string) error {
	_, err := s.exec(ctx, s.db, `DELETE FROM presence WHERE device_id = ? AND instance_id = ?`, deviceID, instanceID)
	return err
}

// TouchPresence refreshes every presence row owned by an instance (its heartbeat).
func (s *Store) TouchPresence(ctx context.Context, instanceID string) error {
	_, err := s.exec(ctx, s.db, `UPDATE presence SET updated_at = ? WHERE instance_id = ?`, Now(), instanceID)
	return err
}

// ListPresence returns device id -> instance id for rows refreshed since the given time.
func (s *Store) ListPresence(ctx context.Context, since int64) (map[string]string, error) {
	var rows []struct {
		DeviceID   string `db:"device_id"`
		InstanceID string `db:"instance_id"`
	}
	if err := s.sel(ctx, s.db, &rows, `SELECT device_id, instance_id FROM presence WHERE updated_at >= ?`, since); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.DeviceID] = r.InstanceID
	}
	return out, nil
}

// PurgeStale removes presence rows and instances nobody refreshed since the given time.
func (s *Store) PurgeStale(ctx context.Context, before int64) error {
	if _, err := s.exec(ctx, s.db, `DELETE FROM presence WHERE updated_at < ?`, before); err != nil {
		return err
	}
	if _, err := s.exec(ctx, s.db, `DELETE FROM relay_presence WHERE updated_at < ?`, before); err != nil {
		return err
	}
	if _, err := s.exec(ctx, s.db, `DELETE FROM instances WHERE last_seen < ?`, before); err != nil {
		return err
	}
	_, err := s.exec(ctx, s.db, `DELETE FROM locks WHERE expires_at < ?`, Now()-3600)
	return err
}

// ---------- key/value storage (shared TLS certificates) ----------

// KV is a stored blob.
type KV struct {
	Key      string
	Value    []byte
	Modified int64
}

func (s *Store) KVPut(ctx context.Context, key string, value []byte) error {
	_, err := s.exec(ctx, s.db, `INSERT INTO kv_storage (key, value, modified) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, modified = excluded.modified`,
		key, base64.StdEncoding.EncodeToString(value), Now())
	return err
}

func (s *Store) KVGet(ctx context.Context, key string) (*KV, error) {
	var row struct {
		Value    string `db:"value"`
		Modified int64  `db:"modified"`
	}
	if err := s.get(ctx, s.db, &row, `SELECT value, modified FROM kv_storage WHERE key = ?`, key); err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.DecodeString(row.Value)
	if err != nil {
		return nil, err
	}
	return &KV{Key: key, Value: b, Modified: row.Modified}, nil
}

func (s *Store) KVDelete(ctx context.Context, key string) error {
	_, err := s.exec(ctx, s.db, `DELETE FROM kv_storage WHERE key = ?`, key)
	return err
}

// KVList returns keys with the given prefix.
func (s *Store) KVList(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	const esc = "!"
	like := strings.NewReplacer(esc, esc+esc, "%", esc+"%", "_", esc+"_").Replace(prefix) + "%"
	return keys, s.sel(ctx, s.db, &keys, `SELECT key FROM kv_storage WHERE key LIKE ? ESCAPE '!' ORDER BY key`, like)
}

// ---------- relay presence: which instance holds a device's relay connection ----------

func (s *Store) SetRelayPresence(ctx context.Context, key, instanceID string) error {
	_, err := s.exec(ctx, s.db, `INSERT INTO relay_presence (key, instance_id, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET instance_id = excluded.instance_id, updated_at = excluded.updated_at`, key, instanceID, Now())
	return err
}

func (s *Store) ClearRelayPresence(ctx context.Context, key, instanceID string) error {
	_, err := s.exec(ctx, s.db, `DELETE FROM relay_presence WHERE key = ? AND instance_id = ?`, key, instanceID)
	return err
}

func (s *Store) TouchRelayPresence(ctx context.Context, instanceID string) error {
	_, err := s.exec(ctx, s.db, `UPDATE relay_presence SET updated_at = ? WHERE instance_id = ?`, Now(), instanceID)
	return err
}

// ListRelayPresence returns key -> instance id for rows refreshed since the given time.
func (s *Store) ListRelayPresence(ctx context.Context, since int64) (map[string]string, error) {
	var rows []struct {
		Key        string `db:"key"`
		InstanceID string `db:"instance_id"`
	}
	if err := s.sel(ctx, s.db, &rows, `SELECT key, instance_id FROM relay_presence WHERE updated_at >= ?`, since); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.InstanceID
	}
	return out, nil
}
