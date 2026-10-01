#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload || true
    systemctl enable gorget.service || true
    systemctl restart gorget.service || true
fi
# Let the person who ran sudo control the VPN from the tray app and CLI without root.
if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != root ] && [ ! -s /var/lib/gorget/operator ]; then
    /usr/bin/gorget operator set "$SUDO_USER" || true
fi
exit 0
