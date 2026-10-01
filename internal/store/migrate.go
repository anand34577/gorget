package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// migrations are applied in order and never edited once released.
// The schema uses only types both SQLite and PostgreSQL understand.
var migrations = []string{
	// 1: initial schema
	`
CREATE TABLE settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	updated_at BIGINT NOT NULL
);

CREATE TABLE users (
	id               TEXT PRIMARY KEY,
	email            TEXT NOT NULL UNIQUE,
	name             TEXT NOT NULL DEFAULT '',
	password_hash    TEXT NOT NULL DEFAULT '',
	role             TEXT NOT NULL,
	provider         TEXT NOT NULL DEFAULT 'local',
	provider_subject TEXT NOT NULL DEFAULT '',
	totp_secret      TEXT NOT NULL DEFAULT '',
	totp_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
	recovery_codes   TEXT NOT NULL DEFAULT '[]',
	disabled         BOOLEAN NOT NULL DEFAULT FALSE,
	must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
	failed_logins    INTEGER NOT NULL DEFAULT 0,
	locked_until     BIGINT NOT NULL DEFAULT 0,
	created_at       BIGINT NOT NULL,
	last_login_at    BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX users_provider_idx ON users (provider, provider_subject);

CREATE TABLE webauthn_credentials (
	id           TEXT PRIMARY KEY,
	user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name         TEXT NOT NULL DEFAULT '',
	credential   TEXT NOT NULL,
	created_at   BIGINT NOT NULL,
	last_used_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX webauthn_user_idx ON webauthn_credentials (user_id);

CREATE TABLE sessions (
	id           TEXT PRIMARY KEY,
	user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	csrf_token   TEXT NOT NULL,
	mfa_pending  BOOLEAN NOT NULL DEFAULT FALSE,
	created_at   BIGINT NOT NULL,
	expires_at   BIGINT NOT NULL,
	last_seen_at BIGINT NOT NULL,
	reauth_at    BIGINT NOT NULL DEFAULT 0,
	ip           TEXT NOT NULL DEFAULT '',
	user_agent   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE user_groups (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL UNIQUE,
	description TEXT NOT NULL DEFAULT '',
	source      TEXT NOT NULL DEFAULT 'local',
	created_at  BIGINT NOT NULL
);

CREATE TABLE group_members (
	group_id TEXT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
	user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (group_id, user_id)
);

CREATE TABLE devices (
	id                  TEXT PRIMARY KEY,
	name                TEXT NOT NULL UNIQUE,
	kind                TEXT NOT NULL,
	user_id             TEXT REFERENCES users(id) ON DELETE CASCADE,
	machine_key         TEXT UNIQUE,
	wg_public_key       TEXT NOT NULL UNIQUE,
	disco_key           TEXT NOT NULL DEFAULT '',
	ipv4                TEXT NOT NULL UNIQUE,
	ipv6                TEXT NOT NULL UNIQUE,
	static_ip           BOOLEAN NOT NULL DEFAULT FALSE,
	tags                TEXT NOT NULL DEFAULT '[]',
	state               TEXT NOT NULL,
	ephemeral           BOOLEAN NOT NULL DEFAULT FALSE,
	key_expiry_disabled BOOLEAN NOT NULL DEFAULT FALSE,
	key_expires_at      BIGINT NOT NULL DEFAULT 0,
	hostname            TEXT NOT NULL DEFAULT '',
	os                  TEXT NOT NULL DEFAULT '',
	os_version          TEXT NOT NULL DEFAULT '',
	client_version      TEXT NOT NULL DEFAULT '',
	arch                TEXT NOT NULL DEFAULT '',
	endpoints           TEXT NOT NULL DEFAULT '[]',
	home_relay          TEXT NOT NULL DEFAULT '',
	exit_advertised     BOOLEAN NOT NULL DEFAULT FALSE,
	exit_approved       BOOLEAN NOT NULL DEFAULT FALSE,
	setup_key_id        TEXT NOT NULL DEFAULT '',
	psk                 TEXT NOT NULL DEFAULT '',
	tunnel_mode         TEXT NOT NULL DEFAULT '',
	custom_allowed_ips  TEXT NOT NULL DEFAULT '[]',
	dns_enabled         BOOLEAN NOT NULL DEFAULT TRUE,
	expires_at          BIGINT NOT NULL DEFAULT 0,
	last_seen_at        BIGINT NOT NULL DEFAULT 0,
	last_endpoint       TEXT NOT NULL DEFAULT '',
	rx_bytes            BIGINT NOT NULL DEFAULT 0,
	tx_bytes            BIGINT NOT NULL DEFAULT 0,
	created_at          BIGINT NOT NULL,
	updated_at          BIGINT NOT NULL
);
CREATE INDEX devices_user_idx ON devices (user_id);
CREATE INDEX devices_kind_idx ON devices (kind);

CREATE TABLE routes (
	id         TEXT PRIMARY KEY,
	device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	cidr       TEXT NOT NULL,
	advertised BOOLEAN NOT NULL DEFAULT TRUE,
	approved   BOOLEAN NOT NULL DEFAULT FALSE,
	enabled    BOOLEAN NOT NULL DEFAULT TRUE,
	priority   INTEGER NOT NULL DEFAULT 100,
	created_at BIGINT NOT NULL,
	UNIQUE (device_id, cidr)
);

CREATE TABLE setup_keys (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	key_hash     TEXT NOT NULL UNIQUE,
	key_prefix   TEXT NOT NULL,
	reusable     BOOLEAN NOT NULL DEFAULT FALSE,
	ephemeral    BOOLEAN NOT NULL DEFAULT FALSE,
	auto_approve BOOLEAN NOT NULL DEFAULT TRUE,
	tags         TEXT NOT NULL DEFAULT '[]',
	max_uses     INTEGER NOT NULL DEFAULT 0,
	uses         INTEGER NOT NULL DEFAULT 0,
	expires_at   BIGINT NOT NULL DEFAULT 0,
	revoked      BOOLEAN NOT NULL DEFAULT FALSE,
	created_by   TEXT NOT NULL DEFAULT '',
	created_at   BIGINT NOT NULL,
	last_used_at BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE policy_versions (
	version    BIGINT PRIMARY KEY,
	document   TEXT NOT NULL,
	comment    TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	created_at BIGINT NOT NULL
);

CREATE TABLE api_tokens (
	id           TEXT PRIMARY KEY,
	name         TEXT NOT NULL,
	user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash   TEXT NOT NULL UNIQUE,
	token_prefix TEXT NOT NULL,
	scopes       TEXT NOT NULL DEFAULT '[]',
	expires_at   BIGINT NOT NULL DEFAULT 0,
	last_used_at BIGINT NOT NULL DEFAULT 0,
	created_at   BIGINT NOT NULL
);

CREATE TABLE audit_log (
	seq         BIGINT PRIMARY KEY,
	ts          BIGINT NOT NULL,
	actor_id    TEXT NOT NULL DEFAULT '',
	actor       TEXT NOT NULL DEFAULT '',
	action      TEXT NOT NULL,
	target_type TEXT NOT NULL DEFAULT '',
	target_id   TEXT NOT NULL DEFAULT '',
	target_name TEXT NOT NULL DEFAULT '',
	details     TEXT NOT NULL DEFAULT '{}',
	ip          TEXT NOT NULL DEFAULT '',
	prev_hash   TEXT NOT NULL,
	hash        TEXT NOT NULL
);
CREATE INDEX audit_ts_idx ON audit_log (ts);

CREATE TABLE webhooks (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL DEFAULT '',
	url              TEXT NOT NULL,
	secret           TEXT NOT NULL,
	events           TEXT NOT NULL DEFAULT '[]',
	enabled          BOOLEAN NOT NULL DEFAULT TRUE,
	last_status      INTEGER NOT NULL DEFAULT 0,
	last_error       TEXT NOT NULL DEFAULT '',
	last_delivery_at BIGINT NOT NULL DEFAULT 0,
	created_at       BIGINT NOT NULL
);

CREATE TABLE oidc_providers (
	id                TEXT PRIMARY KEY,
	name              TEXT NOT NULL UNIQUE,
	issuer            TEXT NOT NULL,
	client_id         TEXT NOT NULL,
	client_secret     TEXT NOT NULL DEFAULT '',
	scopes            TEXT NOT NULL DEFAULT '[]',
	groups_claim      TEXT NOT NULL DEFAULT 'groups',
	allowed_domains   TEXT NOT NULL DEFAULT '[]',
	auto_create_users BOOLEAN NOT NULL DEFAULT TRUE,
	default_role      TEXT NOT NULL DEFAULT 'user',
	sync_groups       BOOLEAN NOT NULL DEFAULT TRUE,
	enabled           BOOLEAN NOT NULL DEFAULT TRUE,
	created_at        BIGINT NOT NULL
);

CREATE TABLE device_logins (
	id               TEXT PRIMARY KEY,
	user_code        TEXT NOT NULL UNIQUE,
	machine_key      TEXT NOT NULL,
	hostname         TEXT NOT NULL DEFAULT '',
	os               TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL,
	user_id          TEXT NOT NULL DEFAULT '',
	login_token_hash TEXT NOT NULL DEFAULT '',
	created_at       BIGINT NOT NULL,
	expires_at       BIGINT NOT NULL
);
`,
	// 2: device posture (0 = unknown, 1 = yes, 2 = no) and the public address devices connect from
	`
ALTER TABLE devices ADD COLUMN disk_encrypted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN firewall_on INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN public_ip TEXT NOT NULL DEFAULT ''
`,
	// 3: shared state for running several server instances on one database (high availability)
	`
CREATE TABLE pending_state (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	expires_at BIGINT NOT NULL
);

CREATE TABLE presence (
	device_id   TEXT PRIMARY KEY,
	instance_id TEXT NOT NULL,
	updated_at  BIGINT NOT NULL
);
CREATE INDEX presence_instance_idx ON presence (instance_id);

CREATE TABLE instances (
	id         TEXT PRIMARY KEY,
	region     TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	relay_url  TEXT NOT NULL DEFAULT '',
	udp_addr   TEXT NOT NULL DEFAULT '',
	peer_addr  TEXT NOT NULL DEFAULT '',
	started_at BIGINT NOT NULL,
	last_seen  BIGINT NOT NULL
);

CREATE TABLE relay_presence (
	key         TEXT PRIMARY KEY,
	instance_id TEXT NOT NULL,
	updated_at  BIGINT NOT NULL
);
CREATE INDEX relay_presence_instance_idx ON relay_presence (instance_id);

CREATE TABLE locks (
	name       TEXT PRIMARY KEY,
	owner      TEXT NOT NULL,
	expires_at BIGINT NOT NULL
);

CREATE TABLE kv_storage (
	key      TEXT PRIMARY KEY,
	value    TEXT NOT NULL,
	modified BIGINT NOT NULL
)
`,
	// 4: temporary access requests and grants
	`
CREATE TABLE access_requests (
	id            TEXT PRIMARY KEY,
	requester_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	requester     TEXT NOT NULL,
	target        TEXT NOT NULL,
	ports         TEXT NOT NULL DEFAULT '*',
	reason        TEXT NOT NULL DEFAULT '',
	minutes       INTEGER NOT NULL,
	status        TEXT NOT NULL,
	decided_by    TEXT NOT NULL DEFAULT '',
	decided_at    BIGINT NOT NULL DEFAULT 0,
	granted_until BIGINT NOT NULL DEFAULT 0,
	created_at    BIGINT NOT NULL
);
CREATE INDEX access_requests_user_idx ON access_requests (requester_id);
CREATE INDEX access_requests_status_idx ON access_requests (status)
`,
	// 5: connection history and usage statistics
	`
CREATE TABLE device_sessions (
	id             TEXT PRIMARY KEY,
	device_id      TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
	started_at     BIGINT NOT NULL,
	ended_at       BIGINT NOT NULL DEFAULT 0,
	public_ip      TEXT NOT NULL DEFAULT '',
	country        TEXT NOT NULL DEFAULT '',
	client_version TEXT NOT NULL DEFAULT ''
);
CREATE INDEX device_sessions_device_idx ON device_sessions (device_id, started_at);
CREATE INDEX device_sessions_open_idx ON device_sessions (ended_at);

CREATE TABLE stats_samples (
	ts       BIGINT PRIMARY KEY,
	total    INTEGER NOT NULL,
	online   INTEGER NOT NULL,
	pending  INTEGER NOT NULL DEFAULT 0,
	disabled INTEGER NOT NULL DEFAULT 0,
	rx       BIGINT NOT NULL DEFAULT 0,
	tx       BIGINT NOT NULL DEFAULT 0
)
`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int
	if err := s.db.GetContext(ctx, &current, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`); err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d); upgrade gorget-server", current, len(migrations))
	}
	for i := current; i < len(migrations); i++ {
		v := i + 1
		err := s.Tx(ctx, func(tx *sqlx.Tx) error {
			for _, stmt := range splitStatements(migrations[i]) {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("migration %d: %w\n%s", v, err, stmt)
				}
			}
			_, err := tx.ExecContext(ctx, s.rb(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`), v, Now())
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func splitStatements(sqlText string) []string {
	var out []string
	for _, part := range strings.Split(sqlText, ";") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Tables lists all data tables in dependency order (used by backup/migration tools).
var Tables = []string{
	"settings", "users", "webauthn_credentials", "sessions", "user_groups", "group_members",
	"devices", "routes", "setup_keys", "policy_versions", "api_tokens", "audit_log",
	"webhooks", "oidc_providers", "device_logins", "access_requests",
	"device_sessions", "stats_samples",
}
