#!/bin/sh
set -e
# Only stop on removal, not on upgrade ($1 is "remove" for deb, 0 for rpm).
case "$1" in
    remove|0)
        if [ -d /run/systemd/system ]; then
            systemctl disable --now gorget.service || true
        fi
        ;;
esac
exit 0
