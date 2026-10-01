package store

import "database/sql"

// Roles.
const (
	RoleOwner        = "owner"
	RoleAdmin        = "admin"
	RoleNetworkAdmin = "network_admin"
	RoleAuditor      = "auditor"
	RoleUser         = "user"
)

var Roles = []string{RoleOwner, RoleAdmin, RoleNetworkAdmin, RoleAuditor, RoleUser}

func ValidRole(r string) bool {
	for _, x := range Roles {
		if x == r {
			return true
		}
	}
	return false
}

// Device kinds.
const (
	KindNative    = "native"    // Gorget client
	KindWireGuard = "wireguard" // standard WireGuard client behind the gateway
	KindGateway   = "gateway"   // built-in gateway node
)

// Device states.
const (
	StateActive   = "active"
	StatePending  = "pending"
	StateDisabled = "disabled"
)

// Tunnel modes for standard WireGuard configs.
const (
	TunnelFull   = "full"
	TunnelSplit  = "split"
	TunnelCustom = "custom"
)

type User struct {
	ID                 string     `db:"id" json:"id"`
	Email              string     `db:"email" json:"email"`
	Name               string     `db:"name" json:"name"`
	PasswordHash       string     `db:"password_hash" json:"-"`
	Role               string     `db:"role" json:"role"`
	Provider           string     `db:"provider" json:"provider"`
	ProviderSubject    string     `db:"provider_subject" json:"-"`
	TOTPSecret         string     `db:"totp_secret" json:"-"`
	TOTPEnabled        bool       `db:"totp_enabled" json:"totp_enabled"`
	RecoveryCodes      StringList `db:"recovery_codes" json:"-"`
	Disabled           bool       `db:"disabled" json:"disabled"`
	MustChangePassword bool       `db:"must_change_password" json:"must_change_password"`
	FailedLogins       int        `db:"failed_logins" json:"-"`
	LockedUntil        int64      `db:"locked_until" json:"locked_until"`
	CreatedAt          int64      `db:"created_at" json:"created_at"`
	LastLoginAt        int64      `db:"last_login_at" json:"last_login_at"`
}

type WebAuthnCredential struct {
	ID         string `db:"id" json:"id"`
	UserID     string `db:"user_id" json:"-"`
	Name       string `db:"name" json:"name"`
	Credential string `db:"credential" json:"-"`
	CreatedAt  int64  `db:"created_at" json:"created_at"`
	LastUsedAt int64  `db:"last_used_at" json:"last_used_at"`
}

type Session struct {
	ID         string `db:"id"` // SHA-256 of the session token
	UserID     string `db:"user_id"`
	CSRFToken  string `db:"csrf_token"`
	MFAPending bool   `db:"mfa_pending"`
	CreatedAt  int64  `db:"created_at"`
	ExpiresAt  int64  `db:"expires_at"`
	LastSeenAt int64  `db:"last_seen_at"`
	ReauthAt   int64  `db:"reauth_at"`
	IP         string `db:"ip"`
	UserAgent  string `db:"user_agent"`
}

type Group struct {
	ID          string   `db:"id" json:"id"`
	Name        string   `db:"name" json:"name"`
	Description string   `db:"description" json:"description"`
	Source      string   `db:"source" json:"source"`
	CreatedAt   int64    `db:"created_at" json:"created_at"`
	Members     []string `db:"-" json:"members"`
}

type Device struct {
	ID                string         `db:"id" json:"id"`
	Name              string         `db:"name" json:"name"`
	Kind              string         `db:"kind" json:"kind"`
	UserID            sql.NullString `db:"user_id" json:"-"`
	MachineKey        sql.NullString `db:"machine_key" json:"-"`
	WGPublicKey       string         `db:"wg_public_key" json:"wg_public_key"`
	DiscoKey          string         `db:"disco_key" json:"-"`
	IPv4              string         `db:"ipv4" json:"ipv4"`
	IPv6              string         `db:"ipv6" json:"ipv6"`
	StaticIP          bool           `db:"static_ip" json:"static_ip"`
	Tags              StringList     `db:"tags" json:"tags"`
	State             string         `db:"state" json:"state"`
	Ephemeral         bool           `db:"ephemeral" json:"ephemeral"`
	KeyExpiryDisabled bool           `db:"key_expiry_disabled" json:"key_expiry_disabled"`
	KeyExpiresAt      int64          `db:"key_expires_at" json:"key_expires_at"`
	Hostname          string         `db:"hostname" json:"hostname"`
	OS                string         `db:"os" json:"os"`
	OSVersion         string         `db:"os_version" json:"os_version"`
	ClientVersion     string         `db:"client_version" json:"client_version"`
	Arch              string         `db:"arch" json:"arch"`
	DiskEncrypted     int            `db:"disk_encrypted" json:"disk_encrypted"` // 0 unknown, 1 yes, 2 no
	FirewallOn        int            `db:"firewall_on" json:"firewall_on"`
	PublicIP          string         `db:"public_ip" json:"public_ip"`
	Endpoints         StringList     `db:"endpoints" json:"endpoints"`
	HomeRelay         string         `db:"home_relay" json:"home_relay"`
	ExitAdvertised    bool           `db:"exit_advertised" json:"exit_advertised"`
	ExitApproved      bool           `db:"exit_approved" json:"exit_approved"`
	SetupKeyID        string         `db:"setup_key_id" json:"setup_key_id"`
	PSK               string         `db:"psk" json:"-"`
	TunnelMode        string         `db:"tunnel_mode" json:"tunnel_mode"`
	CustomAllowedIPs  StringList     `db:"custom_allowed_ips" json:"custom_allowed_ips"`
	DNSEnabled        bool           `db:"dns_enabled" json:"dns_enabled"`
	ExpiresAt         int64          `db:"expires_at" json:"expires_at"`
	LastSeenAt        int64          `db:"last_seen_at" json:"last_seen_at"`
	LastEndpoint      string         `db:"last_endpoint" json:"last_endpoint"`
	RxBytes           int64          `db:"rx_bytes" json:"rx_bytes"`
	TxBytes           int64          `db:"tx_bytes" json:"tx_bytes"`
	CreatedAt         int64          `db:"created_at" json:"created_at"`
	UpdatedAt         int64          `db:"updated_at" json:"updated_at"`
}

func (d *Device) Owner() string {
	if d.UserID.Valid {
		return d.UserID.String
	}
	return ""
}

// HasTags reports whether the device is tag-owned (tagged devices lose user identity in ACLs).
func (d *Device) HasTags() bool { return len(d.Tags) > 0 }

type Route struct {
	ID         string `db:"id" json:"id"`
	DeviceID   string `db:"device_id" json:"device_id"`
	CIDR       string `db:"cidr" json:"cidr"`
	Advertised bool   `db:"advertised" json:"advertised"`
	Approved   bool   `db:"approved" json:"approved"`
	Enabled    bool   `db:"enabled" json:"enabled"`
	Priority   int    `db:"priority" json:"priority"`
	CreatedAt  int64  `db:"created_at" json:"created_at"`
}

type SetupKey struct {
	ID          string     `db:"id" json:"id"`
	Name        string     `db:"name" json:"name"`
	KeyHash     string     `db:"key_hash" json:"-"`
	KeyPrefix   string     `db:"key_prefix" json:"key_prefix"`
	Reusable    bool       `db:"reusable" json:"reusable"`
	Ephemeral   bool       `db:"ephemeral" json:"ephemeral"`
	AutoApprove bool       `db:"auto_approve" json:"auto_approve"`
	Tags        StringList `db:"tags" json:"tags"`
	MaxUses     int        `db:"max_uses" json:"max_uses"`
	Uses        int        `db:"uses" json:"uses"`
	ExpiresAt   int64      `db:"expires_at" json:"expires_at"`
	Revoked     bool       `db:"revoked" json:"revoked"`
	CreatedBy   string     `db:"created_by" json:"created_by"`
	CreatedAt   int64      `db:"created_at" json:"created_at"`
	LastUsedAt  int64      `db:"last_used_at" json:"last_used_at"`
}

// Valid reports whether the key can still be used at time now.
func (k *SetupKey) Valid(now int64) bool {
	if k.Revoked {
		return false
	}
	if k.ExpiresAt > 0 && now > k.ExpiresAt {
		return false
	}
	if !k.Reusable && k.Uses > 0 {
		return false
	}
	if k.MaxUses > 0 && k.Uses >= k.MaxUses {
		return false
	}
	return true
}

type PolicyVersion struct {
	Version   int64  `db:"version" json:"version"`
	Document  string `db:"document" json:"document"`
	Comment   string `db:"comment" json:"comment"`
	CreatedBy string `db:"created_by" json:"created_by"`
	CreatedAt int64  `db:"created_at" json:"created_at"`
}

type APIToken struct {
	ID          string     `db:"id" json:"id"`
	Name        string     `db:"name" json:"name"`
	UserID      string     `db:"user_id" json:"user_id"`
	TokenHash   string     `db:"token_hash" json:"-"`
	TokenPrefix string     `db:"token_prefix" json:"token_prefix"`
	Scopes      StringList `db:"scopes" json:"scopes"`
	ExpiresAt   int64      `db:"expires_at" json:"expires_at"`
	LastUsedAt  int64      `db:"last_used_at" json:"last_used_at"`
	CreatedAt   int64      `db:"created_at" json:"created_at"`
}

type AuditEntry struct {
	Seq        int64  `db:"seq" json:"seq"`
	TS         int64  `db:"ts" json:"ts"`
	ActorID    string `db:"actor_id" json:"actor_id"`
	Actor      string `db:"actor" json:"actor"`
	Action     string `db:"action" json:"action"`
	TargetType string `db:"target_type" json:"target_type"`
	TargetID   string `db:"target_id" json:"target_id"`
	TargetName string `db:"target_name" json:"target_name"`
	Details    string `db:"details" json:"details"`
	IP         string `db:"ip" json:"ip"`
	PrevHash   string `db:"prev_hash" json:"prev_hash"`
	Hash       string `db:"hash" json:"hash"`
}

type Webhook struct {
	ID             string     `db:"id" json:"id"`
	Name           string     `db:"name" json:"name"`
	URL            string     `db:"url" json:"url"`
	Secret         string     `db:"secret" json:"-"`
	Events         StringList `db:"events" json:"events"`
	Enabled        bool       `db:"enabled" json:"enabled"`
	LastStatus     int        `db:"last_status" json:"last_status"`
	LastError      string     `db:"last_error" json:"last_error"`
	LastDeliveryAt int64      `db:"last_delivery_at" json:"last_delivery_at"`
	CreatedAt      int64      `db:"created_at" json:"created_at"`
}

type OIDCProvider struct {
	ID              string     `db:"id" json:"id"`
	Name            string     `db:"name" json:"name"`
	Issuer          string     `db:"issuer" json:"issuer"`
	ClientID        string     `db:"client_id" json:"client_id"`
	ClientSecret    string     `db:"client_secret" json:"-"`
	Scopes          StringList `db:"scopes" json:"scopes"`
	GroupsClaim     string     `db:"groups_claim" json:"groups_claim"`
	AllowedDomains  StringList `db:"allowed_domains" json:"allowed_domains"`
	AutoCreateUsers bool       `db:"auto_create_users" json:"auto_create_users"`
	DefaultRole     string     `db:"default_role" json:"default_role"`
	SyncGroups      bool       `db:"sync_groups" json:"sync_groups"`
	Enabled         bool       `db:"enabled" json:"enabled"`
	CreatedAt       int64      `db:"created_at" json:"created_at"`
}

type DeviceLogin struct {
	ID             string `db:"id"`
	UserCode       string `db:"user_code"`
	MachineKey     string `db:"machine_key"`
	Hostname       string `db:"hostname"`
	OS             string `db:"os"`
	Status         string `db:"status"` // pending, approved, denied, consumed
	UserID         string `db:"user_id"`
	LoginTokenHash string `db:"login_token_hash"`
	CreatedAt      int64  `db:"created_at"`
	ExpiresAt      int64  `db:"expires_at"`
}
