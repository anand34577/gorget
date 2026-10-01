# Email notifications

Gorget can email administrators when something needs a look, invite new people, and send
password-reset links. It uses your own mail provider over SMTP; nothing goes through a
third-party service.

## Set it up

**Settings > Email**:

1. Pick your provider (Gmail, Microsoft 365, Amazon SES, Brevo, Mailgun, Zoho) to fill in
   the server, or enter it yourself. Port 587 uses STARTTLS, port 465 uses TLS.
2. Enter the username and password. For Gmail and Zoho with two-factor sign-in, create an
   *app password*. The password is stored encrypted with the server's master key and is
   never shown again.
3. Enter the address mail comes from. Your provider must allow sending from it.
4. Save, then **Send test**. If it fails, the exact error from the mail server is shown.
5. Turn on **Send email**.

"None" as encryption is only for a mail relay on the same machine or a trusted private
network. Gorget refuses to send a password over an unencrypted connection to anywhere else.

## What you get notified about

Each one can be turned off separately:

| Event | Why it matters |
|---|---|
| A device is waiting for approval | New devices don't sit unapproved |
| A device joins | Every new device, approved or not |
| A device connects from a new country | Possible stolen device or account; needs the country database |
| A device is blocked by health rules | For example after its firewall was turned off |
| A device's sign-in expires soon | A week before, so it doesn't drop off unexpectedly |
| Someone requests temporary access | Approve or deny in the console |
| A device offers a network or exit node | Nothing is used until you approve it |
| An account is locked after wrong passwords | Someone may be guessing passwords |

Notifications go to every owner and admin, or to the addresses you list. With **Also tell
people about their own devices**, the device owner gets the device-specific ones too.

## Invitations and password resets

With email on:

- **Adding a person** emails them a link to choose their own password (valid for 3 days,
  works once). Nobody else ever sees their password.
- **People > … > Email a password link** sends a new link at any time.
- **Forgot your password?** appears on the sign-in page. The reset link works once and
  expires after 30 minutes. The page answers the same way whether or not an address has an
  account, so it can't be used to find out who has one. Two-factor sign-in still applies
  after a reset.

Mail that can't be delivered is retried three times and then dropped; **Settings > Email**
shows the last error.
