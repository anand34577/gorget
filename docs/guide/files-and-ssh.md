# Files and SSH

## Send files between your devices

Files travel straight between two of **your own** devices through the encrypted tunnel; the server never sees them.

```sh
gorget file cp report.pdf photos.zip laptop     # send to the device named laptop
gorget file list                                # what arrived on this device
gorget file get -dir ~/Downloads                # save everything (and clear the inbox)
```

In the tray app: open a device and choose **Send a file…**; received files appear under **Files**.

Rules:

- the receiver listens only on its Gorget address (never on the physical network);
- it accepts files only from devices signed in as the same user;
- the sender must be allowed to reach the receiver by your [access rules](access-rules.md) (the default "own devices" rule is enough);
- files up to 32 GiB; names are sanitised and never overwrite existing files.

## SSH by device name

```sh
gorget ssh deploy@build-server uptime
```

This runs your system `ssh` against the device's Gorget address with host keys tied to the *device name*, so renumbering a device does not break known hosts. Plain `ssh user@build-server.gorget.internal` works as well; Gorget does not replace sshd or its authentication.
