package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ---------- settings ----------

// GetSetting decodes the JSON value stored under key into dest. Returns ErrNotFound if unset.
func (s *Store) GetSetting(ctx context.Context, key string, dest any) error {
	var raw string
	if err := s.get(ctx, s.db, &raw, `SELECT value FROM settings WHERE key = ?`, key); err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), dest)
}

func (s *Store) PutSetting(ctx context.Context, key string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, s.db, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, string(b), Now())
	return err
}

// ---------- users ----------

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	return s.insert(ctx, s.db, "users", u)
}

func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	return s.update(ctx, s.db, "users", "id", u)
}

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	var u User
	return &u, s.get(ctx, s.db, &u, `SELECT * FROM users WHERE id = ?`, id)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	return &u, s.get(ctx, s.db, &u, `SELECT * FROM users WHERE email = ?`, strings.ToLower(strings.TrimSpace(email)))
}

func (s *Store) GetUserBySubject(ctx context.Context, provider, subject string) (*User, error) {
	var u User
	return &u, s.get(ctx, s.db, &u, `SELECT * FROM users WHERE provider = ? AND provider_subject = ?`, provider, subject)
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	var out []User
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM users ORDER BY email`)
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	return n, s.get(ctx, s.db, &n, `SELECT COUNT(*) FROM users`)
}

func (s *Store) CountOwners(ctx context.Context) (int, error) {
	var n int
	return n, s.get(ctx, s.db, &n, `SELECT COUNT(*) FROM users WHERE role = ? AND disabled = ?`, RoleOwner, false)
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM users WHERE id = ?`, id)
}

// ---------- webauthn ----------

func (s *Store) AddWebAuthnCredential(ctx context.Context, c *WebAuthnCredential) error {
	return s.insert(ctx, s.db, "webauthn_credentials", c)
}

func (s *Store) ListWebAuthnCredentials(ctx context.Context, userID string) ([]WebAuthnCredential, error) {
	var out []WebAuthnCredential
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at`, userID)
}

func (s *Store) UpdateWebAuthnCredential(ctx context.Context, c *WebAuthnCredential) error {
	return s.update(ctx, s.db, "webauthn_credentials", "id", c)
}

func (s *Store) DeleteWebAuthnCredential(ctx context.Context, userID, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, userID)
}

// ---------- sessions ----------

func (s *Store) CreateSession(ctx context.Context, se *Session) error {
	return s.insert(ctx, s.db, "sessions", se)
}

func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	var se Session
	return &se, s.get(ctx, s.db, &se, `SELECT * FROM sessions WHERE id = ?`, id)
}

func (s *Store) UpdateSession(ctx context.Context, se *Session) error {
	return s.update(ctx, s.db, "sessions", "id", se)
}

func (s *Store) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	var out []Session
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC`, userID, Now())
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.exec(ctx, s.db, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.exec(ctx, s.db, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) DeleteExpired(ctx context.Context) error {
	now := Now()
	if _, err := s.exec(ctx, s.db, `DELETE FROM sessions WHERE expires_at < ?`, now); err != nil {
		return err
	}
	if _, err := s.exec(ctx, s.db, `DELETE FROM device_logins WHERE expires_at < ?`, now-3600); err != nil {
		return err
	}
	_, err := s.exec(ctx, s.db, `DELETE FROM pending_state WHERE expires_at < ?`, now)
	return err
}

// ---------- groups ----------

func (s *Store) CreateGroup(ctx context.Context, g *Group) error {
	return s.Tx(ctx, func(tx *sqlx.Tx) error {
		if err := s.insert(ctx, tx, "user_groups", g); err != nil {
			return err
		}
		return s.setMembers(ctx, tx, g.ID, g.Members)
	})
}

func (s *Store) UpdateGroup(ctx context.Context, g *Group) error {
	return s.Tx(ctx, func(tx *sqlx.Tx) error {
		if err := s.update(ctx, tx, "user_groups", "id", g); err != nil {
			return err
		}
		return s.setMembers(ctx, tx, g.ID, g.Members)
	})
}

func (s *Store) setMembers(ctx context.Context, x q, groupID string, members []string) error {
	if _, err := s.exec(ctx, x, `DELETE FROM group_members WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, uid := range members {
		if seen[uid] {
			continue
		}
		seen[uid] = true
		if _, err := s.exec(ctx, x, `INSERT INTO group_members (group_id, user_id) VALUES (?, ?)`, groupID, uid); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	var groups []Group
	if err := s.sel(ctx, s.db, &groups, `SELECT * FROM user_groups ORDER BY name`); err != nil {
		return nil, err
	}
	var rows []struct {
		GroupID string `db:"group_id"`
		UserID  string `db:"user_id"`
	}
	if err := s.sel(ctx, s.db, &rows, `SELECT group_id, user_id FROM group_members`); err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i := range groups {
		idx[groups[i].ID] = i
		groups[i].Members = []string{}
	}
	for _, r := range rows {
		if i, ok := idx[r.GroupID]; ok {
			groups[i].Members = append(groups[i].Members, r.UserID)
		}
	}
	return groups, nil
}

func (s *Store) GetGroup(ctx context.Context, id string) (*Group, error) {
	var g Group
	if err := s.get(ctx, s.db, &g, `SELECT * FROM user_groups WHERE id = ?`, id); err != nil {
		return nil, err
	}
	g.Members = []string{}
	return &g, s.sel(ctx, s.db, &g.Members, `SELECT user_id FROM group_members WHERE group_id = ?`, id)
}

func (s *Store) GetGroupByName(ctx context.Context, name string) (*Group, error) {
	var g Group
	if err := s.get(ctx, s.db, &g, `SELECT * FROM user_groups WHERE name = ?`, name); err != nil {
		return nil, err
	}
	return s.GetGroup(ctx, g.ID)
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM user_groups WHERE id = ?`, id)
}

// SetUserGroups replaces the memberships of userID among groups with the given source
// (used for IdP group sync).
func (s *Store) AddGroupMember(ctx context.Context, groupID, userID string) error {
	_, err := s.exec(ctx, s.db, `INSERT INTO group_members (group_id, user_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, groupID, userID)
	return err
}

func (s *Store) RemoveUserFromSourceGroups(ctx context.Context, userID, source string, keep []string) error {
	var ids []string
	if err := s.sel(ctx, s.db, &ids, `SELECT g.id FROM user_groups g JOIN group_members m ON m.group_id = g.id WHERE m.user_id = ? AND g.source = ?`, userID, source); err != nil {
		return err
	}
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[k] = true
	}
	for _, id := range ids {
		if !keepSet[id] {
			if _, err := s.exec(ctx, s.db, `DELETE FROM group_members WHERE group_id = ? AND user_id = ?`, id, userID); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------- devices ----------

func (s *Store) CreateDevice(ctx context.Context, d *Device) error {
	return s.insert(ctx, s.db, "devices", d)
}

func (s *Store) UpdateDevice(ctx context.Context, d *Device) error {
	d.UpdatedAt = Now()
	return s.update(ctx, s.db, "devices", "id", d)
}

func (s *Store) GetDevice(ctx context.Context, id string) (*Device, error) {
	var d Device
	return &d, s.get(ctx, s.db, &d, `SELECT * FROM devices WHERE id = ?`, id)
}

func (s *Store) GetDeviceByMachineKey(ctx context.Context, mk string) (*Device, error) {
	var d Device
	return &d, s.get(ctx, s.db, &d, `SELECT * FROM devices WHERE machine_key = ?`, mk)
}

func (s *Store) GetDeviceByName(ctx context.Context, name string) (*Device, error) {
	var d Device
	return &d, s.get(ctx, s.db, &d, `SELECT * FROM devices WHERE name = ?`, name)
}

func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	var out []Device
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM devices ORDER BY name`)
}

func (s *Store) ListDeviceNames(ctx context.Context) (map[string]bool, error) {
	var names []string
	if err := s.sel(ctx, s.db, &names, `SELECT name FROM devices`); err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m, nil
}

func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM devices WHERE id = ?`, id)
}

// TouchDevice records liveness information without rewriting the whole row.
func (s *Store) TouchDevice(ctx context.Context, id string, lastSeen int64, endpoint string, rx, tx int64) error {
	_, err := s.exec(ctx, s.db, `UPDATE devices SET last_seen_at = ?, last_endpoint = ?, rx_bytes = ?, tx_bytes = ? WHERE id = ?`,
		lastSeen, endpoint, rx, tx, id)
	return err
}

// ---------- routes ----------

func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	var out []Route
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM routes ORDER BY cidr, priority`)
}

func (s *Store) GetRoute(ctx context.Context, id string) (*Route, error) {
	var r Route
	return &r, s.get(ctx, s.db, &r, `SELECT * FROM routes WHERE id = ?`, id)
}

func (s *Store) CreateRoute(ctx context.Context, r *Route) error {
	return s.insert(ctx, s.db, "routes", r)
}

func (s *Store) UpdateRoute(ctx context.Context, r *Route) error {
	return s.update(ctx, s.db, "routes", "id", r)
}

func (s *Store) DeleteRoute(ctx context.Context, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM routes WHERE id = ?`, id)
}

// SyncAdvertisedRoutes marks the routes a device currently advertises; new ones are created
// (approved if autoApprove), missing ones are flagged as not advertised.
func (s *Store) SyncAdvertisedRoutes(ctx context.Context, deviceID string, cidrs []string, autoApprove func(cidr string) bool, newID func() string) (changed bool, err error) {
	err = s.Tx(ctx, func(tx *sqlx.Tx) error {
		var existing []Route
		if err := s.sel(ctx, tx, &existing, `SELECT * FROM routes WHERE device_id = ?`, deviceID); err != nil {
			return err
		}
		want := map[string]bool{}
		for _, c := range cidrs {
			want[c] = true
		}
		have := map[string]*Route{}
		for i := range existing {
			have[existing[i].CIDR] = &existing[i]
		}
		for _, r := range existing {
			adv := want[r.CIDR]
			if r.Advertised != adv {
				changed = true
				if _, err := s.exec(ctx, tx, `UPDATE routes SET advertised = ? WHERE id = ?`, adv, r.ID); err != nil {
					return err
				}
			}
		}
		for c := range want {
			if _, ok := have[c]; ok {
				continue
			}
			changed = true
			r := &Route{ID: newID(), DeviceID: deviceID, CIDR: c, Advertised: true, Approved: autoApprove(c), Enabled: true, Priority: 100, CreatedAt: Now()}
			if err := s.insert(ctx, tx, "routes", r); err != nil {
				return err
			}
		}
		return nil
	})
	return changed, err
}

// ---------- setup keys ----------

func (s *Store) CreateSetupKey(ctx context.Context, k *SetupKey) error {
	return s.insert(ctx, s.db, "setup_keys", k)
}

func (s *Store) ListSetupKeys(ctx context.Context) ([]SetupKey, error) {
	var out []SetupKey
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM setup_keys ORDER BY created_at DESC`)
}

func (s *Store) GetSetupKey(ctx context.Context, id string) (*SetupKey, error) {
	var k SetupKey
	return &k, s.get(ctx, s.db, &k, `SELECT * FROM setup_keys WHERE id = ?`, id)
}

func (s *Store) UpdateSetupKey(ctx context.Context, k *SetupKey) error {
	return s.update(ctx, s.db, "setup_keys", "id", k)
}

func (s *Store) DeleteSetupKey(ctx context.Context, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM setup_keys WHERE id = ?`, id)
}

// ConsumeSetupKey atomically validates and increments the use counter of the key with the given hash.
func (s *Store) ConsumeSetupKey(ctx context.Context, keyHash string) (*SetupKey, error) {
	var k SetupKey
	err := s.Tx(ctx, func(tx *sqlx.Tx) error {
		if err := s.get(ctx, tx, &k, `SELECT * FROM setup_keys WHERE key_hash = ?`, keyHash); err != nil {
			return err
		}
		now := Now()
		if !k.Valid(now) {
			return errors.New("setup key is expired, revoked or used up")
		}
		k.Uses++
		k.LastUsedAt = now
		_, err := s.exec(ctx, tx, `UPDATE setup_keys SET uses = ?, last_used_at = ? WHERE id = ?`, k.Uses, now, k.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// ---------- policy ----------

func (s *Store) CurrentPolicy(ctx context.Context) (*PolicyVersion, error) {
	var p PolicyVersion
	return &p, s.get(ctx, s.db, &p, `SELECT * FROM policy_versions ORDER BY version DESC LIMIT 1`)
}

func (s *Store) GetPolicyVersion(ctx context.Context, v int64) (*PolicyVersion, error) {
	var p PolicyVersion
	return &p, s.get(ctx, s.db, &p, `SELECT * FROM policy_versions WHERE version = ?`, v)
}

func (s *Store) ListPolicyVersions(ctx context.Context, limit int) ([]PolicyVersion, error) {
	var out []PolicyVersion
	return out, s.sel(ctx, s.db, &out, `SELECT version, '' AS document, comment, created_by, created_at FROM policy_versions ORDER BY version DESC LIMIT ?`, limit)
}

// SavePolicy stores a new version; expectVersion guards against concurrent edits (0 = no check).
func (s *Store) SavePolicy(ctx context.Context, doc, comment, by string, expectVersion int64) (*PolicyVersion, error) {
	var p PolicyVersion
	err := s.Tx(ctx, func(tx *sqlx.Tx) error {
		var cur int64
		if err := s.get(ctx, tx, &cur, `SELECT COALESCE(MAX(version), 0) FROM policy_versions`); err != nil {
			return err
		}
		if expectVersion > 0 && cur != expectVersion {
			return ErrConflict
		}
		p = PolicyVersion{Version: cur + 1, Document: doc, Comment: comment, CreatedBy: by, CreatedAt: Now()}
		return s.insert(ctx, tx, "policy_versions", &p)
	})
	return &p, err
}

// ---------- API tokens ----------

func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken) error {
	return s.insert(ctx, s.db, "api_tokens", t)
}

func (s *Store) GetAPITokenByHash(ctx context.Context, h string) (*APIToken, error) {
	var t APIToken
	return &t, s.get(ctx, s.db, &t, `SELECT * FROM api_tokens WHERE token_hash = ?`, h)
}

func (s *Store) ListAPITokens(ctx context.Context, userID string) ([]APIToken, error) {
	var out []APIToken
	if userID == "" {
		return out, s.sel(ctx, s.db, &out, `SELECT * FROM api_tokens ORDER BY created_at DESC`)
	}
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
}

func (s *Store) TouchAPIToken(ctx context.Context, id string) error {
	_, err := s.exec(ctx, s.db, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, Now(), id)
	return err
}

func (s *Store) DeleteAPIToken(ctx context.Context, id, userID string) error {
	if userID == "" {
		return s.execOne(ctx, s.db, `DELETE FROM api_tokens WHERE id = ?`, id)
	}
	return s.execOne(ctx, s.db, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
}

// ---------- audit ----------

// AppendAudit adds a hash-chained entry: hash = sha256(prev_hash || canonical fields).
func (s *Store) AppendAudit(ctx context.Context, e *AuditEntry) error {
	for attempt := 0; attempt < 5; attempt++ {
		err := s.Tx(ctx, func(tx *sqlx.Tx) error {
			var last struct {
				Seq  int64  `db:"seq"`
				Hash string `db:"hash"`
			}
			err := s.get(ctx, tx, &last, `SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if errors.Is(err, ErrNotFound) {
				last.Hash = strings.Repeat("0", 64)
			}
			e.Seq = last.Seq + 1
			e.PrevHash = last.Hash
			e.Hash = AuditHash(e)
			return s.insert(ctx, tx, "audit_log", e)
		})
		if err == nil || !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return errors.New("audit append: too much contention")
}

func AuditHash(e *AuditEntry) string {
	h := sha256.New()
	for _, part := range []string{
		e.PrevHash, itoa(e.Seq), itoa(e.TS), e.ActorID, e.Actor, e.Action,
		e.TargetType, e.TargetID, e.TargetName, e.Details, e.IP,
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type AuditFilter struct {
	Action    string
	ActorID   string
	TargetID  string
	Search    string
	Since     int64
	Until     int64
	BeforeSeq int64
	Limit     int
}

func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	where := []string{"1=1"}
	var args []any
	if f.Action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, f.Action+"%")
	}
	if f.ActorID != "" {
		where = append(where, "actor_id = ?")
		args = append(args, f.ActorID)
	}
	if f.TargetID != "" {
		where = append(where, "target_id = ?")
		args = append(args, f.TargetID)
	}
	if f.Search != "" {
		where = append(where, "(LOWER(actor) LIKE ? OR LOWER(target_name) LIKE ? OR LOWER(action) LIKE ?)")
		pat := "%" + strings.ToLower(f.Search) + "%"
		args = append(args, pat, pat, pat)
	}
	if f.Since > 0 {
		where = append(where, "ts >= ?")
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		where = append(where, "ts <= ?")
		args = append(args, f.Until)
	}
	if f.BeforeSeq > 0 {
		where = append(where, "seq < ?")
		args = append(args, f.BeforeSeq)
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	args = append(args, f.Limit)
	var out []AuditEntry
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM audit_log WHERE `+strings.Join(where, " AND ")+` ORDER BY seq DESC LIMIT ?`, args...)
}

// VerifyAuditChain recomputes the hash chain; returns the first broken seq or 0.
func (s *Store) VerifyAuditChain(ctx context.Context) (int64, int64, error) {
	rows, err := s.db.QueryxContext(ctx, `SELECT * FROM audit_log ORDER BY seq`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	prev := strings.Repeat("0", 64)
	var n int64
	for rows.Next() {
		var e AuditEntry
		if err := rows.StructScan(&e); err != nil {
			return 0, n, err
		}
		n++
		if e.PrevHash != prev || AuditHash(&e) != e.Hash {
			return e.Seq, n, nil
		}
		prev = e.Hash
	}
	return 0, n, rows.Err()
}

// ---------- webhooks ----------

func (s *Store) CreateWebhook(ctx context.Context, w *Webhook) error {
	return s.insert(ctx, s.db, "webhooks", w)
}

func (s *Store) UpdateWebhook(ctx context.Context, w *Webhook) error {
	return s.update(ctx, s.db, "webhooks", "id", w)
}

func (s *Store) GetWebhook(ctx context.Context, id string) (*Webhook, error) {
	var w Webhook
	return &w, s.get(ctx, s.db, &w, `SELECT * FROM webhooks WHERE id = ?`, id)
}

func (s *Store) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	var out []Webhook
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM webhooks ORDER BY created_at`)
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM webhooks WHERE id = ?`, id)
}

func (s *Store) RecordWebhookDelivery(ctx context.Context, id string, status int, errMsg string) error {
	_, err := s.exec(ctx, s.db, `UPDATE webhooks SET last_status = ?, last_error = ?, last_delivery_at = ? WHERE id = ?`, status, errMsg, Now(), id)
	return err
}

// ---------- OIDC providers ----------

func (s *Store) CreateOIDCProvider(ctx context.Context, p *OIDCProvider) error {
	return s.insert(ctx, s.db, "oidc_providers", p)
}

func (s *Store) UpdateOIDCProvider(ctx context.Context, p *OIDCProvider) error {
	return s.update(ctx, s.db, "oidc_providers", "id", p)
}

func (s *Store) GetOIDCProvider(ctx context.Context, id string) (*OIDCProvider, error) {
	var p OIDCProvider
	return &p, s.get(ctx, s.db, &p, `SELECT * FROM oidc_providers WHERE id = ?`, id)
}

func (s *Store) ListOIDCProviders(ctx context.Context) ([]OIDCProvider, error) {
	var out []OIDCProvider
	return out, s.sel(ctx, s.db, &out, `SELECT * FROM oidc_providers ORDER BY name`)
}

func (s *Store) DeleteOIDCProvider(ctx context.Context, id string) error {
	return s.execOne(ctx, s.db, `DELETE FROM oidc_providers WHERE id = ?`, id)
}

// ---------- device logins ----------

func (s *Store) CreateDeviceLogin(ctx context.Context, l *DeviceLogin) error {
	return s.insert(ctx, s.db, "device_logins", l)
}

func (s *Store) GetDeviceLogin(ctx context.Context, id string) (*DeviceLogin, error) {
	var l DeviceLogin
	return &l, s.get(ctx, s.db, &l, `SELECT * FROM device_logins WHERE id = ?`, id)
}

func (s *Store) GetDeviceLoginByCode(ctx context.Context, code string) (*DeviceLogin, error) {
	var l DeviceLogin
	return &l, s.get(ctx, s.db, &l, `SELECT * FROM device_logins WHERE user_code = ?`, code)
}

func (s *Store) GetDeviceLoginByToken(ctx context.Context, tokenHash string) (*DeviceLogin, error) {
	var l DeviceLogin
	return &l, s.get(ctx, s.db, &l, `SELECT * FROM device_logins WHERE login_token_hash = ? AND login_token_hash <> ''`, tokenHash)
}

func (s *Store) UpdateDeviceLogin(ctx context.Context, l *DeviceLogin) error {
	return s.update(ctx, s.db, "device_logins", "id", l)
}

func (s *Store) SetLastSeen(ctx context.Context, id string, ts int64) error {
	_, err := s.exec(ctx, s.db, `UPDATE devices SET last_seen_at = ? WHERE id = ?`, ts, id)
	return err
}
