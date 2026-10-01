import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import QRCode from "qrcode";
import { toOpenWrt } from "@/lib/openwrt";
import { Ban, Cable, Download, FileText, Globe, MoreHorizontal, Plus, RefreshCw, Split, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { errMessage, get, post } from "@/lib/api";
import type { Device, UserView } from "@/lib/types";
import { useSession } from "@/lib/session";
import { generateKeyPair, withPrivateKey } from "@/lib/wgkeys";
import { cn, download, fmtBytes, fmtDate, relTime } from "@/lib/utils";
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
  full: { label: "All traffic", desc: "Everything, including internet browsing, goes through the gateway.", icon: <Globe /> },
  split: { label: "Private network only", desc: "Only traffic to your devices and shared networks uses the VPN. Choose this for a router that shares its home network.", icon: <Split /> },
  custom: { label: "Custom ranges", desc: "Send only the address ranges you list.", icon: <FileText /> },
} as const;

export function WireGuardApps() {
  const [params, setParams] = useSearchParams();
  const { me } = useSession();
  const [open, setOpen] = useState(params.get("new") === "1");
  const [showConfig, setShowConfig] = useState<Device | null>(null);
  const { data, isLoading } = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices"), refetchInterval: 30_000 });
  const actions = useDeviceActions();
  const list = (data ?? []).filter((d) => d.kind === "wireguard");

  useEffect(() => {
    if (params.get("new") === "1") setOpen(true);
  }, [params]);

  return (
    <>
      <PageHeader
        title="WireGuard apps"
        description="Connect anything that speaks WireGuard (the official apps, routers, NAS boxes) through the built-in gateway. No Gorget app needed."
        actions={
          <Button variant="primary" onClick={() => setOpen(true)} disabled={!me.features.gateway}>
            <Plus /> Add WireGuard app
          </Button>
        }
      />
      {!me.features.gateway && <div className="mb-4"><Note tone="warn">The gateway is disabled in the server configuration, so WireGuard apps can't connect.</Note></div>}
      <Panel>
        {isLoading ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : list.length === 0 ? (
          <EmptyState icon={<Cable />} title="No WireGuard apps yet" action={me.features.gateway && <Button variant="primary" onClick={() => setOpen(true)}>Add WireGuard app</Button>}>
            Create a configuration, then scan the QR code with the WireGuard app on your phone or import the file on a computer.
          </EmptyState>
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>Name</Th>
                <Th>Address</Th>
                <Th className="hidden md:table-cell">Routing</Th>
                <Th className="hidden lg:table-cell">Status</Th>
                <Th className="hidden lg:table-cell">Traffic</Th>
                <Th className="w-10" />
              </tr>
            </thead>
            <tbody>
              {list.map((d) => (
                <tr key={d.id} className="hover:bg-surface-2">
                  <Td>
                    <div className="flex items-center gap-2.5">
                      <StatusDot online={d.online} />
                      <div>
                        <div className="flex items-center gap-1.5">
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
                    {d.online ? <span className="font-medium text-verdigris">Online</span> : d.last_seen_at ? relTime(d.last_seen_at) : "never"}
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
                  <Td>
                    <Menu trigger={<Button variant="ghost" size="icon" aria-label={`Actions for ${d.name}`}><MoreHorizontal /></Button>}>
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
        )}
      </Panel>
      <p className="mt-3 text-xs text-ink-3">
        Standard WireGuard apps connect through the gateway rather than directly to each device, and can't use single sign-on. Access rules apply to them the same way.
      </p>
      <CreateDialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v);
          if (!v && params.get("new")) setParams({});
        }}
      />
      {showConfig && <ConfigDialog device={showConfig} onClose={() => setShowConfig(null)} />}
    </>
  );
}

function CreateDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { me, can } = useSession();
  const qc = useQueryClient();
  const admin = can("manage_net");
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<UserView[]>("/users"), enabled: admin && open });
  const [name, setName] = useState("");
  const [owner, setOwner] = useState("");
  const [mode, setMode] = useState<keyof typeof modes>("full");
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
    setMode("full");
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
          <Field label="Name" hint="For example: pixel-phone, office-router.">
            <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} />
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
              <ListEditor values={custom} onChange={setCustom} placeholder="10.0.0.0/8" />
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
  const [format, setFormat] = useState<"wg" | "openwrt">("wg");
  const hasKey = !wgConfig.includes("<REPLACE_WITH_YOUR_PRIVATE_KEY>");
  const config = format === "openwrt" ? toOpenWrt(name, wgConfig) : wgConfig;
  const file = format === "openwrt" ? `${name}-openwrt.sh` : `${name}.conf`;
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
                ["openwrt", "OpenWrt router"],
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
        {format === "openwrt" && (
          <p className="mb-2 text-xs text-ink-2">
            A script of <Mono className="text-[11px]">uci</Mono> commands: it sets up the interface, a firewall zone and LAN forwarding. Afterwards add the router's networks under Routes &amp; exit nodes.
          </p>
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
