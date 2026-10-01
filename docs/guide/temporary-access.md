# Temporary access

Permanent rules for "the contractor who needs the build server this week" pile up and are never cleaned. Temporary access gives time-limited access that ends by itself.

## Asking

**Temporary access → Request access**: choose a device (or all devices with a tag), the ports, how long (30 minutes to 7 days) and a reason. Administrators see it in the queue and on the overview page.

## Deciding

Administrators **Approve** (the window starts at approval, not at the request) or **Deny**. They can end approved access early with **End now**. Everything is recorded in the activity log, and `access.requested`, `access.approved` and `access.denied` are webhook events, so you can route requests to chat.

## Granting directly

**Grant access** gives a person, for example a guest, access to one device right away. Create the person under **People & groups** first; they only need an account, no other permissions.

## What it does

An approved request becomes an ordinary, expiring rule: *the person's devices may reach `device:name` on those ports*. It is combined with your permanent rules (it can only add access, never remove), appears in the device's **Access** tab while active, and is dropped by the server the moment it expires: connections are cut within seconds.

Use it with the [REST API](../ops/api.md) (`/access-requests`, `/access-grants`) to build your own approval flow.
