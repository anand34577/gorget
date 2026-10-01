# Gorget for Android

Kotlin + Jetpack Compose app with the Go client core embedded through gomobile. The `VpnService` hosts the tunnel.

## Build

Requirements: Go 1.26+, JDK 17, Android SDK (compileSdk 37), NDK 27+, and gomobile:

```sh
go install golang.org/x/mobile/cmd/gomobile@latest golang.org/x/mobile/cmd/gobind@latest
export ANDROID_HOME=~/Android/Sdk ANDROID_NDK_HOME=$ANDROID_HOME/ndk/28.1.13356709
make android            # builds android/app/libs/gorgetcore.aar, then a debug APK
```

The APKs are written to `android/app/build/outputs/apk/`. There is one per ABI plus a universal APK.

## Release signing (your own key)

Create the key once. Keep it offline and back it up: if you lose it, existing installs can't be updated.

```sh
keytool -genkeypair -v -keystore gorget-release.jks -alias gorget -keyalg EC -groupname secp384r1 -validity 10000
```

Create `android/keystore.properties`. This file is ignored by git, so it is never committed.

```properties
storeFile=/absolute/path/to/gorget-release.jks
storePassword=...
keyAlias=gorget
keyPassword=...
```

Then build the release:

```sh
make android-release
```

Publish the certificate fingerprint (`keytool -list -v -keystore gorget-release.jks`) so users can verify the APK.

## Features

- Sign in through the browser (Custom Tab), with a code to compare on screen, or with a setup key.
- Connects peer-to-peer using NAT traversal (STUN and simultaneous hole punching), and falls back to the relay over port 443 when direct paths fail.
- Exit-node picker, plus a setting to keep the local network reachable while using an exit node.
- Gorget DNS: device names resolve as `name.gorget.internal`, and queries go to DNS-over-HTTPS or DNS-over-TLS upstreams. The resolver runs inside the tunnel at `100.100.100.100`.
- Inbound firewall enforced on the phone itself. It applies the server's access rules and allows return traffic for connections the phone opened.
- Per-app split tunnelling, in three modes: all apps, only selected apps, or every app except the selected ones.
- Always-On VPN support, so Android's "block connections without VPN" setting acts as the kill switch. Also reconnects after a reboot.
- Quick Settings tile, home-screen widget, and local notifications for sign-in, approval and disconnection.
- Diagnostics screen. Logs stay on the phone and are only shared when you choose to.
- Android TV launcher entry.

## Privacy

There are no analytics or crash-reporting SDKs. Backups and device transfer are disabled, so keys never leave the device. The only network traffic goes to your own server, to your peers, and to the DNS upstreams your administrator configured.
