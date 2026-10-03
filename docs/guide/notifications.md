# Notifications and sign-in alerts

Gorget can tell you when something needs a look: by [email](email.md), and as a push message
on your phone through **Gotify** or **ntfy**. It can also warn you (or the person concerned)
when someone signs in to the console. Everything is under **Settings > Notifications** and
**Settings > Email**. Nothing is sent through a third party: mail goes through your own SMTP
provider and push messages go to the Gotify or ntfy server you choose.

## Sign-in alerts

Choose when to be told:

| Mode | What it sends |
|---|---|
| **New browsers and countries** (default) | Only when an account signs in from a browser, or a country, it hasn't used before. Quiet, and catches the sign-ins that matter. |
| **Every sign-in** | Every successful sign-in to the console. |
| **Off** | Nothing. |

Each message says **who** signed in, **when**, **how** (password, password and authenticator
code or passkey, or single sign-on), **from where** (public address and country) and **with what**
(for example "Chrome 131 on Windows"), and why you are being told ("a browser this account hasn't
used before"). An unrecognised sign-in is sent with a higher priority.

Choose where it goes:

- **Email the person whose account was used**, so they can spot a sign-in that wasn't them.
- **Email the administrators too** (the notification recipients, or every owner and admin).
- **Send to Gotify and ntfy**, so you see it on your phone.

The first sign-in of an account is only reported in "every sign-in" mode: there is nothing to
compare it with yet. The country comes from the local country database (**Settings > Device
health**); no address is looked up online. Without the database the alert still names the
browser and address.

## Gotify

[Gotify](https://gotify.net) is a small self-hosted push server with an Android app.

1. In Gotify, create an **application** (for example "Gorget") and copy its token.
2. In Gorget open **Settings > Notifications > Gotify**, enter the server address
   (`https://gotify.example.com`) and the token, and switch it on.
3. **Save**, then **Send test**. If it fails, the exact error is shown ("the server refused the
   token", or a connection error).

The token is stored encrypted with the server's master key and never shown again. Important
events (an unrecognised sign-in, a blocked device, a locked account) are raised to at least
priority 8 so they stand out. A self-signed certificate can be accepted for your own server.

## ntfy

[ntfy](https://ntfy.sh) works with the free hosted service or your own server.

1. In **Settings > Notifications > ntfy** enter the server (`https://ntfy.sh` by default) and a
   **topic**. Press **Generate** for a hard-to-guess name: on a public server anyone who knows the
   topic can read it.
2. Subscribe to the same topic in the ntfy app on your phone.
3. If the topic needs a login, add an access token.
4. **Save**, then **Send test**.

## What gets pushed

The list under **What to push** controls Gotify and ntfy; the list under **Settings > Email**
controls email. They are independent, so you can send everything to your phone and only the
important events by email.

| Event | Why it matters |
|---|---|
| A device is waiting for approval | New devices don't sit unapproved |
| A device joins | Every new device, approved or not |
| A device connects from a new country | Possible stolen device; needs the country database |
| A device is blocked by health rules | For example after its firewall was turned off |
| A device's sign-in expires soon | A week before |
| Someone requests temporary access | Approve or deny in the console |
| A device offers a network or exit node | Nothing is used until you approve it |
| An account is locked after wrong passwords | Someone may be guessing passwords |
| A device has been offline for over a minute | Handy for servers and routers; a short grace period avoids a message every time a phone changes network |
| A device is back online | Sent only after an offline message |

Messages that can't be delivered are retried three times; the page shows how many were sent or
failed and the last error.

## Webhooks

For chat apps and automation there are also signed [webhooks](../ops/api.md) (**Settings >
Webhooks**). The sign-in event is `auth.login`.
