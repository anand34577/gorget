# Terraform

The provider (`terraform-provider-gorget/`) manages a Gorget network as code through the REST API.

```hcl
terraform {
  required_providers { gorget = { source = "anand34577/gorget" } }
}

provider "gorget" {
  url   = "https://vpn.example.com"   # or GORGET_URL
  token = var.gorget_token            # or GORGET_TOKEN: a read/write API token
}

resource "gorget_setup_key" "ci" {
  name            = "ci-runners"
  reusable        = true
  ephemeral       = true
  tags            = ["tag:ci"]
  expires_in_days = 30
}

resource "gorget_group" "eng" {
  name    = "eng"
  members = [gorget_user.alice.id]
}

resource "gorget_user" "alice" {
  email = "alice@example.com"
  name  = "Alice"
  role  = "user"
}

resource "gorget_policy" "main" {
  comment  = "managed by terraform"
  document = file("${path.module}/policy.hujson")
}

resource "gorget_settings" "posture" {
  section = "posture"
  json = jsonencode({
    enabled = true, mode = "enforce", min_client_version = "0.3.0",
    min_os_version = { windows = "10.0.19045" }, require_disk_encryption = true,
    require_firewall = false, allowed_networks = [], exempt_tags = ["tag:server"],
  })
}

data "gorget_devices" "servers" { tag = "tag:server" }
```

| Resource | Notes |
|---|---|
| `gorget_setup_key` | The secret `key` is available only after creation. Changing any argument makes a new key. |
| `gorget_policy` | Saving validates and runs the policy tests; failing tests fail the apply. Destroying leaves the policy in place. |
| `gorget_group`, `gorget_user` | `gorget_user` returns a `temporary_password` (sensitive) for local sign-in. |
| `gorget_webhook` | The signing `secret` is available only after creation. |
| `gorget_settings` | Replaces one whole section (`network`, `dns`, `devices`, `client`, `auth`, `gateway`, `posture`, `routing`). Drift is not detected for this one. |
| data `gorget_devices` | Filter by `kind` or `tag`. |

Build: `cd terraform-provider-gorget && go build -o terraform-provider-gorget`. For local use point Terraform at the binary with a `dev_overrides` block in `~/.terraformrc`.
