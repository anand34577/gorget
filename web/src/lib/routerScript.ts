/**
 * toRouterScript converts a wg-quick configuration into a shell script of `uci` commands
 * for routers configured with uci (OpenWrt-style firmware, 22.03 or newer). It creates a "gorget" WireGuard interface,
 * a firewall zone for it, and forwarding between that zone and the LAN, so devices on
 * Gorget reach the router's networks and the reverse.
 */
export interface RouterScriptOptions {
  /** Let Gorget devices open the router's own SSH and web interface (the access rules still decide who). */
  manage?: boolean;
  /**
   * Translate the source address of Gorget traffic entering the LAN. Needed when home devices
   * don't use this router as their gateway (a VLAN behind another router, a managed switch).
   */
  masquerade?: boolean;
}

export function toRouterScript(name: string, conf: string, opts: RouterScriptOptions = {}): string {
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
    `# Gorget WireGuard setup (uci router): ${name}`,
    "#",
    "# 1. Make sure WireGuard is installed on the router (many have it built in;",
    "#    otherwise install the wireguard-tools package with its package manager).",
    "# 2. Run this script on the router, for example:",
    `#      ssh root@192.168.1.1 'sh -s' < ${name}-router.sh`,
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
    opts.manage ? "# Gorget devices may also open the router's SSH and web interface." : "# The router itself only answers ping from the tunnel.",
    opts.masquerade ? "# Gorget traffic is translated to the router's address on the way into the LAN." : "# No address translation: your home devices see the real Gorget address of whoever connects.",
    "uci -q delete firewall.gorget || true",
    "uci -q delete firewall.gorget_manage || true",
    "uci -q delete firewall.gorget_masq || true",
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
  );
  if (opts.manage) {
    out.push(
      "uci set firewall.gorget_manage=rule",
      "uci set firewall.gorget_manage.name='Allow-Manage-Gorget'",
      "uci set firewall.gorget_manage.src='gorget'",
      "uci set firewall.gorget_manage.proto='tcp'",
      "uci set firewall.gorget_manage.dest_port='22 80 443'",
      "uci set firewall.gorget_manage.target='ACCEPT'",
    );
  }
  if (opts.masquerade) {
    // The first non-host range the gateway sends us is the Gorget network itself.
    const overlay = list(peer["allowedips"]).find((a) => /^\d+\.\d+\.\d+\.\d+\/\d+$/.test(a) && !a.endsWith("/32") && !a.endsWith("/0")) ?? "100.80.0.0/16";
    out.push(
      "uci set firewall.gorget_masq=nat",
      "uci set firewall.gorget_masq.name='Gorget-to-LAN'",
      "uci set firewall.gorget_masq.src='lan'",
      `uci set firewall.gorget_masq.src_ip=${q(overlay)}`,
      "uci set firewall.gorget_masq.target='MASQUERADE'",
    );
  }
  out.push(
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
