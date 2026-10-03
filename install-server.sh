#!/bin/sh
# Gorget server installer for Linux (a VPS or a machine at home).
#
#   curl -fsSL https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh | sh
#
# It asks for your domain name, installs the server as a service, starts it and prints the
# link where you create your owner account. Run it again later to upgrade.
#
# Before you start: a Linux machine with a public address, and a DNS record (for example
# vpn.example.com) that points at it. Ports to open: TCP 80+443, UDP 443, 3478, 3479, 51820.
#
# Options (as environment variables, or after "sh -s --"):
#   GORGET_DOMAIN  / --domain NAME     the server's domain name; skips the questions
#   GORGET_EMAIL   / --email ADDRESS   email for Let's Encrypt expiry notices (optional)
#   GORGET_VERSION / --version vX.Y.Z  a specific release (default: the latest)
#                    --docker          run in Docker (needs Docker with the compose plugin)
#                                      instead of installing the binary as a system service
#                    --yes             never ask; take the defaults (needs --domain)
#                    --no-init         only install the binary
#                    --uninstall       remove the service (your data stays)
#   GORGET_DIR                         where --docker keeps its files (default /opt/gorget)
#
# Everything is downloaded from the project's GitHub releases and checked against the
# release's SHA256SUMS. Read the script first if you like:
#   curl -fsSLo install-server.sh https://raw.githubusercontent.com/anand34577/gorget/main/install-server.sh

set -eu

REPO="anand34577/gorget"
VERSION="${GORGET_VERSION:-}"
DOMAIN="${GORGET_DOMAIN:-}"
EMAIL="${GORGET_EMAIL:-}"
DIR="${GORGET_DIR:-/opt/gorget}"
MODE=native
ASSUME_YES=0
INIT=1
UNINSTALL=0
CONFIG=/etc/gorget/config.yaml

while [ $# -gt 0 ]; do
	case "$1" in
	--version) VERSION="${2:?--version needs a value}"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--domain) DOMAIN="${2:?--domain needs a value}"; shift 2 ;;
	--domain=*) DOMAIN="${1#*=}"; shift ;;
	--email) EMAIL="${2:?--email needs a value}"; shift 2 ;;
	--email=*) EMAIL="${1#*=}"; shift ;;
	--docker) MODE=docker; shift ;;
	--native) MODE=native; shift ;;
	-y | --yes) ASSUME_YES=1; shift ;;
	--no-init) INIT=0; shift ;;
	--uninstall) UNINSTALL=1; shift ;;
	-h | --help) sed -n '2,26p' "$0" 2>/dev/null || true; exit 0 ;;
	*) echo "Unknown option: $1 (see --help)" >&2; exit 2 ;;
	esac
done

if [ -t 1 ]; then BOLD="$(printf '\033[1m')"; DIM="$(printf '\033[2m')"; RED="$(printf '\033[31m')"; RESET="$(printf '\033[0m')"; else BOLD=""; DIM=""; RED=""; RESET=""; fi
step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*" >&2; }
note() { printf '%s%s%s\n' "$DIM" "$*" "$RESET" >&2; }
die() { printf '%sError:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

[ "$(uname -s)" = Linux ] || die "this installer is for Linux. On Windows or macOS, download gorget-server from https://github.com/$REPO/releases and see https://github.com/$REPO/wiki/Install-the-server"

SUDO=""
if [ "$(id -u)" -ne 0 ]; then
	if have sudo; then SUDO="sudo"; elif have doas; then SUDO="doas"; else die "run this as root, or install sudo"; fi
fi
as_root() { if [ -n "$SUDO" ]; then $SUDO "$@"; else "$@"; fi; }

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

# Is there a terminal to ask questions on? (The script itself arrives on standard input.)
can_ask() { [ "$ASSUME_YES" -eq 0 ] && [ -r /dev/tty ]; }

# The address people will use, from the saved configuration.
configured_url() {
	as_root sed -n 's/^public_url:[[:space:]]*//p' "$CONFIG" 2>/dev/null | head -n 1 | tr -d "\"'"
}

print_ports() {
	cat >&2 <<'EOF'

Open these ports in the server's firewall (and in your cloud provider's security group):
  TCP 80, 443    web console, apps, free HTTPS certificate
  UDP 443        HTTP/3
  UDP 3478       STUN (helps devices connect directly)
  UDP 3479       UDP relay
  UDP 51820      standard WireGuard apps (only if you use them)
EOF
	if have ufw && as_root ufw status 2>/dev/null | grep -q "Status: active"; then
		echo "  ufw is active here:  sudo ufw allow 80,443/tcp && sudo ufw allow 443,3478,3479,51820/udp" >&2
	elif have firewall-cmd && as_root firewall-cmd --state >/dev/null 2>&1; then
		echo "  firewalld is active here:  sudo firewall-cmd --permanent --add-port={80,443}/tcp --add-port={443,3478,3479,51820}/udp && sudo firewall-cmd --reload" >&2
	fi
}

ask_domain() {
	[ -n "$DOMAIN" ] && return 0
	can_ask || die "no domain given. Run again with --domain vpn.example.com (and --yes if there is no terminal)"
	while [ -z "$DOMAIN" ]; do
		printf 'Domain name of this server, e.g. vpn.example.com: ' >&2
		read -r DOMAIN </dev/tty || die "no input"
		DOMAIN="$(printf '%s' "$DOMAIN" | sed 's#^https\{0,1\}://##; s#/.*$##; s/[[:space:]]//g')"
	done
	if [ -z "$EMAIL" ]; then
		printf "Email for Let's Encrypt expiry notices (optional, press Enter to skip): " >&2
		read -r EMAIL </dev/tty || EMAIL=""
	fi
}

warn_dns() {
	have getent || return 0
	if ! getent hosts "$DOMAIN" >/dev/null 2>&1; then
		note "Note: $DOMAIN doesn't resolve yet. Create a DNS A (and AAAA) record pointing at this server's public address; HTTPS certificates can't be issued until it does."
	fi
}

# ---------- Docker ----------

docker_run() { # docker, with sudo when this user can't talk to the Docker daemon
	if docker info >/dev/null 2>&1; then docker "$@"; else as_root docker "$@"; fi
}

docker_main() {
	have docker || die "Docker isn't installed. Install it first (https://docs.docker.com/engine/install/, or: curl -fsSL https://get.docker.com | sh) and run this again, or leave out --docker to install without Docker."
	docker_run compose version >/dev/null 2>&1 || die "Docker is installed but the 'compose' plugin is missing. Install docker-compose-plugin, or leave out --docker."

	if [ "$UNINSTALL" -eq 1 ]; then
		step "Stopping Gorget in $DIR"
		[ -f "$DIR/docker-compose.yml" ] || die "no Gorget Docker files in $DIR"
		(cd "$DIR" && docker_run compose down)
		echo "Gorget is stopped and removed. Its data is kept in the Docker volume gorget_gorget-data; delete it with 'docker volume rm gorget_gorget-data'."
		exit 0
	fi

	as_root mkdir -p "$DIR"
	step "Downloading the Docker setup into $DIR"
	fetch "https://raw.githubusercontent.com/$REPO/main/deploy/docker/docker-compose.yml" "$TMP/docker-compose.yml" || die "couldn't download the compose file"
	as_root install -m 0644 "$TMP/docker-compose.yml" "$DIR/docker-compose.yml"

	if as_root test -f "$DIR/.env"; then
		note "Keeping your existing $DIR/.env (this is an upgrade)."
	else
		ask_domain
		warn_dns
		{
			echo "GORGET_DOMAIN=$DOMAIN"
			echo "GORGET_EMAIL=$EMAIL"
			if [ -n "$VERSION" ]; then echo "GORGET_VERSION=${VERSION#v}"; fi
		} >"$TMP/env"
		as_root install -m 0600 "$TMP/env" "$DIR/.env"
	fi

	step "Starting Gorget (the first start downloads the image)"
	(cd "$DIR" && docker_run compose pull && docker_run compose up -d) || die "docker compose failed; see the messages above"

	step "Waiting for the server"
	i=0
	link=""
	while [ "$i" -lt 45 ]; do
		out="$(cd "$DIR" && docker_run compose exec -T gorget gorget-server setup-link 2>/dev/null || true)"
		case "$out" in
		https://* | http://*) link="$out"; break ;;
		"No setup is pending"*) break ;;
		esac
		i=$((i + 1))
		sleep 2
	done
	echo
	if [ -n "$link" ]; then
		echo "${BOLD}Gorget is running.${RESET} Finish setting it up in your browser:"
		echo
		echo "  $link"
		echo
		echo "Create the owner account there. The first visit can take a few seconds while the HTTPS certificate is issued."
	elif [ "$i" -lt 45 ]; then
		echo "${BOLD}Gorget is upgraded and running.${RESET}"
	else
		echo "The container is starting but isn't ready yet. See: cd $DIR && sudo docker compose logs gorget"
	fi
	print_ports
	url="$(sed -n 's/^GORGET_DOMAIN=//p' "$DIR/.env" 2>/dev/null | head -n 1)"
	[ -n "$url" ] || url="YOUR-SERVER"
	echo >&2
	echo "Then, on each Linux or Mac device:  curl -fsSL https://$url/install.sh | sh" >&2
	echo "Windows (PowerShell):               irm https://$url/install.ps1 | iex" >&2
	echo "Upgrade later:  run this installer again.  Logs:  cd $DIR && sudo docker compose logs -f gorget" >&2
	exit 0
}

if [ "$MODE" = docker ]; then docker_main; fi

# ---------- native install ----------

if [ "$UNINSTALL" -eq 1 ]; then
	step "Removing the Gorget server service"
	if have gorget-server; then
		as_root gorget-server stop >/dev/null 2>&1 || true
		as_root gorget-server uninstall || true
	fi
	as_root rm -f /usr/local/bin/gorget-server
	echo "The server is removed. Its configuration (/etc/gorget) and data (/var/lib/gorget) stay; delete them to remove everything."
	exit 0
fi

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
armv7l | armv6l) ARCH=arm ;;
*) die "unsupported processor $(uname -m)" ;;
esac

if [ -n "$VERSION" ]; then
	case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
	BASE="https://github.com/$REPO/releases/download/$VERSION"
else
	BASE="https://github.com/$REPO/releases/latest/download"
fi

step "Checking the latest Gorget release"
fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" || die "couldn't download the release list from $BASE (no internet, or no such version?)"
NAME="$(awk '{print $2}' "$TMP/SHA256SUMS" | sed 's/^\*//' | grep -E "^gorget-server_v?[0-9][^_]*_linux_${ARCH}\$" | head -n 1 || true)"
[ -n "$NAME" ] || die "this release has no gorget-server for linux/$ARCH"

step "Downloading $NAME"
fetch "$BASE/$NAME" "$TMP/gorget-server" || die "download failed"
WANT="$(awk -v n="$NAME" '{f=$2; sub(/^\*/, "", f); if (f == n) print $1}' "$TMP/SHA256SUMS")"
GOT="$(sha256 "$TMP/gorget-server")"
[ "$WANT" = "$GOT" ] || die "checksum mismatch for $NAME. Not installing."

# An existing install means this is an upgrade.
UPGRADE=0
if [ -f "$CONFIG" ] && have gorget-server; then UPGRADE=1; fi

step "Installing /usr/local/bin/gorget-server"
as_root install -m 0755 "$TMP/gorget-server" /usr/local/bin/gorget-server

if ! have nft; then
	note "Note: install nftables if standard WireGuard apps or routers will connect (the gateway needs it): sudo apt install nftables"
fi

if [ "$INIT" -eq 0 ]; then
	echo "Installed. Next: sudo gorget-server init && sudo gorget-server install"
	exit 0
fi

if [ -f "$CONFIG" ]; then
	step "Found $CONFIG; checking it"
	as_root /usr/local/bin/gorget-server check -config "$CONFIG" || note "Fix the items marked above, then run: sudo gorget-server restart"
elif [ -n "$DOMAIN" ]; then
	warn_dns
	step "Writing $CONFIG"
	set -- init -config "$CONFIG" -yes -domain "$DOMAIN"
	if [ -n "$EMAIL" ]; then set -- "$@" -email "$EMAIL"; fi
	as_root /usr/local/bin/gorget-server "$@"
elif can_ask; then
	step "Setting up"
	as_root /usr/local/bin/gorget-server init -config "$CONFIG" </dev/tty
else
	echo "Installed. There's no terminal to ask the setup questions. Run: sudo gorget-server init && sudo gorget-server install"
	echo "Or run this installer again with: --domain vpn.example.com --yes"
	exit 0
fi

step "Installing and starting the service"
if as_root /usr/local/bin/gorget-server install -config "$CONFIG"; then
	: # the command prints the setup link itself
else
	# Already installed (an upgrade): restart it on the new binary.
	as_root /usr/local/bin/gorget-server restart || die "the service didn't restart; see: sudo journalctl -u gorget-server -n 50"
	if [ "$UPGRADE" -eq 1 ]; then echo; echo "${BOLD}Gorget is upgraded and running.${RESET}"; fi
	as_root /usr/local/bin/gorget-server setup-link -config "$CONFIG" || true
fi

print_ports
url="$(configured_url)"
echo >&2
echo "Then, on each Linux or Mac device:  curl -fsSL ${url:-https://YOUR-SERVER}/install.sh | sh" >&2
echo "Windows (PowerShell):               irm ${url:-https://YOUR-SERVER}/install.ps1 | iex" >&2
echo "Check the machine any time:  sudo gorget-server check    Upgrade:  run this installer again" >&2
