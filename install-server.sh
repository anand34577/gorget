#!/bin/sh
# Gorget server installer for Linux (a VPS or a machine at home).
#
#   curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh | sh
#
# It downloads gorget-server from the latest GitHub release (checked against the release's
# SHA256SUMS), installs it to /usr/local/bin, asks five setup questions
# (gorget-server init), checks the machine, and installs and starts the service.
#
# Options (environment variables, or after "sh -s --"):
#   GORGET_VERSION / --version vX.Y.Z   a specific release (default: the latest)
#                    --no-init          only install the binary
#
# Prefer Docker? See deploy/docker in the repository.

set -eu

REPO="anand34577/gorget"
VERSION="${GORGET_VERSION:-}"
INIT=1

while [ $# -gt 0 ]; do
	case "$1" in
	--version) VERSION="${2:?--version needs a value}"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--no-init) INIT=0; shift ;;
	*) echo "Unknown option: $1" >&2; exit 2 ;;
	esac
done

if [ -t 1 ]; then BOLD="$(printf '\033[1m')"; RED="$(printf '\033[31m')"; RESET="$(printf '\033[0m')"; else BOLD=""; RED=""; RESET=""; fi
step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*" >&2; }
die() { printf '%sError:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

[ "$(uname -s)" = Linux ] || die "this installer is for Linux. On Windows or macOS, download gorget-server from https://github.com/$REPO/releases"

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
	if have sudo; then SUDO="sudo"; elif have doas; then SUDO="doas"; else die "run this as root, or install sudo"; fi
fi
as_root() { if [ -n "$SUDO" ]; then $SUDO "$@"; else "$@"; fi; }

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
armv7l | armv6l) ARCH=arm ;;
*) die "unsupported processor $(uname -m)" ;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

fetch() {
	if have curl; then curl -fsSL --retry 3 --proto '=https' --tlsv1.2 -o "$2" "$1"
	elif have wget; then wget -q --https-only -O "$2" "$1"
	else die "curl or wget is needed"; fi
}
sha256() {
	if have sha256sum; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

if [ -n "$VERSION" ]; then
	case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
	BASE="https://github.com/$REPO/releases/download/$VERSION"
else
	BASE="https://github.com/$REPO/releases/latest/download"
fi

step "Checking the latest Gorget release"
fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" || die "couldn't download the release list from $BASE"
NAME="$(awk '{print $2}' "$TMP/SHA256SUMS" | sed 's/^\*//' | grep -E "^gorget-server_v?[0-9][^_]*_linux_${ARCH}\$" | head -n 1 || true)"
[ -n "$NAME" ] || die "this release has no gorget-server for linux/$ARCH"

step "Downloading $NAME"
fetch "$BASE/$NAME" "$TMP/gorget-server" || die "download failed"
WANT="$(awk -v n="$NAME" '{f=$2; sub(/^\*/, "", f); if (f == n) print $1}' "$TMP/SHA256SUMS")"
GOT="$(sha256 "$TMP/gorget-server")"
[ "$WANT" = "$GOT" ] || die "checksum mismatch for $NAME. Not installing."

step "Installing /usr/local/bin/gorget-server"
as_root install -m 0755 "$TMP/gorget-server" /usr/local/bin/gorget-server

if ! have nft; then
	printf '%s\n' "Note: install nftables if standard WireGuard apps or routers will connect (the gateway needs it)." >&2
fi

if [ "$INIT" -eq 0 ]; then
	echo "Installed. Next: sudo gorget-server init && sudo gorget-server install"
	exit 0
fi

if [ ! -r /dev/tty ]; then
	echo "Installed. There's no terminal to ask the setup questions; run: sudo gorget-server init && sudo gorget-server install"
	exit 0
fi

CONFIG=/etc/gorget/config.yaml
if [ -f "$CONFIG" ]; then
	step "Found $CONFIG; checking it"
	as_root /usr/local/bin/gorget-server check -config "$CONFIG" </dev/tty || true
else
	step "Setting up"
	as_root /usr/local/bin/gorget-server init -config "$CONFIG" </dev/tty
fi

step "Installing and starting the service"
if as_root /usr/local/bin/gorget-server install -config "$CONFIG"; then
	:
else
	# Already installed (an upgrade): restart it on the new binary.
	as_root /usr/local/bin/gorget-server restart || true
fi

echo
echo "${BOLD}Done.${RESET} The setup link is in the service log:"
echo "  sudo journalctl -u gorget-server -n 40 | grep -A3 setup"
echo "Then, on each Linux or Mac device:  curl -fsSL https://YOUR-SERVER/install.sh | sh"
