export type Role = "owner" | "admin" | "network_admin" | "auditor" | "user";

export interface User {
  id: string;
  email: string;
  name: string;
  role: Role;
  provider: string;
  totp_enabled: boolean;
  disabled: boolean;
  must_change_password: boolean;
  locked_until: number;
  created_at: number;
  last_login_at: number;
}

export interface UserView extends User {
  groups: string[];
  device_count: number;
  passkeys: number;
}

export interface Me {
  user: User;
  permissions: string[];
  mfa_enrollment_required: boolean;
  passkeys: number;
  csrf_token?: string;
  network: { name: string; domain: string };
  features: { user_wg_configs: boolean; user_setup_keys: boolean; gateway: boolean };
  version: string;
  public_url: string;
  /** Routing preselected when adding a WireGuard app ("split" or "full"). */
  wg_tunnel_mode?: string;
}

export interface Route {
  id: string;
  device_id: string;
  cidr: string;
  advertised: boolean;
  approved: boolean;
  enabled: boolean;
  priority: number;
  created_at: number;
}

export type DeviceKind = "native" | "wireguard" | "gateway";

export interface Device {
  id: string;
  name: string;
  kind: DeviceKind;
  wg_public_key: string;
  ipv4: string;
  ipv6: string;
  static_ip: boolean;
  tags: string[];
  state: "active" | "pending" | "disabled";
  ephemeral: boolean;
  key_expiry_disabled: boolean;
  key_expires_at: number;
  hostname: string;
  os: string;
  os_version: string;
  client_version: string;
  arch: string;
  endpoints: string[];
  home_relay: string;
  exit_advertised: boolean;
  exit_approved: boolean;
  tunnel_mode: string;
  custom_allowed_ips: string[];
  dns_enabled: boolean;
  expires_at: number;
  last_seen_at: number;
  last_endpoint: string;
  rx_bytes: number;
  tx_bytes: number;
  created_at: number;
  online: boolean;
  user_id: string;
  user_email: string;
  fqdn: string;
  routes: Route[];
  key_expired: boolean;
  has_psk: boolean;
  peer_count: number;
  /** 0 unknown, 1 yes, 2 no */
  disk_encrypted: number;
  firewall_on: number;
  public_ip: string;
  /** Security rules the device breaks (empty = compliant). */
  posture: string[];
  /** Public address the device connects from, and its country code ("" when unknown). */
  remote_ip: string;
  country: string;
}

export interface Group {
  id: string;
  name: string;
  description: string;
  source: string;
  members: string[];
  created_at: number;
}

export interface SetupKey {
  id: string;
  name: string;
  key_prefix: string;
  reusable: boolean;
  ephemeral: boolean;
  auto_approve: boolean;
  tags: string[];
  max_uses: number;
  uses: number;
  expires_at: number;
  revoked: boolean;
  created_by: string;
  created_at: number;
  last_used_at: number;
}

export interface AuditEntry {
  seq: number;
  ts: number;
  actor_id: string;
  actor: string;
  action: string;
  target_type: string;
  target_id: string;
  target_name: string;
  details: string;
  ip: string;
}

export interface PolicyVersion {
  version: number;
  document: string;
  comment: string;
  created_by: string;
  created_at: number;
}

export interface TestResult {
  src: string;
  dst: string;
  expect: string;
  passed: boolean;
  decision: { allowed: boolean; rule_id?: string; reason: string };
  error?: string;
}

export interface PolicyAnalysis {
  valid: boolean;
  problems: string[];
  tests: TestResult[];
  rules: number;
  parsed?: Policy;
}

export interface PolicyRule {
  id?: string;
  description?: string;
  action: "accept";
  src: string[];
  dst: string[];
  proto?: string;
  disabled?: boolean;
  expires?: string;
}

export interface Policy {
  tagOwners?: Record<string, string[]>;
  hosts?: Record<string, string>;
  acls: PolicyRule[];
  autoApprovers?: { routes?: Record<string, string[]>; exitNode?: string[] };
  tests?: { src: string; proto?: string; accept?: string[]; deny?: string[] }[];
}

export interface GatewayStatus {
  enabled: boolean;
  running: boolean;
  backend: string;
  interface: string;
  endpoint: string;
  public_key: string;
  peers: number;
  error?: string;
}

export interface Overview {
  devices: { total: number; online: number; pending: number; native: number; wireguard: number; expiring_soon: number; non_compliant: number };
  os: Record<string, number>;
  pending_routes: number;
  pending_access: number;
  users: number;
  groups: number;
  policy_version: number;
  policy_error: string;
  network: { name: string; ipv4: string; ipv6: string; domain: string; capacity: number };
  version: string;
  gateway?: GatewayStatus;
  relay?: { enabled: boolean; connections?: number; bytes?: number };
  checks?: { level: "warn" | "danger"; text: string; to: string }[];
  recent_activity?: AuditEntry[];
}

export interface Settings {
  network: { name: string; ipv4: string; ipv6: string; ipv6_enabled: boolean; domain: string; reserved: string[] | null; pools: Record<string, string> };
  dns: {
    magic_dns: boolean;
    nameservers: string[] | null;
    search_domains: string[] | null;
    split: { domain: string; nameservers: string[] }[] | null;
    records: { name: string; type: string; value: string }[] | null;
    override_local: boolean;
    fail_open: boolean;
  };
  devices: { approval_required: boolean; key_expiry_days: number; ephemeral_timeout_mins: number; inactive_cleanup_days: number };
  client: {
    allow_lan_default: boolean;
    kill_switch_enforced: boolean;
    forced_exit_node_id: string;
    default_exit_node_id: string;
    mtu: number;
    allow_user_exit_choice: boolean;
    allow_custom_dns: boolean;
  };
  auth: {
    password_login: boolean;
    mfa_required: boolean;
    mfa_required_admins: boolean;
    user_wg_configs: boolean;
    user_setup_keys: boolean;
    lockout_threshold: number;
    lockout_minutes: number;
  };
  gateway: { exit_node: boolean; default_tunnel_mode: string; split_routes: string[] | null; default_expiry_days: number; persistent_keepalive: number };
  posture: Posture;
  routing: { domain_routes: DomainRoute[] | null };
  notifications?: NotificationSettings;
}

export interface Posture {
  enabled: boolean;
  mode: "enforce" | "report";
  min_client_version: string;
  min_os_version: Record<string, string> | null;
  require_disk_encryption: boolean;
  require_firewall: boolean;
  allowed_networks: string[] | null;
  allowed_countries: string[] | null;
  blocked_countries: string[] | null;
  allowed_os: string[] | null;
  geo_auto_update: boolean;
  exempt_tags: string[] | null;
}

export interface DeviceSession {
  id: string;
  device_id: string;
  device_name?: string;
  started_at: number;
  ended_at: number;
  public_ip: string;
  country: string;
  client_version: string;
}

export interface StatsPoint {
  ts: number;
  online: number;
  total: number;
  pending: number;
  rx: number;
  tx: number;
}

export interface CountRow {
  label: string;
  count: number;
}

export interface Stats {
  range: string;
  series: StatsPoint[];
  breakdown: { os: CountRow[]; versions: CountRow[]; countries: CountRow[]; states: CountRow[] };
  top: { id: string; name: string; kind: string; online: boolean; rx: number; tx: number }[];
  sessions: DeviceSession[];
  current: { online: number; total: number; blocked: number; routes: number; routes_up: number };
  geo: { loaded: boolean; attribution: string };
}

export interface EmailNotify {
  device_pending: boolean;
  device_added: boolean;
  new_country: boolean;
  device_blocked: boolean;
  key_expiring: boolean;
  access_requests: boolean;
  route_advertised: boolean;
  login_lockout: boolean;
  device_offline: boolean;
  device_online: boolean;
  owners_too: boolean;
  invitations: boolean;
}

export interface GotifySettings {
  enabled: boolean;
  url: string;
  token_set: boolean;
  priority: number;
  skip_verify: boolean;
}

export interface NtfySettings {
  enabled: boolean;
  url: string;
  topic: string;
  token_set: boolean;
  priority: number;
  skip_verify: boolean;
}

export interface LoginAlerts {
  mode: "off" | "new" | "all";
  to_user: boolean;
  to_admins: boolean;
  to_push: boolean;
}

export interface NotificationSettings {
  gotify: GotifySettings;
  ntfy: NtfySettings;
  events: EmailNotify;
  login: LoginAlerts;
}

export interface PushStatus {
  sent: number;
  failed: number;
  last_sent: number;
  last_error: string;
  last_error_at: number;
  queued: number;
}

export interface EmailSettings {
  enabled: boolean;
  host: string;
  port: number;
  security: "tls" | "starttls" | "none";
  username: string;
  password_set: boolean;
  from: string;
  from_name: string;
  skip_verify: boolean;
  recipients: string[];
  notify: EmailNotify;
}

export interface MailStatus {
  last_sent: number;
  last_error: string;
  last_error_at: number;
  sent: number;
  failed: number;
  queued: number;
}

export interface GeoStatus {
  database: { loaded: boolean; file?: string; type?: string; built_at?: number; loaded_at?: number; source?: string };
  attribution: string;
}

export interface DomainRoute {
  domain: string;
  via: string;
}

export type AccessStatus = "pending" | "approved" | "denied" | "revoked";

export interface AccessRequest {
  id: string;
  requester_id: string;
  requester: string;
  target: string;
  ports: string;
  reason: string;
  minutes: number;
  status: AccessStatus;
  decided_by: string;
  decided_at: number;
  granted_until: number;
  created_at: number;
}

export interface ClusterInstance {
  id: string;
  region: string;
  name: string;
  relay_url: string;
  udp_addr: string;
  self: boolean;
}

export interface SettingsResponse {
  settings: Settings;
  derived: { gateway_ipv4: string; gateway_ipv6: string; capacity: number; devices: number };
  server: {
    public_url: string;
    tls_mode: string;
    database: string;
    gateway_enabled: boolean;
    gateway_endpoint: string;
    relay_enabled: boolean;
    stun: string;
    version: string;
  };
}

export interface OIDCProvider {
  id: string;
  name: string;
  issuer: string;
  client_id: string;
  scopes: string[];
  groups_claim: string;
  allowed_domains: string[];
  auto_create_users: boolean;
  default_role: Role;
  sync_groups: boolean;
  enabled: boolean;
}

export interface Webhook {
  id: string;
  name: string;
  url: string;
  events: string[];
  enabled: boolean;
  last_status: number;
  last_error: string;
  last_delivery_at: number;
}

export interface APIToken {
  id: string;
  name: string;
  user_id: string;
  token_prefix: string;
  scopes: string[];
  expires_at: number;
  last_used_at: number;
  created_at: number;
}
