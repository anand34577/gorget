package store

import "context"

// DeviceSession is one period during which a device was connected, with the public
// address it connected from. Sessions with EndedAt 0 are still open.
type DeviceSession struct {
	ID            string `db:"id" json:"id"`
	DeviceID      string `db:"device_id" json:"device_id"`
	StartedAt     int64  `db:"started_at" json:"started_at"`
	EndedAt       int64  `db:"ended_at" json:"ended_at"`
	PublicIP      string `db:"public_ip" json:"public_ip"`
	Country       string `db:"country" json:"country"`
	ClientVersion string `db:"client_version" json:"client_version"`
}

// StatsSample is a network-wide snapshot taken every few minutes. Rx and Tx are the
// bytes moved since the previous sample.
type StatsSample struct {
	TS       int64 `db:"ts" json:"ts"`
	Total    int   `db:"total" json:"total"`
	Online   int   `db:"online" json:"online"`
	Pending  int   `db:"pending" json:"pending"`
	Disabled int   `db:"disabled" json:"disabled"`
	Rx       int64 `db:"rx" json:"rx"`
	Tx       int64 `db:"tx" json:"tx"`
}

func (s *Store) OpenSession(ctx context.Context, d *DeviceSession) error {
	return s.insert(ctx, s.db, "device_sessions", d)
}

func (s *Store) CloseSession(ctx context.Context, id string, at int64) error {
	_, err := s.exec(ctx, s.db, `UPDATE device_sessions SET ended_at = ? WHERE id = ? AND ended_at = 0`, at, id)
	return err
}

// CloseOpenSessions ends every open session (used at start-up: the previous
// process can't have known when its devices went away).
func (s *Store) CloseOpenSessions(ctx context.Context, at int64) error {
	_, err := s.exec(ctx, s.db, `UPDATE device_sessions SET ended_at = ? WHERE ended_at = 0`, at)
	return err
}

// DeviceSessions returns a device's sessions, newest first.
func (s *Store) DeviceSessions(ctx context.Context, deviceID string, limit int) ([]DeviceSession, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out := []DeviceSession{}
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM device_sessions WHERE device_id = ? ORDER BY started_at DESC LIMIT ?`, deviceID, limit)
}

// RecentSessions returns sessions started after since, newest first.
func (s *Store) RecentSessions(ctx context.Context, since int64, limit int) ([]DeviceSession, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	out := []DeviceSession{}
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM device_sessions WHERE started_at >= ? ORDER BY started_at DESC LIMIT ?`, since, limit)
}

// KnownCountries returns the countries a device connected from after since.
func (s *Store) KnownCountries(ctx context.Context, deviceID string, since int64) ([]string, error) {
	var out []string
	return out, s.sel(ctx, s.db, &out, `SELECT DISTINCT country FROM device_sessions WHERE device_id = ? AND started_at >= ? AND country <> ''`, deviceID, since)
}

func (s *Store) InsertSample(ctx context.Context, x *StatsSample) error {
	return s.insert(ctx, s.db, "stats_samples", x)
}

// Samples returns samples taken at or after since, oldest first.
func (s *Store) Samples(ctx context.Context, since int64) ([]StatsSample, error) {
	out := []StatsSample{}
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM stats_samples WHERE ts >= ? ORDER BY ts`, since)
}

// PruneHistory removes samples and finished sessions older than the given times.
func (s *Store) PruneHistory(ctx context.Context, samplesBefore, sessionsBefore int64) error {
	if _, err := s.exec(ctx, s.db, `DELETE FROM stats_samples WHERE ts < ?`, samplesBefore); err != nil {
		return err
	}
	_, err := s.exec(ctx, s.db, `DELETE FROM device_sessions WHERE ended_at > 0 AND ended_at < ?`, sessionsBefore)
	return err
}
