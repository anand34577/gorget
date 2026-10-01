"""Generates internal/api/openapi.json (served at /api/v1/openapi.json) from the compact
description below. Run:  python docs/gen_openapi.py
Keep it in sync with internal/api/api.go when routes change."""
import json, os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

S = "string"
def obj(props, required=()):
    return {"type": "object", "properties": props, "required": list(required)} if required else {"type": "object", "properties": props}
def arr(item): return {"type": "array", "items": item}
def ref(n): return {"$ref": "#/components/schemas/" + n}
STR, INT, BOOL = {"type": "string"}, {"type": "integer"}, {"type": "boolean"}
STRS = arr(STR)

schemas = {
    "Error": obj({"code": STR, "message": STR, "details": {}}),
    "Ok": obj({"ok": BOOL}),
    "Device": obj({
        "id": STR, "name": STR, "kind": {"type": "string", "enum": ["native", "wireguard", "gateway"]},
        "wg_public_key": STR, "ipv4": STR, "ipv6": STR, "static_ip": BOOL, "tags": STRS,
        "state": {"type": "string", "enum": ["active", "pending", "disabled"]}, "ephemeral": BOOL,
        "key_expiry_disabled": BOOL, "key_expires_at": INT, "hostname": STR, "os": STR, "os_version": STR,
        "client_version": STR, "arch": STR, "disk_encrypted": {"type": "integer", "description": "0 unknown, 1 yes, 2 no"},
        "firewall_on": INT, "public_ip": STR, "endpoints": STRS, "exit_advertised": BOOL, "exit_approved": BOOL,
        "online": BOOL, "user_id": STR, "user_email": STR, "fqdn": STR, "key_expired": BOOL, "peer_count": INT,
        "posture": {"type": "array", "items": STR, "description": "Security rules the device breaks (empty = compliant)"},
        "routes": arr(ref("Route")), "last_seen_at": INT, "rx_bytes": INT, "tx_bytes": INT, "created_at": INT,
        "remote_ip": {"type": "string", "description": "Public address the device connects from"},
        "country": {"type": "string", "description": "ISO country code of remote_ip (needs the country database)"}}),
    "DeviceSession": obj({"id": STR, "device_id": STR, "started_at": INT, "ended_at": {"type": "integer", "description": "0 while still connected"},
        "public_ip": STR, "country": STR, "client_version": STR}),
    "Route": obj({"id": STR, "device_id": STR, "cidr": STR, "advertised": BOOL, "approved": BOOL, "enabled": BOOL, "priority": INT}),
    "User": obj({"id": STR, "email": STR, "name": STR, "role": {"type": "string", "enum": ["owner", "admin", "network_admin", "auditor", "user"]},
                 "provider": STR, "totp_enabled": BOOL, "disabled": BOOL, "must_change_password": BOOL, "created_at": INT,
                 "last_login_at": INT, "groups": STRS, "device_count": INT, "passkeys": INT}),
    "Group": obj({"id": STR, "name": STR, "description": STR, "source": STR, "members": STRS}),
    "SetupKey": obj({"id": STR, "name": STR, "key_prefix": STR, "reusable": BOOL, "ephemeral": BOOL, "auto_approve": BOOL, "tags": STRS,
                     "max_uses": INT, "uses": INT, "expires_at": INT, "revoked": BOOL}),
    "AccessRequest": obj({"id": STR, "requester_id": STR, "requester": STR, "target": {"type": "string", "description": "device:<name> or tag:<name>"},
                          "ports": {"type": "string", "description": "22,443 | 8000-9000 | *"}, "reason": STR, "minutes": INT,
                          "status": {"type": "string", "enum": ["pending", "approved", "denied", "revoked"]}, "decided_by": STR,
                          "decided_at": INT, "granted_until": INT, "created_at": INT}),
    "Policy": obj({"version": INT, "document": {"type": "string", "description": "HuJSON policy document"}, "comment": STR, "created_by": STR, "created_at": INT}),
    "Posture": obj({"enabled": BOOL, "mode": {"type": "string", "enum": ["enforce", "report"]}, "min_client_version": STR,
                    "min_os_version": {"type": "object", "additionalProperties": STR, "description": "windows, darwin, linux, android"},
                    "require_disk_encryption": BOOL, "require_firewall": BOOL, "allowed_networks": STRS, "exempt_tags": STRS}),
    "Webhook": obj({"id": STR, "name": STR, "url": STR, "events": STRS, "enabled": BOOL, "last_status": INT, "last_error": STR}),
    "AuditEntry": obj({"seq": INT, "ts": INT, "actor": STR, "action": STR, "target_type": STR, "target_id": STR, "target_name": STR, "details": STR, "ip": STR, "hash": STR}),
}

def body(schema, required=True):
    return {"required": required, "content": {"application/json": {"schema": schema}}}

def resp(schema=None, desc="OK"):
    r = {"description": desc}
    if schema is not None:
        r["content"] = {"application/json": {"schema": schema}}
    return r

paths = {}
def op(method, path, tag, summary, req=None, res=None, public=False, params=(), code="200", roles=None):
    o = {"tags": [tag], "summary": summary, "responses": {code: resp(res), "default": resp(ref("Error"), "Error")}}
    if roles:
        o["description"] = "Needs: " + roles
    if req is not None:
        o["requestBody"] = body(req)
    ps = []
    for p in params:
        if p.startswith("{"):
            continue
        ps.append({"name": p.split(":")[0], "in": "query", "schema": STR, "required": False, "description": p.split(":", 1)[1] if ":" in p else ""})
    for seg in path.split("/"):
        if seg.startswith("{"):
            ps.append({"name": seg.strip("{}"), "in": "path", "required": True, "schema": STR})
    if ps:
        o["parameters"] = ps
    if public:
        o["security"] = []
    paths.setdefault(path, {})[method] = o

ADMIN = "network admin"
# --- setup & auth
op("get", "/setup/status", "Setup", "Is first-run setup finished?", public=True, res=obj({"completed": BOOL, "version": STR}))
op("post", "/setup", "Setup", "Finish first-run setup (needs the one-time setup token)", req=obj({"network_name": STR, "owner_email": STR, "owner_name": STR, "password": STR, "ipv4": STR, "domain": STR, "policy": STR, "approval_required": BOOL, "setup_token": STR}, ["owner_email", "password", "setup_token"]), public=True, res=ref("Ok"))
op("post", "/auth/login", "Auth", "Sign in with email and password", req=obj({"email": STR, "password": STR}, ["email", "password"]), public=True, res=obj({"csrf_token": STR, "mfa_required": BOOL, "mfa_methods": STRS}))
op("post", "/auth/logout", "Auth", "Sign out", res=ref("Ok"))
op("get", "/auth/providers", "Auth", "List single sign-on providers", public=True, res=arr(obj({"id": STR, "name": STR})))
op("get", "/auth/oidc/{id}/start", "Auth", "Start an OIDC sign-in (redirects)", public=True, code="302")
op("post", "/auth/mfa/totp", "Auth", "Complete sign-in with an authenticator or recovery code", req=obj({"code": STR}))
op("get", "/me", "Account", "Current user, permissions and features", res=ref("User"))
op("patch", "/me", "Account", "Update your profile", req=obj({"name": STR}))
op("post", "/me/password", "Account", "Change your password", req=obj({"current": STR, "new": STR}))
op("get", "/me/sessions", "Account", "Your active sign-ins")
op("get", "/me/tokens", "Account", "Your API tokens")
op("post", "/me/tokens", "Account", "Create an API token (shown once)", req=obj({"name": STR, "scopes": STRS, "expires_in_days": INT}), code="201")
op("delete", "/me/tokens/{id}", "Account", "Delete an API token", res=ref("Ok"))
op("get", "/overview", "Dashboard", "Counts for the dashboard")
op("get", "/network-map", "Dashboard", "Devices and the connections the policy allows")
op("get", "/events", "Dashboard", "Server-sent events stream of changes")
# --- devices
op("get", "/devices", "Devices", "List devices", params=("q:search text", "kind:native|wireguard|gateway"), res=arr(ref("Device")))
op("get", "/devices/{id}", "Devices", "One device", res=ref("Device"))
op("patch", "/devices/{id}", "Devices", "Rename, tag, approve, disable, set key expiry or exit-node approval", req=obj({"name": STR, "tags": STRS, "ipv4": STR, "key_expiry_disabled": BOOL, "exit_approved": BOOL, "state": STR, "expires_at": INT, "tunnel_mode": STR, "custom_allowed_ips": STRS}), res=ref("Device"))
op("delete", "/devices/{id}", "Devices", "Delete a device", res=ref("Ok"))
op("post", "/devices/{id}/approve", "Devices", "Approve a pending device", res=ref("Device"), roles=ADMIN)
op("post", "/devices/{id}/expire-key", "Devices", "Force the device to sign in again", res=ref("Ok"))
op("get", "/devices/{id}/access", "Devices", "What this device may reach, and who may reach it")
op("get", "/devices/{id}/wireguard-config", "Devices", "Config file for a standard WireGuard device")
op("post", "/devices/{id}/rotate-psk", "Devices", "Rotate the pre-shared key of a WireGuard device")
op("post", "/wireguard-configs", "Devices", "Create a standard WireGuard device (config and QR code)", req=obj({"name": STR, "user_id": STR, "public_key": STR, "tunnel_mode": STR, "custom_allowed_ips": STRS, "expires_at": INT, "preshared_key": BOOL, "dns": BOOL, "tags": STRS, "ipv4": STR}), code="201")
op("get", "/device-logins/{code}", "Devices", "Look up a pending browser sign-in of the desktop or Android app")
op("post", "/device-logins/{code}/approve", "Devices", "Approve a device sign-in", res=ref("Ok"))
op("post", "/device-logins/{code}/deny", "Devices", "Deny a device sign-in", res=ref("Ok"))
# --- people
op("get", "/users", "People", "List users", res=arr(ref("User")))
op("post", "/users", "People", "Create a user", req=obj({"email": STR, "name": STR, "role": STR, "password": STR, "groups": STRS}, ["email"]), code="201", roles="manage users")
op("patch", "/users/{id}", "People", "Change name, role or disable", req=obj({"name": STR, "role": STR, "disabled": BOOL}), res=ref("User"), roles="manage users")
op("delete", "/users/{id}", "People", "Delete a user and their devices", res=ref("Ok"), roles="manage users")
op("post", "/users/{id}/reset-mfa", "People", "Remove a user's second factors", res=ref("Ok"), roles="manage users")
op("post", "/users/{id}/reset-password", "People", "Set a temporary password", res=obj({"temporary_password": STR}), roles="manage users")
op("post", "/users/{id}/revoke-sessions", "People", "Sign a user out everywhere", res=ref("Ok"), roles="manage users")
op("get", "/groups", "People", "List groups", res=arr(ref("Group")))
op("post", "/groups", "People", "Create a group", req=obj({"name": STR, "description": STR, "members": STRS}, ["name"]), code="201", res=ref("Group"))
op("patch", "/groups/{id}", "People", "Edit a group", req=obj({"name": STR, "description": STR, "members": STRS}), res=ref("Group"))
op("delete", "/groups/{id}", "People", "Delete a group", res=ref("Ok"))
# --- access
op("get", "/policy", "Access rules", "Current policy", res=ref("Policy"))
op("put", "/policy", "Access rules", "Save a new policy version (tests must pass)", req=obj({"document": STR, "comment": STR, "expect_version": INT}, ["document"]), res=ref("Policy"), roles=ADMIN)
op("post", "/policy/validate", "Access rules", "Check a policy document and run its tests", req=obj({"document": STR}, ["document"]))
op("post", "/policy/check", "Access rules", "Simulate: may src reach dst?", req=obj({"src": STR, "dst": STR, "port": INT, "proto": STR, "document": STR}, ["src", "dst"]))
op("get", "/policy/versions", "Access rules", "Version history", res=arr(ref("Policy")))
op("get", "/policy/versions/{v}", "Access rules", "One version", res=ref("Policy"))
op("post", "/policy/versions/{v}/restore", "Access rules", "Roll back to a version", res=ref("Policy"), roles=ADMIN)
op("get", "/access-requests", "Temporary access", "Requests (all for administrators, your own otherwise)", res=arr(ref("AccessRequest")))
op("post", "/access-requests", "Temporary access", "Ask for temporary access to a device or tag", req=obj({"target": STR, "ports": STR, "reason": STR, "minutes": INT}, ["target", "minutes"]), code="201", res=ref("AccessRequest"))
op("post", "/access-requests/{id}/approve", "Temporary access", "Approve (optionally with a different duration)", req=obj({"minutes": INT}), res=ref("AccessRequest"), roles=ADMIN)
op("post", "/access-requests/{id}/deny", "Temporary access", "Deny", res=ref("AccessRequest"), roles=ADMIN)
op("post", "/access-requests/{id}/revoke", "Temporary access", "End approved access early", res=ref("AccessRequest"), roles=ADMIN)
op("post", "/access-grants", "Temporary access", "Give a person (for example a guest) temporary access directly", req=obj({"user_id": STR, "target": STR, "ports": STR, "reason": STR, "minutes": INT}, ["user_id", "target", "minutes"]), code="201", res=ref("AccessRequest"), roles=ADMIN)
# --- routes & keys
op("get", "/routes", "Routes", "Subnet routes and exit nodes")
op("post", "/routes", "Routes", "Add a network behind a standard WireGuard device (a site router)", req=obj({"device_id": STR, "cidr": STR}, ["device_id", "cidr"]), res=ref("Route"), code="201", roles=ADMIN)
op("get", "/stats", "Insights", "Usage over time, breakdowns and recent connections", params=["range:24h (default), 7d, 30d or 90d"])
op("get", "/devices/{id}/sessions", "Devices", "When the device was connected and from which public address", res=arr(ref("DeviceSession")))
op("get", "/geoip", "Settings", "Country database status")
op("post", "/geoip/update", "Settings", "Download the latest free country database", roles=ADMIN)
op("get", "/settings/email", "Settings", "Email (SMTP) settings without the password, and delivery status")
op("put", "/settings/email", "Settings", "Save email settings; send password to replace it, omit it to keep the stored one", req=obj({}), roles="admin")
op("post", "/settings/email/test", "Settings", "Send a test message", req=obj({"to": STR}), roles="admin")
op("post", "/users/{id}/invite", "People", "Email a one-time link to choose a password", roles="admin")
op("post", "/auth/forgot-password", "Auth", "Email a password reset link (same answer whether or not the account exists)", req=obj({"email": STR}, ["email"]), public=True)
op("post", "/auth/reset-password", "Auth", "Set a new password with a one-time link", req=obj({"token": STR, "password": STR}, ["token", "password"]), public=True)
op("patch", "/routes/{id}", "Routes", "Approve, enable or prioritise a route", req=obj({"approved": BOOL, "enabled": BOOL, "priority": INT}), res=ref("Route"), roles=ADMIN)
op("delete", "/routes/{id}", "Routes", "Remove a route", res=ref("Ok"), roles=ADMIN)
op("get", "/setup-keys", "Setup keys", "List setup keys", res=arr(ref("SetupKey")))
op("post", "/setup-keys", "Setup keys", "Create a setup key (shown once)", req=obj({"name": STR, "reusable": BOOL, "ephemeral": BOOL, "auto_approve": BOOL, "tags": STRS, "max_uses": INT, "expires_in_days": INT}), code="201")
op("post", "/setup-keys/{id}/revoke", "Setup keys", "Revoke a setup key", res=ref("Ok"))
op("delete", "/setup-keys/{id}", "Setup keys", "Delete a setup key", res=ref("Ok"))
# --- settings
op("get", "/settings", "Settings", "All settings sections and derived values")
op("put", "/settings/{section}", "Settings", "Replace one section: network, dns, devices, client, auth, gateway, posture or routing", req=obj({}), roles=ADMIN)
op("get", "/settings", "Settings", "All settings sections and derived values")
op("post", "/settings/network/readdress", "Settings", "Preview or apply a new address range", req=obj({"ipv4": STR, "ipv6": STR, "dry_run": BOOL}), roles="owner")
op("get", "/sso-providers", "Single sign-on", "OIDC providers")
op("post", "/sso-providers", "Single sign-on", "Add a provider", req=obj({"name": STR, "issuer": STR, "client_id": STR, "client_secret": STR, "scopes": STRS, "groups_claim": STR, "allowed_domains": STRS, "auto_create_users": BOOL, "default_role": STR, "sync_groups": BOOL, "enabled": BOOL}), code="201")
op("patch", "/sso-providers/{id}", "Single sign-on", "Edit a provider", req=obj({"name": STR}))
op("delete", "/sso-providers/{id}", "Single sign-on", "Remove a provider", res=ref("Ok"))
op("post", "/sso-providers/test", "Single sign-on", "Check that an issuer answers", req=obj({"issuer": STR}))
op("get", "/scim", "Single sign-on", "SCIM provisioning status and endpoint")
op("post", "/scim/token", "Single sign-on", "Create a SCIM token (shown once) and turn provisioning on", res=obj({"token": STR, "base_url": STR}))
op("delete", "/scim", "Single sign-on", "Turn SCIM provisioning off", res=ref("Ok"))
op("get", "/webhooks", "Webhooks", "List webhooks and the available events")
op("post", "/webhooks", "Webhooks", "Add a webhook (the signing secret is shown once)", req=obj({"name": STR, "url": STR, "events": STRS, "enabled": BOOL}), code="201")
op("patch", "/webhooks/{id}", "Webhooks", "Edit a webhook", req=obj({"name": STR, "url": STR, "events": STRS, "enabled": BOOL}))
op("delete", "/webhooks/{id}", "Webhooks", "Remove a webhook", res=ref("Ok"))
op("post", "/webhooks/{id}/test", "Webhooks", "Send a test event")
op("get", "/audit", "Operations", "Activity log", params=("limit:", "before:", "q:"), res=arr(ref("AuditEntry")))
op("get", "/audit/verify", "Operations", "Check the log's hash chain", res=obj({"entries": INT, "ok": BOOL, "first_broken_seq": INT}))
op("get", "/system/status", "Operations", "Database, gateway, TLS, relay and cluster status")
op("get", "/api-tokens", "Operations", "All API tokens (administrators)")

spec = {
    "openapi": "3.0.3",
    "info": {"title": "Gorget REST API", "version": "1.0.0",
             "description": "The API behind the web console. Sign in with a session cookie (send X-CSRF-Token on writes) or use an API token: `Authorization: Bearer gat_...`. Tokens are read-only or read/write. IdP provisioning uses the separate SCIM 2.0 endpoint under /scim/v2."},
    "servers": [{"url": "/api/v1"}],
    "security": [{"bearer": []}, {"session": []}],
    "components": {
        "securitySchemes": {
            "bearer": {"type": "http", "scheme": "bearer", "description": "API token created under Account > API tokens"},
            "session": {"type": "apiKey", "in": "cookie", "name": "gorget_session"},
        },
        "schemas": schemas,
    },
    "paths": paths,
}

out = os.path.join(ROOT, "internal", "api", "openapi.json")
with open(out, "w", encoding="utf-8", newline="\n") as f:
    json.dump(spec, f, indent=1, ensure_ascii=False)
    f.write("\n")
print("wrote", out, len(paths), "paths")
