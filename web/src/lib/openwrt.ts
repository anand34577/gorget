/**
 * toOpenWrt converts a wg-quick configuration into a shell script of `uci` commands
 * for an OpenWrt router (22.03 or newer). It creates a "gorget" WireGuard interface,
 * a firewall zone for it, and forwarding between that zone and the LAN, so devices on
 * Gorget reach the router's networks and the reverse.
 */
export function toOpenWrt(name: string, conf: string): string {
  const iface: Record<string, string> = {};
  const peer: Record<string, string> = {};
  let section: Record<string, string> | null = null;
  for (const raw of conf.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    if (/^\[interface\]$/i.test(line)) section = iface;
    else if (/^\[peer\]$/i.test(line)) section = peer;
    else if (section) {
      const i = line.indexOf("=");
      if (i > 0) section[line.slice(0, i).trim().toLowerCase()] = line.slice(i + 1).trim();
    }
  }
  const list = (v?: string) => (v ?? "").split(",").map((s) => s.trim()).filter(Boolean);
  const q = (v: string) => `'${v.replace(/'/g, "'\\''")}'`;
  const endpoint = peer["endpoint"] ?? "";
  const m = /^\[?([^\]]+?)\]?:(\d+)$/.exec(endpoint);
  const host = m ? m[1] : endpoint;
  const port = m ? m[2] : "51820";
  const key = iface["privatekey"] ?? "";
  const placeholder = key.startsWith("<");

  const out: string[] = [
    "#!/bin/sh",
    `# Gorget WireGuard setup for OpenWrt: ${name}`,
    "#",
    "# 1. On the router, install WireGuard once:",
    "#      opkg update && opkg install wireguard-tools luci-proto-wireguard",
    "# 2. Run this script on the router, for example:",
    `#      ssh root@192.168.1.1 'sh -s' < ${name}-openwrt.sh`,
    "# 3. In the Gorget console, open Routes & exit nodes > Add networks behind a router,",
    "#    choose this configuration and add your LAN and VLAN ranges.",
    "#",
    "# Networks in other firewall zones (for example a VLAN in zone \"iot\") need their own",
    "# forwarding: copy the two forwarding blocks below and change dest/src.",
    "",
    "set -e",
  ];
  if (placeholder) out.push("", "# Replace YOUR_PRIVATE_KEY below with the private key saved when this configuration was created.");
  out.push(
    "",
    "uci -q delete network.gorget || true",
    "uci -q delete network.gorget_server || true",
    "uci set network.gorget=interface",
    "uci set network.gorget.proto='wireguard'",
    `uci set network.gorget.private_key=${q(placeholder ? "YOUR_PRIVATE_KEY" : key)}`,
  );
  for (const a of list(iface["address"])) out.push(`uci add_list network.gorget.addresses=${q(a)}`);
  if (iface["mtu"]) out.push(`uci set network.gorget.mtu=${q(iface["mtu"])}`);
  out.push(
    "uci set network.gorget_server=wireguard_gorget",
    "uci set network.gorget_server.description='Gorget gateway'",
    `uci set network.gorget_server.public_key=${q(peer["publickey"] ?? "")}`,
  );
  if (peer["presharedkey"]) out.push(`uci set network.gorget_server.preshared_key=${q(peer["presharedkey"])}`);
  out.push(
    `uci set network.gorget_server.endpoint_host=${q(host)}`,
    `uci set network.gorget_server.endpoint_port=${q(port)}`,
    `uci set network.gorget_server.persistent_keepalive=${q(peer["persistentkeepalive"] || "25")}`,
    "uci set network.gorget_server.route_allowed_ips='1'",
  );
  for (const a of list(peer["allowedips"])) out.push(`uci add_list network.gorget_server.allowed_ips=${q(a)}`);
  out.push(
    "",
    "# Firewall: Gorget traffic may reach the LAN and the LAN may reach Gorget devices.",
    "# The router itself only answers ping from the tunnel. No address translation, so",
    "# your home devices see the real Gorget address of whoever connects.",
    "uci -q delete firewall.gorget || true",
    "uci -q delete firewall.gorget_to_lan || true",
    "uci -q delete firewall.lan_to_gorget || true",
    "uci -q delete firewall.gorget_ping || true",
    "uci set firewall.gorget=zone",
    "uci set firewall.gorget.name='gorget'",
    "uci set firewall.gorget.input='REJECT'",
    "uci set firewall.gorget.output='ACCEPT'",
    "uci set firewall.gorget.forward='REJECT'",
    "uci add_list firewall.gorget.network='gorget'",
    "uci set firewall.gorget_to_lan=forwarding",
    "uci set firewall.gorget_to_lan.src='gorget'",
    "uci set firewall.gorget_to_lan.dest='lan'",
    "uci set firewall.lan_to_gorget=forwarding",
    "uci set firewall.lan_to_gorget.src='lan'",
    "uci set firewall.lan_to_gorget.dest='gorget'",
    "uci set firewall.gorget_ping=rule",
    "uci set firewall.gorget_ping.name='Allow-Ping-Gorget'",
    "uci set firewall.gorget_ping.src='gorget'",
    "uci set firewall.gorget_ping.proto='icmp'",
    "uci set firewall.gorget_ping.icmp_type='echo-request'",
    "uci set firewall.gorget_ping.target='ACCEPT'",
    "",
    "uci commit network",
    "uci commit firewall",
    "/etc/init.d/firewall reload",
    "ifup gorget",
    'echo "Gorget interface is up. Check it with: wg show"',
    "",
  );
  return out.join("\n");
}
