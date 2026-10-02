#!/bin/sh
# Gorget client installer for Linux and macOS.
#
#   curl -fsSL https://vpn.example.com/install.sh | sh          (from your server: it knows its own address)
#   curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install.sh | sh
#
# Options (as environment variables, or after "sh -s --"):
#   GORGET_SERVER     / --server URL      connect to this server after installing
#   GORGET_SETUP_KEY  / --setup-key KEY   sign in with a setup key instead of the browser
#   GORGET_VERSION    / --version vX.Y.Z  install a specific release (default: the latest)
#   GORGET_NO_TRAY=1                      on a Linux desktop, skip the tray app
#                       --uninstall       remove Gorget
#
# Prefer the environment variable for the setup key: command-line arguments are visible
# to other users of the machine while the script runs. Example:
#   curl -fsSL https://vpn.example.com/install.sh | GORGET_SETUP_KEY=gsk_... sh
#
# The script only downloads from the project's GitHub releases and checks every file
# against the release's SHA256SUMS before installing it.

set -eu

REPO="anand34577/gorget"
# A Gorget server serving this file fills in its own address here.
DEFAULT_SERVER=""

SERVER="${GORGET_SERVER:-$DEFAULT_SERVER}"
SETUP_KEY="${GORGET_SETUP_KEY:-}"
VERSION="${GORGET_VERSION:-}"
UNINSTALL=0

while [ $# -gt 0 ]; do
	case "$1" in
	--server) SERVER="${2:?--server needs a value}"; shift 2 ;;
	--server=*) SERVER="${1#*=}"; shift ;;
	--setup-key) SETUP_KEY="${2:?--setup-key needs a value}"; shift 2 ;;
	--setup-key=*) SETUP_KEY="${1#*=}"; shift ;;
	--version) VERSION="${2:?--version needs a value}"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--uninstall) UNINSTALL=1; shift ;;
	-h | --help) sed -n '2,19p' "$0" 2>/dev/null || true; exit 0 ;;
	*) echo "Unknown option: $1 (see --help)" >&2; exit 2 ;;
	esac
done

# ---------- helpers ----------

if [ -t 1 ]; then BOLD="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"; RED="$(printf '\033[31m')"; RESET="$(printf '\033[0m')"; else BOLD=""; DIM=""; RED=""; RESET=""; fi
say() { printf '%s\n' "$*"; }
# Progress goes to stderr, so helpers can return values on stdout.
step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*" >&2; }
die() { printf '%sError:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
	if have sudo; then SUDO="sudo"
	elif have doas; then SUDO="doas"
	else die "run this as root, or install sudo"; fi
fi
as_root() { if [ -n "$SUDO" ]; then $SUDO "$@"; else "$@"; fi; }

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t gorget)"
trap 'rm -rf "$TMP"' EXIT INT TERM

fetch() { # fetch URL FILE
	if have curl; then curl -fsSL --retry 3 --proto '=https' --tlsv1.2 -o "$2" "$1"
	elif have wget; then wget -q --https-only -O "$2" "$1"
	else die "curl or wget is needed"; fi
}

sha256() {
	if have sha256sum; then sha256sum "$1" | cut -d' ' -f1
	elif have shasum; then shasum -a 256 "$1" | cut -d' ' -f1
	else die "sha256sum or shasum is needed to verify downloads"; fi
}

# ---------- what to install ----------

OS="$(uname -s)"
case "$OS" in
Linux) OS=linux ;;
Darwin) OS=darwin ;;
*) die "this installer supports Linux and macOS. On Windows, use the installer (.msi) from https://github.com/$REPO/releases" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64; RPMARCH=x86_64 ;;
aarch64 | arm64) ARCH=arm64; RPMARCH=aarch64 ;;
*) die "unsupported processor $(uname -m) (amd64 and arm64 are available)" ;;
esac

# ---------- uninstall ----------

if [ "$UNINSTALL" -eq 1 ]; then
	step "Removing Gorget"
	if have gorget; then as_root gorget down >/dev/null 2>&1 || true; fi
	if have dpkg && dpkg -s gorget >/dev/null 2>&1; then
		as_root apt-get remove -y gorget || as_root dpkg -r gorget
	elif have rpm && rpm -q gorget >/dev/null 2>&1; then
		if have dnf; then as_root dnf remove -y gorget; elif have zypper; then as_root zypper -n remove gorget; else as_root rpm -e gorget; fi
	elif [ "$OS" = darwin ]; then
		as_root /usr/local/bin/gorget uninstall-service >/dev/null 2>&1 || true
		for d in /Users/*; do
			u=${d##*/}
			id "$u" >/dev/null 2>&1 && as_root launchctl bootout "gui/$(id -u "$u")/net.gorget.desktop" >/dev/null 2>&1 || true
		done
		as_root rm -f /Library/LaunchAgents/net.gorget.desktop.plist /usr/local/bin/gorget
		as_root rm -rf /Applications/Gorget.app
		as_root pkgutil --forget net.gorget.pkg >/dev/null 2>&1 || true
	else
		if have gorget; then as_root gorget uninstall-service >/dev/null 2>&1 || true; fi
		as_root rm -f /usr/local/bin/gorget
	fi
	say "Gorget is removed. Its settings stay in $( [ "$OS" = darwin ] && echo '/Library/Application Support/Gorget' || echo /var/lib/gorget ); delete that folder to remove them too."
	exit 0
fi

# ---------- download ----------

if [ -n "$VERSION" ]; then
	case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
	BASE="https://github.com/$REPO/releases/download/$VERSION"
else
	BASE="https://github.com/$REPO/releases/latest/download"
fi

step "Checking the latest Gorget release"
fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" || die "couldn't download the release list from $BASE (no internet, or no release yet?)"

# pick PATTERN: the first file in SHA256SUMS whose name matches the extended regex.
pick() { awk '{print $2}' "$TMP/SHA256SUMS" | sed 's/^\*//' | grep -E "$1" | head -n 1 || true; }

download() { # download NAME -> path; verifies the checksum
	name="$1"
	step "Downloading $name"
	fetch "$BASE/$name" "$TMP/$name" || die "download of $name failed"
	want="$(awk -v n="$name" '{f=$2; sub(/^\*/, "", f); if (f == n) print $1}' "$TMP/SHA256SUMS")"
	[ -n "$want" ] || die "$name isn't listed in SHA256SUMS"
	got="$(sha256 "$TMP/$name")"
	[ "$want" = "$got" ] || die "checksum mismatch for $name (expected $want, got $got). Not installing."
	printf '%s' "$TMP/$name"
}

install_binary() {
	bin="$(pick "^gorget_v?[0-9][^_]*_${OS}_${ARCH}\$")"
	[ -n "$bin" ] || die "this release has no gorget build for $OS/$ARCH"
	path="$(download "$bin")"
	step "Installing /usr/local/bin/gorget"
	as_root mkdir -p /usr/local/bin
	as_root install -m 0755 "$path" /usr/local/bin/gorget
	step "Starting the Gorget service"
	as_root /usr/local/bin/gorget uninstall-service >/dev/null 2>&1 || true
	as_root /usr/local/bin/gorget install-service
}

PKG=""
if [ "$OS" = linux ]; then
	if have apt-get && have dpkg; then PKG=deb
	elif have dnf || have yum || have zypper; then PKG=rpm
	fi
	if [ "$PKG" = deb ]; then
		f="$(pick "^gorget_[0-9][^_]*_${ARCH}\\.deb\$")"
		if [ -n "$f" ]; then
			path="$(download "$f")"
			step "Installing $f"
			as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y "$path"
		else PKG=""; fi
	elif [ "$PKG" = rpm ]; then
		f="$(pick "^gorget-[0-9].*\\.${RPMARCH}\\.rpm\$")"
		if [ -n "$f" ]; then
			path="$(download "$f")"
			step "Installing $f"
			if have dnf; then as_root dnf install -y "$path"
			elif have zypper; then as_root zypper -n --no-gpg-checks install "$path"
			else as_root yum install -y "$path"; fi
		else PKG=""; fi
	fi
	# On a desktop, add the tray app too (it needs the package-managed service).
	if [ -n "$PKG" ] && { [ -n "${DISPLAY:-}" ] || [ -n "${WAYLAND_DISPLAY:-}" ]; } && [ "${GORGET_NO_TRAY:-}" != 1 ]; then
		if [ "$PKG" = deb ]; then t="$(pick "^gorget-desktop_[0-9][^_]*_${ARCH}\.deb\$")"; else t="$(pick "^gorget-desktop-[0-9].*\.${RPMARCH}\.rpm\$")"; fi
		if [ -n "$t" ]; then
			path="$(download "$t")"
			step "Installing the tray app ($t)"
			if [ "$PKG" = deb ]; then as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y "$path" || say "${DIM}The tray app couldn't be installed; the service and the gorget command work without it.${RESET}"
			elif have dnf; then as_root dnf install -y "$path" || true
			elif have zypper; then as_root zypper -n --no-gpg-checks install "$path" || true
			else as_root yum install -y "$path" || true; fi
		fi
	fi
	if [ -z "$PKG" ]; then
		install_binary
		if ! have nft; then
			say "${DIM}Note: install nftables for the kill switch and for sharing networks (subnet routes, exit node).${RESET}"
		fi
	fi
	if ! have systemctl && [ -z "$PKG" ]; then
		say "${DIM}No systemd found: if the service didn't start, run 'sudo gorget daemon' yourself.${RESET}"
	fi
else
	f="$(pick "^gorget_[0-9][^_]*_macos\\.pkg\$")"
	if [ -n "$f" ]; then
		path="$(download "$f")"
		step "Installing $f"
		as_root installer -pkg "$path" -target /
	else
		install_binary
	fi
fi

GORGET="$(command -v gorget || echo /usr/local/bin/gorget)"

# Let the person who ran the installer control Gorget (and the tray app) without sudo.
if [ "$(id -u)" -ne 0 ]; then
	as_root "$GORGET" operator set "$(id -un)" >/dev/null 2>&1 || true
fi

# Wait until the service answers.
i=0
until "$GORGET" status >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -ge 20 ] && break
	sleep 0.5
done

say ""
say "${BOLD}Gorget $("$GORGET" version 2>/dev/null | cut -d' ' -f2) is installed.${RESET}"

if [ -z "$SERVER" ]; then
	say ""
	say "Connect this device:"
	say "  gorget up -server vpn.example.com"
	exit 0
fi

# ---------- connect ----------

say ""
step "Connecting to $SERVER"
# Run as the installing user when they may control the service (then a browser can
# open in their session); otherwise as root.
AS=""
if [ "$(id -u)" -ne 0 ] && ! "$GORGET" operator 2>/dev/null | grep -q "can control: true"; then AS="$SUDO"; fi
run_up() { if [ -n "$AS" ]; then $AS "$GORGET" up "$@"; else "$GORGET" up "$@"; fi; }
if [ -n "$SETUP_KEY" ]; then
	# Through standard input, so the key doesn't appear in the process list.
	printf '%s\n' "$SETUP_KEY" | run_up -server "$SERVER" -setup-key -
else
	# The script itself may be arriving on standard input (curl | sh); give the
	# sign-in its own terminal input when there is one.
	if [ -r /dev/tty ]; then run_up -server "$SERVER" </dev/tty; else run_up -server "$SERVER"; fi
fi

say ""
say "Done. Useful commands:"
say "  gorget status       see your devices and how they're connected"
say "  gorget down         disconnect (gorget up to reconnect)"
say "  gorget exit-node    list exit nodes"
