# Single sign-on and SCIM

## OpenID Connect (sign-in)

**Settings → Single sign-on → Add provider** works with any OIDC provider: Keycloak, Authentik, Zitadel, Google, Microsoft Entra ID, Okta, GitHub (via an OIDC bridge).

| Field | |
|---|---|
| Issuer | The provider's issuer URL. The console checks that it answers. |
| Client ID / secret | From the provider. Redirect URL: shown in the console (`https://vpn.example.com/api/v1/auth/oidc/callback`). |
| Allowed email domains | Optional restriction. |
| Create people automatically | New people get the default role (`user`, never owner). |
| Sync groups | Group claims become Gorget groups (managed by the provider). |

People are matched by email address (which the provider must have verified). An existing local account is linked on first sign-in. Passkeys and authenticator codes still apply to local accounts; SSO users use the provider's own two-factor.

## SCIM provisioning (users and groups)

OIDC creates people when they first sign in. SCIM also **removes** them when they leave, and keeps groups in step, without anyone signing in.

1. **Settings → Provisioning → Turn on and create a token**. Copy the token (shown once) and the base URL `https://vpn.example.com/scim/v2`.
2. In your identity provider add a SCIM 2.0 app with that URL and token (HTTP header authentication, bearer).
3. Assign people and groups to the app.

What it does:

- creates people (no password: they sign in through SSO, matched by email) and updates their name;
- **deactivating** a person ends their sessions, signs their devices out and blocks sign-in; deleting removes them and their devices;
- groups pushed by SCIM are managed by the provider and read-only in the console; local groups are never touched;
- roles are never changed by SCIM. Owners cannot be deleted through it.

Supported: `Users` and `Groups` with `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, filters `userName eq` / `displayName eq`, and the discovery endpoints (`ServiceProviderConfig`, `ResourceTypes`, `Schemas`).
