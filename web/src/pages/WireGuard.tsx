import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import QRCode from "qrcode";
import { toRouterScript } from "@/lib/routerScript";
import { Ban, Cable, Download, FileText, Globe, Laptop, MoreHorizontal, Monitor, Pencil, Plus, RefreshCw, Router, Server, Split, Trash2 } from "lucide-react";
import { Explainer, Flow } from "@/components/flow";
import { toast } from "sonner";
import { errMessage, get, post } from "@/lib/api";
import type { Device, UserView } from "@/lib/types";
import { useSession } from "@/lib/session";
import { generateKeyPair, withPrivateKey } from "@/lib/wgkeys";
import { cn, download, fmtBytes, fmtDate, relTime } from "@/lib/utils";
import { Ago, matchesQuery, Pagination, SearchInput, SortTh, useDebounced, useTable, useTick } from "@/components/data";
import {
  Badge,
  Button,
  Checkbox,
  CopyButton,
  Dialog,
  EmptyState,
  ErrorNote,
  Field,
  Input,
  ListEditor,
  Menu,
  MenuItem,
  MenuSeparator,
  Mono,
  Note,
  PageHeader,
  Panel,
  Select,
  Skeleton,
  StatusDot,
  Table,
  Td,
  Th,
  Tip,
} from "@/components/ui";
import { useDeviceActions } from "./Devices";

const modes = {
  split: { label: "Private network only", desc: "Only traffic to your devices and shared networks (home LAN, VLANs) uses the VPN; everything else goes out normally. Recommended for phones, laptops and routers.", icon: <Split /> },
  full: { label: "All traffic", desc: "Everything, including internet browsing, goes through this server, like a commercial VPN.", icon: <Globe /> },
  custom: { label: "Custom ranges", desc: "Send only the address ranges you list.", icon: <FileText /> },
} as const;

function ipKey(ip: string): number {
  const p = ip.split(".").map(Number);
  return p.length === 4 && p.every((n) => Number.isFinite(n)) ? ((p[0] * 256 + p[1]) * 256 + p[2]) * 256 + p[3] : 0;
}

export function WireGuardApps() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { me, can } = useSession();
  const [open, setOpen] = useState(params.get("new") === "1");
  const [showConfig, setShowConfig] = useState<Device | null>(null);
  const [editing, setEditing] = useState<Device | null>(null);
  const [q, setQ] = useState("");
  const dq = useDebounced(q, 200);
  const { data, isLoading } = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices"), refetchInterval: 30_000 });
  const actions = useDeviceActions();
  useTick();
  const all = useMemo(() => (data ?? []).filter((d) => d.kind === "wireguard"), [data]);
  const list = useMemo(() => all.filter((d) => matchesQuery(dq, d.name, d.ipv4, d.user_email, d.last_endpoint, d.tags, d.tunnel_mode)), [all, dq]);
  const table = useTable(list, {
    defaultSort: { key: "name", dir: "asc" },
    sorters: {
      name: (a, b) => a.name.localeCompare(b.name),
      address: (a, b) => ipKey(a.ipv4) - ipKey(b.ipv4),
      status: (a, b) => Number(b.online) - Number(a.online) || b.last_seen_at - a.last_seen_at,
      traffic: (a, b) => b.rx_bytes + b.tx_bytes - (a.rx_bytes + a.tx_bytes),
    },
  });

  useEffect(() => {
    if (params.get("new") === "1") setOpen(true);
  }, [params]);

  return (
    <>
      <PageHeader
        title="WireGuard apps"
        description="Connect anything that speaks WireGuard (the official apps, routers, NAS boxes) through the built-in gateway. No Gorget app needed."
        actions={
          <>
            {can("manage_net") && (
              <Button onClick={() => navigate("/routes?wizard=1")} disabled={!me.features.gateway}>
                <Router /> Connect a router
              </Button>
            )}
            <Button variant="primary" onClick={() => setOpen(true)} disabled={!me.features.gateway}>
              <Plus /> Add WireGuard app
            </Button>
          </>
        }
      />
      {!me.features.gateway && <div className="mb-4"><Note tone="warn">The gateway is disabled in the server configuration, so WireGuard apps can't connect.</Note></div>}
      <div className="mb-4">
        <Explainer id="wireguard" title="How WireGuard apps connect">
          <Flow
            steps={[
              { icon: <Cable />, title: "WireGuard app", sub: "phone, router, NAS" },
              { icon: <Server />, title: "Gorget gateway", sub: me.features.gateway ? "enforces access rules" : "disabled", state: me.features.gateway ? "ok" : "warn" },
              { icon: <Laptop />, title: "Gorget devices", sub: "reached by name or address" },
              { icon: <Monitor />, title: "Shared networks", sub: "home or office, if shared" },
            ]}
          />
          <p className="mt-4 text-center text-xs text-ink-3">
            Pick <b>Private network only</b> to reach your devices and networks while everything else uses the normal connection, or <b>Everything</b> to send all traffic through the gateway.
          </p>
        </Explainer>
      </div>
      <Panel>
        {all.length > 0 && (
          <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3">
            <span className="text-[13px] text-ink-2">
              {all.filter((d) => d.online).length} of {all.length} connected
            </span>
            <SearchInput value={q} onChange={setQ} placeholder="Name, address, owner or routing" label="Search WireGuard apps" className="ml-auto" />
          </div>
        )}
        {isLoading ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : all.length === 0 ? (
          <EmptyState icon={<Cable />} title="No WireGuard apps yet" action={me.features.gateway && <Button variant="primary" onClick={() => setOpen(true)}>Add WireGuard app</Button>}>
            Create a configuration, then scan the QR code with the WireGuard app on your phone or import the file on a computer.
          </EmptyState>
        ) : list.length === 0 ? (
          <EmptyState icon={<Cable />} title="No WireGuard apps match" action={<Button onClick={() => setQ("")}>Clear search</Button>}>
            Try another name or address.
          </EmptyState>
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  <SortTh label="Name" sortKey="name" table={table} />
                  <SortTh label="Address" sortKey="address" table={table} />
                  <Th className="hidden md:table-cell">Routing</Th>
                  <SortTh label="Status" sortKey="status" table={table} className="hidden lg:table-cell" />
                  <SortTh label="Traffic" sortKey="traffic" table={table} className="hidden lg:table-cell" />
                  <Th className="w-10" />
                </tr>
              </thead>
              <tbody>
                {table.rows.map((d) => (
                  <tr key={d.id} className="cursor-pointer hover:bg-surface-2" onClick={() => setEditing(d)}>
                    <Td>
                      <div className="flex items-center gap-2.5">
                        <StatusDot online={d.online} />
                        <div>
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="font-medium">{d.name}</span>
                            {d.state === "disabled" && <Badge tone="danger">{d.expires_at && d.expires_at < Date.now() / 1000 ? "Expired" : "Disabled"}</Badge>}
                            {d.has_psk && <Badge>PSK</Badge>}
                            {d.routes.length > 0 && (
                              <Tip content={`Carries ${d.routes.map((r) => r.cidr).join(", ")}`}>
                                <span>
                                  <Badge tone="blued">Router</Badge>
                                </span>
                              </Tip>
                            )}
                          </div>
                          <div className="text-xs text-ink-3">
                            {d.user_email || "tagged"}
                            {d.expires_at > 0 && ` · expires ${relTime(d.expires_at)}`}
                          </div>
                        </div>
                      </div>
                    </Td>
                    <Td>
                      <Mono>{d.ipv4}</Mono>
                    </Td>
                    <Td className="hidden md:table-cell">
                      <span className="inline-flex items-center gap-1.5 text-ink-2 [&_svg]:size-3.5">
                        {modes[d.tunnel_mode as keyof typeof modes]?.icon}
                        {modes[d.tunnel_mode as keyof typeof modes]?.label ?? d.tunnel_mode}
                      </span>
                    </Td>
                    <Td className="hidden lg:table-cell text-ink-2">
                      {d.online ? (
                        <span className="font-medium text-verdigris">Online</span>
                      ) : d.last_seen_at ? (
                        <span title={fmtDate(d.last_seen_at)}>
                          Offline · <Ago ts={d.last_seen_at} />
                        </span>
                      ) : (
                        "Never connected"
                      )}
                      {d.last_endpoint && (
                        <div className="font-mono text-[11px] text-ink-3">
                          {d.last_endpoint}
                          {d.country && <span className="ml-1 font-sans">{d.country}</span>}
                        </div>
                      )}
                    </Td>
                    <Td className="hidden lg:table-cell text-xs text-ink-2">
                      ↓ {fmtBytes(d.tx_bytes)} · ↑ {fmtBytes(d.rx_bytes)}
                    </Td>
                    <Td onClick={(e) => e.stopPropagation()}>
                      <Menu trigger={<Button variant="ghost" size="icon" aria-label={`Actions for ${d.name}`}><MoreHorizontal /></Button>}>
                        <MenuItem onSelect={() => setEditing(d)}>
                          <Pencil /> Edit
                        </MenuItem>
                        <MenuItem onSelect={() => setShowConfig(d)}>
                          <FileText /> Show configuration
                        </MenuItem>
                        <MenuItem
                          onSelect={async () => {
                            try {
                              await post(`/devices/${d.id}/rotate-psk`);
                              toast.success("New pre-shared key created. Download the configuration again.");
                              actions.refresh();
                            } catch (e) {
                              toast.error(errMessage(e));
                            }
                          }}
                        >
                          <RefreshCw /> Rotate pre-shared key
                        </MenuItem>
                        <MenuItem onSelect={() => actions.toggleBlock(d)}>
                          <Ban /> {d.state === "disabled" ? "Unblock" : "Block"}
                        </MenuItem>
                        <MenuSeparator />
                        <MenuItem danger onSelect={() => actions.remove(d)}>
                          <Trash2 /> Revoke and remove
                        </MenuItem>
                      </Menu>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <Pagination table={table} noun="apps" />
          </>
        )}
      </Panel>
      <p className="mt-3 text-xs text-ink-3">
        Standard WireGuard apps connect through the gateway rather than directly to each device, and can't use single sign-on. Access rules apply to them the same way. A device shows as online while the gateway keeps hearing from it, usually within a few seconds of turning the tunnel on or off.
      </p>
      <CreateDialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v);
          if (!v && params.get("new")) setParams({});
        }}
      />
      {editing && (
        <EditDialog
          device={editing}
          onClose={() => setEditing(null)}
          onShowConfig={() => {
            setShowConfig(editing);
            setEditing(null);
          }}
        />
      )}
      {showConfig && <ConfigDialog device={showConfig} onClose={() => setShowConfig(null)} />}
    </>
  );
}

/** Change how an existing WireGuard app is named and routed. */
function EditDialog({ device: d, onClose, onShowConfig }: { device: Device; onClose: () => void; onShowConfig: () => void }) {
  const { can } = useSession();
  const actions = useDeviceActions();
  const [name, setName] = useState(d.name);
  const [mode, setMode] = useState<keyof typeof modes>((d.tunnel_mode as keyof typeof modes) in modes ? (d.tunnel_mode as keyof typeof modes) : "split");
  const [custom, setCustom] = useState<string[]>(d.custom_allowed_ips ?? []);
  const [busy, setBusy] = useState(false);
  const [changedRouting, setChangedRouting] = useState(false);
  const routingChanged = mode !== d.tunnel_mode || (mode === "custom" && JSON.stringify(custom) !== JSON.stringify(d.custom_allowed_ips ?? []));
  const save = async () => {
    const body: Record<string, unknown> = {};
    if (name.trim() && name !== d.name) body.name = name.trim();
    if (routingChanged) {
      body.tunnel_mode = mode;
      if (mode === "custom") body.custom_allowed_ips = custom;
    }
    if (!Object.keys(body).length) return onClose();
    setBusy(true);
    const ok = await actions.update(d.id, body, "Saved");
    setBusy(false);
    if (!ok) return;
    if (routingChanged) setChangedRouting(true);
    else onClose();
  };
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={d.name}
      description={<span className="font-mono">{d.ipv4}</span>}
      footer={
        changedRouting ? (
          <Button variant="primary" onClick={onShowConfig}>
            Show the new configuration
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button variant="primary" loading={busy} disabled={!name.trim() || (mode === "custom" && !custom.length)} onClick={save}>
              Save changes
            </Button>
          </>
        )
      }
    >
      {changedRouting ? (
        <Note tone="warn">
          Routing changed. The WireGuard app keeps its old settings until you import the new configuration (scan the QR code again, or download the file): the list of networks is part of the app's configuration.
        </Note>
      ) : (
        <div className="space-y-4">
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <fieldset className="space-y-2">
            <legend className="mb-1.5 text-[13px] font-medium">What goes through the VPN</legend>
            {(Object.keys(modes) as (keyof typeof modes)[]).map((m) => (
              <label key={m} className={cn("flex cursor-pointer gap-3 rounded-lg border p-3 [&_svg]:size-4", mode === m ? "border-blued bg-blued-soft" : "border-line hover:border-line-strong")}>
                <input type="radio" name="edit-mode" className="mt-0.5 accent-[var(--blued)]" checked={mode === m} onChange={() => setMode(m)} />
                <span className="text-ink-2">{modes[m].icon}</span>
                <span>
                  <span className="block text-[13px] font-medium">{modes[m].label}</span>
                  <span className="block text-xs text-ink-3">{modes[m].desc}</span>
                </span>
              </label>
            ))}
          </fieldset>
          {mode === "custom" && (
            <Field label="Address ranges">
              <ListEditor values={custom} onChange={setCustom} placeholder="10.0.0.0/8" suggestions={cidrSuggestions} validate={validateCidr} />
            </Field>
          )}
          {!can("manage_net") && <p className="text-xs text-ink-3">You can rename and re-route your own apps. Ask an administrator to change tags or expiry.</p>}
        </div>
      )}
    </Dialog>
  );
}

const cidrSuggestions = [
  { value: "192.168.0.0/16", label: "Home networks", hint: "192.168.0.0/16" },
  { value: "10.0.0.0/8", label: "Private 10.x networks", hint: "10.0.0.0/8" },
  { value: "172.16.0.0/12", label: "Private 172.16–31.x networks", hint: "172.16.0.0/12" },
  { value: "0.0.0.0/0", label: "Everything (IPv4)", hint: "0.0.0.0/0" },
];

function validateCidr(v: string): string | null {
  return /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/.test(v) || /^[0-9a-fA-F:]+\/\d{1,3}$/.test(v) ? null : `${v} isn't a network like 192.168.1.0/24`;
}

function CreateDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { me, can } = useSession();
  const qc = useQueryClient();
  const admin = can("manage_net");
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<UserView[]>("/users"), enabled: admin && open });
  const [name, setName] = useState("");
  const [owner, setOwner] = useState("");
  const defaultMode = (me.wg_tunnel_mode as keyof typeof modes) in modes ? (me.wg_tunnel_mode as keyof typeof modes) : "split";
  const [mode, setMode] = useState<keyof typeof modes>(defaultMode);
  const [custom, setCustom] = useState<string[]>([]);
  const [psk, setPsk] = useState(true);
  const [dns, setDns] = useState(true);
  const [expiry, setExpiry] = useState("0");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<{ name: string; config: string } | null>(null);

  const reset = () => {
    setName("");
    setResult(null);
    setError("");
    setMode(defaultMode);
    setCustom([]);
  };

  const create = async () => {
    setBusy(true);
    setError("");
    try {
      const keys = generateKeyPair();
      const days = Number(expiry);
      const r = await post<{ device: Device; config: string }>("/wireguard-configs", {
        name,
        user_id: owner || undefined,
        public_key: keys.publicKey,
        tunnel_mode: mode,
        custom_allowed_ips: mode === "custom" ? custom : undefined,
        preshared_key: psk,
        dns,
        expires_at: days > 0 ? Math.floor(Date.now() / 1000) + days * 86400 : 0,
      });
      setResult({ name: r.device.name, config: withPrivateKey(r.config, keys.privateKey) });
      qc.invalidateQueries({ queryKey: ["devices"] });
      toast.success(`${r.device.name} added`);
    } catch (e) {
      setError(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) setTimeout(reset, 200);
      }}
      title={result ? `Connect ${result.name}` : "Add a WireGuard app"}
      description={result ? "Scan the code with the WireGuard app, or download the file. The private key is shown only now." : undefined}
      wide={!!result}
      footer={
        result ? (
          <Button variant="primary" onClick={() => onOpenChange(false)}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button variant="primary" loading={busy} disabled={!name.trim() || (mode === "custom" && !custom.length)} onClick={create}>
              Create configuration
            </Button>
          </>
        )
      }
    >
      {result ? (
        <ConfigView name={result.name} config={result.config} />
      ) : (
        <div className="space-y-4">
          {error && <ErrorNote>{error}</ErrorNote>}
          <Field label="Name" hint="Pick a name that tells you which device it is.">
            <div className="space-y-2">
              <Input autoFocus value={name} placeholder="pixel-phone" onChange={(e) => setName(e.target.value)} />
              <div className="flex flex-wrap gap-1.5">
                {["phone", "laptop", "tablet", "router", "nas"].map((w) => {
                  const who = (me.user.name || me.user.email.split("@")[0]).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
                  const v = `${who}-${w}`;
                  return (
                    <button key={w} type="button" onClick={() => setName(v)} className="rounded-md border border-line bg-surface-2 px-2 py-0.5 font-mono text-[11.5px] text-ink-2 hover:border-line-strong hover:text-ink">
                      {v}
                    </button>
                  );
                })}
              </div>
            </div>
          </Field>
          {admin && (
            <Field label="Belongs to">
              <Select value={owner} onChange={(e) => setOwner(e.target.value)}>
                <option value="">Me ({me.user.email})</option>
                {users.data
                  ?.filter((u) => u.id !== me.user.id)
                  .map((u) => (
                    <option key={u.id} value={u.id}>
                      {u.name ? `${u.name} (${u.email})` : u.email}
                    </option>
                  ))}
              </Select>
            </Field>
          )}
          <fieldset className="space-y-2">
            <legend className="mb-1.5 text-[13px] font-medium">What goes through the VPN</legend>
            {(Object.keys(modes) as (keyof typeof modes)[]).map((m) => (
              <label key={m} className={cn("flex cursor-pointer gap-3 rounded-lg border p-3 [&_svg]:size-4", mode === m ? "border-blued bg-blued-soft" : "border-line hover:border-line-strong")}>
                <input type="radio" name="mode" className="mt-0.5 accent-[var(--blued)]" checked={mode === m} onChange={() => setMode(m)} />
                <span className="text-ink-2">{modes[m].icon}</span>
                <span>
                  <span className="block text-[13px] font-medium">{modes[m].label}</span>
                  <span className="block text-xs text-ink-3">{modes[m].desc}</span>
                </span>
              </label>
            ))}
          </fieldset>
          {mode === "custom" && (
            <Field label="Address ranges">
              <ListEditor values={custom} onChange={setCustom} placeholder="10.0.0.0/8" suggestions={cidrSuggestions} validate={validateCidr} />
            </Field>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Stops working after">
              <Select value={expiry} onChange={(e) => setExpiry(e.target.value)}>
                <option value="0">Never</option>
                <option value="1">1 day</option>
                <option value="7">7 days</option>
                <option value="30">30 days</option>
                <option value="90">90 days</option>
                <option value="365">1 year</option>
              </Select>
            </Field>
          </div>
          <div className="space-y-2">
            <Checkbox checked={psk} onChange={setPsk} label="Add a pre-shared key (extra layer of encryption, recommended)" />
            <Checkbox checked={dns} onChange={setDns} label={`Use Gorget DNS (resolve ${me.network.domain} names)`} />
          </div>
          <p className="text-xs text-ink-3">Keys are generated in your browser. The private key is never sent to the server.</p>
        </div>
      )}
    </Dialog>
  );
}

function ConfigView({ name, config: wgConfig }: { name: string; config: string }) {
  const [qr, setQr] = useState("");
  const [format, setFormat] = useState<"wg" | "script">("wg");
  const [manage, setManage] = useState(true);
  const [masq, setMasq] = useState(false);
  const hasKey = !wgConfig.includes("<REPLACE_WITH_YOUR_PRIVATE_KEY>");
  const config = format === "script" ? toRouterScript(name, wgConfig, { manage, masquerade: masq }) : wgConfig;
  const file = format === "script" ? `${name}-router.sh` : `${name}.conf`;
  useEffect(() => {
    if (hasKey) QRCode.toDataURL(wgConfig, { margin: 1, width: 260, errorCorrectionLevel: "M" }).then(setQr);
  }, [wgConfig, hasKey]);
  return (
    <div className="grid gap-5 md:grid-cols-[260px_1fr]">
      <div className="flex flex-col items-center gap-3">
        {hasKey ? (
          qr ? <img src={qr} alt={`QR code for ${name}`} className="rounded-lg border border-line bg-white p-2" width={260} height={260} /> : <Skeleton className="size-[260px]" />
        ) : (
          <div className="grid size-[260px] place-items-center rounded-lg border border-dashed border-line p-6 text-center text-xs text-ink-3">
            The QR code is only available right after creation, because the private key isn't stored.
          </div>
        )}
        <Button className="w-full" onClick={() => download(file, config)}>
          <Download /> Download {file}
        </Button>
      </div>
      <div className="min-w-0">
        <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
          <div className="inline-flex rounded-md border border-line bg-surface-2 p-0.5" role="radiogroup" aria-label="Configuration format">
            {(
              [
                ["wg", "WireGuard app / wg-quick"],
                ["script", "Router script (uci)"],
              ] as const
            ).map(([id, label]) => (
              <button
                key={id}
                type="button"
                role="radio"
                aria-checked={format === id}
                onClick={() => setFormat(id)}
                className={"rounded px-2.5 py-1 text-xs font-medium " + (format === id ? "bg-surface text-ink shadow-sm" : "text-ink-3 hover:text-ink")}
              >
                {label}
              </button>
            ))}
          </div>
          <CopyButton value={config} />
        </div>
        {format === "script" && (
          <div className="mb-3 space-y-2 rounded-lg border border-line bg-surface-2 p-3">
            <p className="text-xs text-ink-2">
              A script of <Mono className="text-[11px]">uci</Mono> commands: it sets up the interface, a firewall zone and LAN forwarding. Afterwards add the router's networks under Routes &amp; exit nodes.
            </p>
            <Checkbox checked={manage} onChange={setManage} label="Let Gorget devices open the router's SSH and web interface" />
            <Checkbox checked={masq} onChange={setMasq} label="My home devices don't use this router as their gateway (VLANs behind another router): translate addresses" />
          </div>
        )}
        <pre className="max-h-[340px] overflow-auto rounded-lg border border-line bg-sunken p-3 font-mono text-[12px] leading-relaxed">{config}</pre>
        {!hasKey && <p className="mt-2 text-xs text-ink-3">Replace the placeholder with the private key you saved when the app was created, or create a new configuration.</p>}
      </div>
    </div>
  );
}

function ConfigDialog({ device, onClose }: { device: Device; onClose: () => void }) {
  const { data, error } = useQuery({ queryKey: ["wgconf", device.id], queryFn: () => get<{ config: string }>(`/devices/${device.id}/wireguard-config`) });
  const conf = useMemo(() => data?.config ?? "", [data]);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()} title={device.name} description={`Added ${fmtDate(device.created_at)}`} wide>
      {error ? <ErrorNote>{errMessage(error)}</ErrorNote> : conf ? <ConfigView name={device.name} config={conf} /> : <Skeleton className="h-64" />}
    </Dialog>
  );
}
