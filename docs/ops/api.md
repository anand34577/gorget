# REST API

The console is a client of the same API you can use.

- **Reference:** the console's **API reference** page, or the OpenAPI 3 document at `/api/v1/openapi.json`.
- **Authentication:** create a token under **Account → API tokens** (read-only or read/write, optional expiry) and send `Authorization: Bearer gat_…`. Tokens act as the person who created them, with that person's role.
- **Writes** with a browser session need the `X-CSRF-Token` header; bearer tokens do not.
- Errors are JSON `{ "code": "…", "message": "…" }` with the usual HTTP status. Requests are rate limited.

```sh
curl -H "Authorization: Bearer $TOKEN" https://vpn.example.com/api/v1/devices
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'content-type: application/json' \
     -d '{"name":"ci","reusable":true,"tags":["tag:ci"],"expires_in_days":30}' \
     https://vpn.example.com/api/v1/setup-keys
```

## Related endpoints

- `/scim/v2`: [identity provider provisioning](sso.md), with its own token.
- Webhooks: signed `POST`s for events (`X-Gorget-Signature: t=<unix>,v1=<hmac-sha256(secret, t + "." + body)>`).
- [Terraform provider](terraform.md) wraps the common resources.
