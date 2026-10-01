#!/bin/sh
# Removes Gorget from this Mac. Run with sudo.
set -u
[ "$(id -u)" -eq 0 ] || { echo "run with sudo"; exit 1; }
/usr/local/bin/gorget uninstall-service >/dev/null 2>&1 || true
for u in $(ls /Users 2>/dev/null); do
    id "$u" >/dev/null 2>&1 && launchctl bootout "gui/$(id -u "$u")/net.gorget.desktop" >/dev/null 2>&1 || true
done
rm -f /Library/LaunchAgents/net.gorget.desktop.plist /usr/local/bin/gorget
rm -rf /Applications/Gorget.app
pkgutil --forget net.gorget.pkg >/dev/null 2>&1 || true
echo "Gorget removed. Your device keys and settings remain in '/Library/Application Support/Gorget';"
echo "delete that folder too if you want to forget this device completely."
