import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Pencil, Plus, Send, Trash2, XCircle } from "lucide-react";
import { toast } from "sonner";
import { ApiError, del, errMessage, get, patch, post, put } from "@/lib/api";
import type { ClusterInstance, GatewayStatus, OIDCProvider, Role, Settings, SettingsResponse, Webhook } from "@/lib/types";
import { useSession } from "@/lib/session";
import { fmtDate, relTime, roleLabel } from "@/lib/utils";
import {
  Badge,
  Button,
  Checkbox,
  confirmAction,
  CopyButton,
  Dialog,
  ErrorNote,
  Field,
  Input,
  ListEditor,
  Mono,
  Note,
  PageHeader,
  Panel,
  PanelHeader,
  SecretBox,
  Select,
  Skeleton,
  Table,
  Td,
  Th,
  ToggleRow,
} from "@/components/ui";
import { useReauth } from "./Account";
import { ClusterCard, HealthTab, ProvisioningTab, RoutingTab } from "./SettingsExtra";
import { EmailTab } from "./SettingsEmail";
import { NotificationsTab } from "./SettingsNotifications";

type Section = keyof Settings;

function useSettings() {
  return useQuery({ queryKey: ["settings"], queryFn: () => get<SettingsResponse>("/settings") });
}

/** Local draft of one settings section with save/reset. */
export function useSection<K extends Section>(key: K, data?: SettingsResponse) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<Settings[K] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (data) setDraft(structuredClone(data.settings[key]));
  }, [data, key]);
  const dirty = !!data && !!draft && JSON.stringify(draft) !== JSON.stringify(data.settings[key]);
  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      await put(`/settings/${key}`, draft);
      toast.success("Settings saved");
      qc.invalidateQueries({ queryKey: ["settings"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return { draft, setDraft, dirty, save, busy, err };
}

export function SaveBar({ dirty, busy, save, err }: { dirty: boolean; busy: boolean; save: () => void; err: string }) {
  const { can } = useSession();
  if (!can("manage_net")) return null;
  return (
    <div className="space-y-3 border-t border-line px-5 py-3">
      {err && <ErrorNote>{err}</ErrorNote>}
      <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
        Save changes
      </Button>
    </div>
  );
}

const sections: { label: string; items: [string, string][] }[] = [
  {
    label: "Network",
    items: [
      ["network", "Addresses & names"],
      ["devices", "Devices"],
      ["health", "Device health"],
      ["routing", "Domain routing"],
    ],
  },
  {
    label: "Apps & gateway",
    items: [
      ["clients", "Gorget apps"],
      ["gateway", "WireGuard gateway"],
    ],
  },
  {
    label: "Security",
    items: [
      ["signin", "Sign-in"],
      ["sso", "Single sign-on"],
      ["provisioning", "Provisioning"],
    ],
  },
  {
    label: "Notifications",
    items: [
      ["notifications", "Alerts, Gotify & ntfy"],
      ["email", "Email"],
      ["webhooks", "Webhooks"],
    ],
  },
  { label: "Server", items: [["system", "Status"]] },
];

export function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const { can } = useSession();
  const { data } = useSettings();
  const tab = params.get("tab") ?? "network";
  if (!data) return <Skeleton className="h-96" />;
  const go = (t: string) => setParams({ tab: t }, { replace: true });
  const all = sections.flatMap((s) => s.items);
  return (
    <>
      <PageHeader title="Settings" description={`${data.server.public_url} · ${data.server.database} database · version ${data.server.version}`} />
      <div className="grid gap-6 lg:grid-cols-[200px_minmax(0,1fr)] lg:gap-8">
        <div className="lg:hidden">
          <Select value={tab} onChange={(e) => go(e.target.value)} aria-label="Settings section">
            {sections.map((sec) => (
              <optgroup key={sec.label} label={sec.label}>
                {sec.items.map(([id, label]) => (
                  <option key={id} value={id}>
                    {label}
                  </option>
                ))}
              </optgroup>
            ))}
          </Select>
        </div>
        <nav aria-label="Settings sections" className="sticky top-4 hidden h-fit space-y-5 lg:block">
          {sections.map((sec) => (
            <div key={sec.label}>
              <div className="mb-1 px-2.5 text-[11px] font-medium uppercase tracking-wider text-ink-3">{sec.label}</div>
              <ul className="space-y-0.5">
                {sec.items.map(([id, label]) => (
                  <li key={id}>
                    <button
                      onClick={() => go(id)}
                      aria-current={tab === id ? "page" : undefined}
                      className={"w-full rounded-md px-2.5 py-1.5 text-left text-[13px] font-medium transition-colors " + (tab === id ? "bg-surface text-ink shadow-sm" : "text-ink-2 hover:bg-sunken hover:text-ink")}
                    >
                      {label}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>
        <div className="min-w-0">
          <h2 className="mb-4 font-display text-xl font-semibold">{all.find(([id]) => id === tab)?.[1] ?? "Settings"}</h2>
          {tab === "network" && <NetworkTab data={data} />}
          {tab === "devices" && <DevicesTab data={data} />}
          {tab === "health" && <HealthTab data={data} />}
          {tab === "routing" && <RoutingTab data={data} />}
          {tab === "clients" && <ClientsTab data={data} />}
          {tab === "gateway" && <GatewayTab data={data} />}
          {tab === "signin" && <SignInTab data={data} canEdit={can("manage_sys")} />}
          {tab === "sso" && <SSOTab />}
          {tab === "provisioning" && <ProvisioningTab />}
          {tab === "notifications" && <NotificationsTab />}
          {tab === "email" && <EmailTab />}
          {tab === "webhooks" && <WebhooksTab />}
          {tab === "system" && <SystemTab />}
        </div>
      </div>
    </>
  );
}

function NetworkTab({ data }: { data: SettingsResponse }) {
  const s = useSection("network", data);
  const [readdress, setReaddress] = useState(false);
  if (!s.draft) return null;
  const d = s.draft;
  return (
    <div className="space-y-6">
      <Panel>
        <PanelHeader title="Network" />
        <div className="grid gap-4 px-5 py-4 md:grid-cols-2">
          <Field label="Name">
            <Input value={d.name} onChange={(e) => s.setDraft({ ...d, name: e.target.value })} />
          </Field>
          <Field label="Internal domain" hint={<>Devices are reachable as <Mono>name.{d.domain}</Mono></>}>
            <Input className="font-mono" value={d.domain} onChange={(e) => s.setDraft({ ...d, domain: e.target.value.toLowerCase() })} />
          </Field>
        </div>
        <div className="px-5">
          <ToggleRow title="Give devices IPv6 addresses" description="Each device also gets an address in the private IPv6 range below." checked={d.ipv6_enabled} onChange={(v) => s.setDraft({ ...d, ipv6_enabled: v })} />
        </div>
        <SaveBar {...s} />
      </Panel>

      <Panel>
        <PanelHeader
          title="Address range"
          description={`${data.derived.devices} of ${data.derived.capacity.toLocaleString()} addresses used. The gateway uses ${data.derived.gateway_ipv4}.`}
          actions={
            <Button size="sm" onClick={() => setReaddress(true)}>
              Change range…
            </Button>
          }
        />
        <dl className="grid gap-3 px-5 py-4 text-[13px] md:grid-cols-2">
          <div>
            <dt className="text-ink-3">IPv4</dt>
            <dd className="font-mono">{d.ipv4}</dd>
          </div>
          <div>
            <dt className="text-ink-3">IPv6</dt>
            <dd className="font-mono">{d.ipv6}</dd>
          </div>
        </dl>
        <div className="space-y-4 border-t border-line px-5 py-4">
          <Field label="Reserved addresses" hint="Never handed out automatically. Single addresses or ranges inside the network.">
            <ListEditor values={d.reserved ?? []} onChange={(v) => s.setDraft({ ...d, reserved: v })} placeholder="100.80.0.2/31" />
          </Field>
          <Field label="Address pools" hint="Give devices with a tag, or people in a group, addresses from their own block. Handy for firewall rules outside Gorget.">
            <PoolEditor pools={d.pools ?? {}} onChange={(p) => s.setDraft({ ...d, pools: p })} />
          </Field>
        </div>
        <SaveBar {...s} />
      </Panel>
      <ReaddressDialog open={readdress} onOpenChange={setReaddress} current={d} />
    </div>
  );
}

function PoolEditor({ pools, onChange }: { pools: Record<string, string>; onChange: (p: Record<string, string>) => void }) {
  const [sel, setSel] = useState("");
  const [cidr, setCidr] = useState("");
  return (
    <div className="space-y-2">
      {Object.entries(pools).map(([k, v]) => (
        <div key={k} className="flex items-center gap-3 text-[13px]">
          <Mono className="w-40">{k}</Mono>
          <Mono className="flex-1">{v}</Mono>
          <Button
            size="icon"
            variant="ghost"
            aria-label={`Remove pool ${k}`}
            onClick={() => {
              const n = { ...pools };
              delete n[k];
              onChange(n);
            }}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      <div className="flex gap-2">
        <Input className="font-mono" placeholder="tag:server or group:eng" value={sel} onChange={(e) => setSel(e.target.value)} />
        <Input className="font-mono" placeholder="100.80.1.0/24" value={cidr} onChange={(e) => setCidr(e.target.value)} />
        <Button
          disabled={!sel || !cidr}
          onClick={() => {
            onChange({ ...pools, [sel]: cidr });
            setSel("");
            setCidr("");
          }}
        >
          Add
        </Button>
      </div>
    </div>
  );
}

function ReaddressDialog({ open, onOpenChange, current }: { open: boolean; onOpenChange: (v: boolean) => void; current: Settings["network"] }) {
  const qc = useQueryClient();
  const reauth = useReauth();
  const [v4, setV4] = useState(current.ipv4);
  const [preview, setPreview] = useState<{ name: string; kind: string; old_ipv4: string; new_ipv4: string }[] | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const run = async (dry: boolean): Promise<void> => {
    setBusy(true);
    setErr("");
    try {
      const r = await post<{ devices: { name: string; kind: string; old_ipv4: string; new_ipv4: string }[] }>("/settings/network/readdress", { ipv4: v4, ipv6: current.ipv6, dry_run: dry });
      if (dry) setPreview(r.devices);
      else {
        toast.success("Address range changed. Gorget apps update automatically; re-download WireGuard app configurations.");
        qc.invalidateQueries();
        onOpenChange(false);
      }
    } catch (e) {
      if (e instanceof ApiError && e.code === "reauth_required" && (await reauth())) return run(dry);
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const wg = preview?.filter((p) => p.kind === "wireguard").length ?? 0;
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) setPreview(null);
      }}
      title="Change the address range"
      description="Every device moves to the same position in the new range where possible."
      wide
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          {preview ? (
            <Button variant="danger" loading={busy} onClick={() => run(false)}>
              Move {preview.length} devices
            </Button>
          ) : (
            <Button variant="primary" loading={busy} onClick={() => run(true)}>
              Preview
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-4">
        {err && <ErrorNote>{err}</ErrorNote>}
        <Field label="New IPv4 range" hint="Private (10/8, 172.16/12, 192.168/16) or 100.64.0.0/10, between /8 and /28.">
          <Input
            className="font-mono"
            value={v4}
            onChange={(e) => {
              setV4(e.target.value);
              setPreview(null);
            }}
          />
        </Field>
        {preview && (
          <>
            {wg > 0 && <Note tone="warn">{wg} WireGuard app configuration(s) must be downloaded and imported again after the change.</Note>}
            <div className="max-h-72 overflow-y-auto rounded-lg border border-line">
              <Table>
                <thead>
                  <tr>
                    <Th>Device</Th>
                    <Th>Now</Th>
                    <Th>After</Th>
                  </tr>
                </thead>
                <tbody>
                  {preview.map((p) => (
                    <tr key={p.name}>
                      <Td>{p.name}</Td>
                      <Td>
                        <Mono>{p.old_ipv4}</Mono>
                      </Td>
                      <Td>
                        <Mono>{p.new_ipv4}</Mono>
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </div>
          </>
        )}
      </div>
    </Dialog>
  );
}

function DevicesTab({ data }: { data: SettingsResponse }) {
  const s = useSection("devices", data);
  if (!s.draft) return null;
  const d = s.draft;
  return (
    <Panel>
      <PanelHeader title="Device lifecycle" />
      <div className="divide-y divide-line px-5">
        <ToggleRow title="Approve new devices" description="Devices people add wait for an admin before they can connect. Setup keys can still auto-approve." checked={d.approval_required} onChange={(v) => s.setDraft({ ...d, approval_required: v })} />
      </div>
      <div className="grid gap-4 border-t border-line px-5 py-4 md:grid-cols-3">
        <Field label="Sign in again every (days)" hint="0 = never. Tagged devices never expire.">
          <Input type="number" min={0} value={d.key_expiry_days} onChange={(e) => s.setDraft({ ...d, key_expiry_days: Number(e.target.value) })} />
        </Field>
        <Field label="Remove ephemeral devices after offline (minutes)">
          <Input type="number" min={1} value={d.ephemeral_timeout_mins} onChange={(e) => s.setDraft({ ...d, ephemeral_timeout_mins: Number(e.target.value) })} />
        </Field>
        <Field label="Remove inactive devices after (days)" hint="0 = keep forever.">
          <Input type="number" min={0} value={d.inactive_cleanup_days} onChange={(e) => s.setDraft({ ...d, inactive_cleanup_days: Number(e.target.value) })} />
        </Field>
      </div>
      <SaveBar {...s} />
    </Panel>
  );
}

function ClientsTab({ data }: { data: SettingsResponse }) {
  const s = useSection("client", data);
  const devices = useQuery({ queryKey: ["routes"], queryFn: () => get<{ exit_nodes: { device_id: string; device_name: string; approved: boolean }[] }>("/routes") });
  if (!s.draft) return null;
  const d = s.draft;
  const exits = (devices.data?.exit_nodes ?? []).filter((x) => x.approved);
  return (
    <Panel>
      <PanelHeader title="Gorget app defaults" description="Applied to the Gorget apps on Android, Windows, macOS and Linux." />
      <div className="divide-y divide-line px-5">
        <ToggleRow title="Allow local network access by default" description="When using an exit node, people can still reach printers and devices on their own Wi-Fi." checked={d.allow_lan_default} onChange={(v) => s.setDraft({ ...d, allow_lan_default: v })} />
        <ToggleRow title="Enforce the kill switch" description="When connected through an exit node, block all traffic if the tunnel drops. People can't turn it off." checked={d.kill_switch_enforced} onChange={(v) => s.setDraft({ ...d, kill_switch_enforced: v })} />
        <ToggleRow title="Let people choose exit nodes" checked={d.allow_user_exit_choice} onChange={(v) => s.setDraft({ ...d, allow_user_exit_choice: v })} />
        <ToggleRow title="Let people use their own DNS" checked={d.allow_custom_dns} onChange={(v) => s.setDraft({ ...d, allow_custom_dns: v })} />
      </div>
      <div className="grid gap-4 border-t border-line px-5 py-4 md:grid-cols-3">
        <Field label="Suggested exit node">
          <Select value={d.default_exit_node_id} onChange={(e) => s.setDraft({ ...d, default_exit_node_id: e.target.value })}>
            <option value="">None</option>
            {exits.map((x) => (
              <option key={x.device_id} value={x.device_id}>
                {x.device_name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Required exit node" hint="All internet traffic goes through it.">
          <Select value={d.forced_exit_node_id} onChange={(e) => s.setDraft({ ...d, forced_exit_node_id: e.target.value })}>
            <option value="">Not required</option>
            {exits.map((x) => (
              <option key={x.device_id} value={x.device_id}>
                {x.device_name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Tunnel MTU" hint="1280 works on every network; raise to 1420 for more speed.">
          <Input type="number" min={1280} max={1500} value={d.mtu} onChange={(e) => s.setDraft({ ...d, mtu: Number(e.target.value) })} />
        </Field>
      </div>
      <SaveBar {...s} />
    </Panel>
  );
}

function GatewayTab({ data }: { data: SettingsResponse }) {
  const s = useSection("gateway", data);
  if (!s.draft) return null;
  const d = s.draft;
  return (
    <Panel>
      <PanelHeader
        title="WireGuard gateway"
        description={
          data.server.gateway_enabled ? (
            <>
              Standard WireGuard apps connect to <Mono>{data.server.gateway_endpoint}</Mono>. Open this UDP port in your firewall.
            </>
          ) : (
            "Disabled in the server configuration (gateway.enabled)."
          )
        }
      />
      <div className="divide-y divide-line px-5">
        <ToggleRow title="Offer the gateway as an exit node" description="Gorget apps can send internet traffic through this server." checked={d.exit_node} onChange={(v) => s.setDraft({ ...d, exit_node: v })} />
      </div>
      <div className="grid gap-4 border-t border-line px-5 py-4 md:grid-cols-3">
        <Field label="Default routing for new apps">
          <Select value={d.default_tunnel_mode} onChange={(e) => s.setDraft({ ...d, default_tunnel_mode: e.target.value })}>
            <option value="split">Private network only (recommended)</option>
            <option value="full">All traffic</option>
          </Select>
        </Field>
        <Field label="New apps stop working after (days)" hint="0 = no limit.">
          <Input type="number" min={0} value={d.default_expiry_days} onChange={(e) => s.setDraft({ ...d, default_expiry_days: Number(e.target.value) })} />
        </Field>
        <Field label="Keepalive (seconds)" hint="Keeps connections open behind NAT. 0 disables.">
          <Input type="number" min={0} value={d.persistent_keepalive} onChange={(e) => s.setDraft({ ...d, persistent_keepalive: Number(e.target.value) })} />
        </Field>
        <Field label="Extra ranges for 'private network only'" className="md:col-span-3" hint="Added to split-routing configurations, e.g. a datacenter range reached via the server.">
          <ListEditor values={d.split_routes ?? []} onChange={(v) => s.setDraft({ ...d, split_routes: v })} placeholder="10.10.0.0/16" />
        </Field>
      </div>
      <SaveBar {...s} />
    </Panel>
  );
}

function SignInTab({ data, canEdit }: { data: SettingsResponse; canEdit: boolean }) {
  const qc = useQueryClient();
  const [d, setD] = useState(data.settings.auth);
  const [busy, setBusy] = useState(false);
  useEffect(() => setD(data.settings.auth), [data]);
  const dirty = JSON.stringify(d) !== JSON.stringify(data.settings.auth);
  return (
    <Panel>
      <PanelHeader title="Sign-in & permissions" />
      <div className="divide-y divide-line px-5">
        <ToggleRow title="Allow password sign-in" description="Turn off to require single sign-on. Owners can always sign in with a password as a recovery path." checked={d.password_login} disabled={!canEdit} onChange={(v) => setD({ ...d, password_login: v })} />
        <ToggleRow title="Require two-factor sign-in for everyone" description="People without it are asked to set it up before they can do anything else." checked={d.mfa_required} disabled={!canEdit} onChange={(v) => setD({ ...d, mfa_required: v })} />
        <ToggleRow title="Require two-factor sign-in for admins" checked={d.mfa_required_admins} disabled={!canEdit} onChange={(v) => setD({ ...d, mfa_required_admins: v })} />
        <ToggleRow title="Members can add WireGuard apps" checked={d.user_wg_configs} disabled={!canEdit} onChange={(v) => setD({ ...d, user_wg_configs: v })} />
        <ToggleRow title="Members can create setup keys" description="Only for their own devices, or tags they own." checked={d.user_setup_keys} disabled={!canEdit} onChange={(v) => setD({ ...d, user_setup_keys: v })} />
      </div>
      <div className="grid gap-4 border-t border-line px-5 py-4 md:grid-cols-2">
        <Field label="Lock account after failed attempts" hint="0 disables locking.">
          <Input type="number" min={0} disabled={!canEdit} value={d.lockout_threshold} onChange={(e) => setD({ ...d, lockout_threshold: Number(e.target.value) })} />
        </Field>
        <Field label="Lock for (minutes)">
          <Input type="number" min={1} disabled={!canEdit} value={d.lockout_minutes} onChange={(e) => setD({ ...d, lockout_minutes: Number(e.target.value) })} />
        </Field>
      </div>
      {canEdit && (
        <div className="border-t border-line px-5 py-3">
          <Button
            variant="primary"
            disabled={!dirty}
            loading={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await put("/settings/auth", d);
                toast.success("Settings saved");
                qc.invalidateQueries({ queryKey: ["settings"] });
              } catch (e) {
                toast.error(errMessage(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            Save changes
          </Button>
        </div>
      )}
    </Panel>
  );
}

function SSOTab() {
  const { can } = useSession();
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["sso-providers"], queryFn: () => get<{ providers: OIDCProvider[]; redirect_url: string }>("/sso-providers") });
  const [edit, setEdit] = useState<(Partial<OIDCProvider> & { client_secret?: string }) | null>(null);
  const [err, setErr] = useState("");
  const [test, setTest] = useState<{ ok: boolean; error?: string } | null>(null);
  const manage = can("manage_sys");
  return (
    <Panel>
      <PanelHeader
        title="Single sign-on (OpenID Connect)"
        description="Let people sign in with Keycloak, Authentik, Zitadel, Google, Microsoft Entra ID, GitHub-compatible providers and more."
        actions={
          manage && (
            <Button size="sm" variant="primary" onClick={() => (setEdit({ enabled: true, auto_create_users: true, sync_groups: true, default_role: "user", groups_claim: "groups", scopes: [], allowed_domains: [] }), setErr(""), setTest(null))}>
              <Plus /> Add provider
            </Button>
          )
        }
      />
      {data && (
        <div className="flex items-center gap-2 border-b border-line px-5 py-3 text-xs text-ink-3">
          Redirect URL to register with your provider: <Mono className="text-ink">{data.redirect_url}</Mono>
          <CopyButton value={data.redirect_url} />
        </div>
      )}
      {!data?.providers.length ? (
        <p className="px-5 py-6 text-[13px] text-ink-3">No providers configured.</p>
      ) : (
        <ul className="divide-y divide-line">
          {data.providers.map((p) => (
            <li key={p.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-[13px]">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 font-medium">
                  {p.name} {!p.enabled && <Badge>Off</Badge>}
                </div>
                <div className="truncate font-mono text-xs text-ink-3">{p.issuer}</div>
              </div>
              <span className="text-xs text-ink-3">
                New people: {p.auto_create_users ? roleLabel[p.default_role] : "invite only"}
                {p.allowed_domains.length > 0 && ` · ${p.allowed_domains.join(", ")}`}
              </span>
              {manage && (
                <>
                  <Button size="icon" variant="ghost" aria-label={`Edit ${p.name}`} onClick={() => (setEdit(p), setErr(""), setTest(null))}>
                    <Pencil />
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={`Delete ${p.name}`}
                    onClick={async () => {
                      if (!(await confirmAction({ title: `Remove ${p.name}?`, description: "People who sign in only with it will need another way in.", confirm: "Remove provider", danger: true }))) return;
                      await del(`/sso-providers/${p.id}`);
                      qc.invalidateQueries({ queryKey: ["sso-providers"] });
                    }}
                  >
                    <Trash2 />
                  </Button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      <Dialog
        open={!!edit}
        onOpenChange={(o) => !o && setEdit(null)}
        title={edit?.id ? `Edit ${edit.name}` : "Add a sign-in provider"}
        wide
        footer={
          <>
            <Button
              onClick={async () => {
                try {
                  setTest(await post("/sso-providers/test", { issuer: edit?.issuer }));
                } catch (e) {
                  setTest({ ok: false, error: errMessage(e) });
                }
              }}
              disabled={!edit?.issuer}
            >
              Test discovery
            </Button>
            <Button
              variant="primary"
              onClick={async () => {
                if (!edit) return;
                setErr("");
                try {
                  const body = {
                    name: edit.name,
                    issuer: edit.issuer,
                    client_id: edit.client_id,
                    client_secret: edit.client_secret || undefined,
                    scopes: edit.scopes,
                    groups_claim: edit.groups_claim,
                    allowed_domains: edit.allowed_domains,
                    auto_create_users: edit.auto_create_users,
                    default_role: edit.default_role,
                    sync_groups: edit.sync_groups,
                    enabled: edit.enabled,
                  };
                  if (edit.id) await patch(`/sso-providers/${edit.id}`, body);
                  else await post("/sso-providers", body);
                  toast.success("Provider saved");
                  setEdit(null);
                  qc.invalidateQueries({ queryKey: ["sso-providers"] });
                } catch (e) {
                  setErr(errMessage(e));
                }
              }}
            >
              Save provider
            </Button>
          </>
        }
      >
        {edit && (
          <div className="space-y-4">
            {err && <ErrorNote>{err}</ErrorNote>}
            {test && (test.ok ? <Note>Discovery succeeded: the issuer is reachable.</Note> : <ErrorNote>Discovery failed: {test.error}</ErrorNote>)}
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="Button label">
                <Input value={edit.name ?? ""} placeholder="Company login" onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
              </Field>
              <Field label="Issuer URL">
                <Input className="font-mono" value={edit.issuer ?? ""} placeholder="https://auth.example.com/realms/main" onChange={(e) => setEdit({ ...edit, issuer: e.target.value })} />
              </Field>
              <Field label="Client ID">
                <Input className="font-mono" value={edit.client_id ?? ""} onChange={(e) => setEdit({ ...edit, client_id: e.target.value })} />
              </Field>
              <Field label="Client secret" hint={edit.id ? "Leave empty to keep the current secret." : "Stored encrypted."}>
                <Input type="password" value={edit.client_secret ?? ""} onChange={(e) => setEdit({ ...edit, client_secret: e.target.value })} />
              </Field>
              <Field label="Groups claim" hint="Claim listing the person's groups; synced into Gorget groups.">
                <Input className="font-mono" value={edit.groups_claim ?? ""} onChange={(e) => setEdit({ ...edit, groups_claim: e.target.value })} />
              </Field>
              <Field label="Role for new people">
                <Select value={edit.default_role} onChange={(e) => setEdit({ ...edit, default_role: e.target.value as Role })}>
                  {(["user", "auditor", "network_admin", "admin"] as Role[]).map((r) => (
                    <option key={r} value={r}>
                      {roleLabel[r]}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            <Field label="Allowed email domains" hint="Empty allows any verified email.">
              <ListEditor values={edit.allowed_domains ?? []} onChange={(v) => setEdit({ ...edit, allowed_domains: v })} placeholder="example.com" />
            </Field>
            <Field label="Extra scopes" hint="openid, email and profile are always requested.">
              <ListEditor values={edit.scopes ?? []} onChange={(v) => setEdit({ ...edit, scopes: v })} placeholder="groups" />
            </Field>
            <div className="space-y-2">
              <Checkbox checked={!!edit.auto_create_users} onChange={(v) => setEdit({ ...edit, auto_create_users: v })} label="Create accounts automatically on first sign-in" />
              <Checkbox checked={!!edit.sync_groups} onChange={(v) => setEdit({ ...edit, sync_groups: v })} label="Sync groups from the provider" />
              <Checkbox checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label="Show on the sign-in page" />
            </div>
          </div>
        )}
      </Dialog>
    </Panel>
  );
}

function WebhooksTab() {
  const { can } = useSession();
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["webhooks"], queryFn: () => get<{ webhooks: Webhook[]; events: string[] }>("/webhooks") });
  const [edit, setEdit] = useState<Partial<Webhook> | null>(null);
  const [secret, setSecret] = useState("");
  const manage = can("manage_sys");
  return (
    <Panel>
      <PanelHeader
        title="Webhooks"
        description="Get notified in chat or automation when devices join, need approval, or rules change. Deliveries are signed (X-Gorget-Signature, HMAC-SHA256)."
        actions={
          manage && (
            <Button size="sm" variant="primary" onClick={() => setEdit({ events: [], enabled: true })}>
              <Plus /> Add webhook
            </Button>
          )
        }
      />
      {!data?.webhooks.length ? (
        <p className="px-5 py-6 text-[13px] text-ink-3">No webhooks.</p>
      ) : (
        <ul className="divide-y divide-line">
          {data.webhooks.map((h) => (
            <li key={h.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-[13px]">
              {h.last_delivery_at ? h.last_status >= 200 && h.last_status < 300 ? <CheckCircle2 className="size-4 text-verdigris" /> : <XCircle className="size-4 text-oxide" /> : <span className="size-4" />}
              <div className="min-w-0 flex-1">
                <div className="font-medium">{h.name || h.url}</div>
                <div className="truncate text-xs text-ink-3">
                  <span className="font-mono">{h.url}</span> · {h.events.length ? `${h.events.length} events` : "all events"}
                  {h.last_delivery_at > 0 && ` · last delivery ${relTime(h.last_delivery_at)}${h.last_error ? ` (${h.last_error})` : ""}`}
                </div>
              </div>
              {!h.enabled && <Badge>Off</Badge>}
              {manage && (
                <>
                  <Button
                    size="sm"
                    onClick={async () => {
                      const r = await post<{ ok: boolean; error: string; status: number }>(`/webhooks/${h.id}/test`);
                      if (r.ok) toast.success("Test delivered");
                      else toast.error(`Test failed: ${r.error || r.status}`);
                      qc.invalidateQueries({ queryKey: ["webhooks"] });
                    }}
                  >
                    <Send /> Test
                  </Button>
                  <Button size="icon" variant="ghost" aria-label="Edit webhook" onClick={() => setEdit(h)}>
                    <Pencil />
                  </Button>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label="Delete webhook"
                    onClick={async () => {
                      if (!(await confirmAction({ title: "Delete this webhook?", confirm: "Delete", danger: true }))) return;
                      await del(`/webhooks/${h.id}`);
                      qc.invalidateQueries({ queryKey: ["webhooks"] });
                    }}
                  >
                    <Trash2 />
                  </Button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      <Dialog
        open={!!edit}
        onOpenChange={(o) => !o && setEdit(null)}
        title={edit?.id ? "Edit webhook" : "Add webhook"}
        footer={
          <Button
            variant="primary"
            disabled={!edit?.url}
            onClick={async () => {
              if (!edit) return;
              try {
                const body = { name: edit.name ?? "", url: edit.url, events: edit.events ?? [], enabled: edit.enabled ?? true };
                if (edit.id) await patch(`/webhooks/${edit.id}`, body);
                else setSecret((await post<{ secret: string }>("/webhooks", body)).secret);
                setEdit(null);
                qc.invalidateQueries({ queryKey: ["webhooks"] });
              } catch (e) {
                toast.error(errMessage(e));
              }
            }}
          >
            Save webhook
          </Button>
        }
      >
        {edit && (
          <div className="space-y-4">
            <Field label="Name">
              <Input value={edit.name ?? ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} />
            </Field>
            <Field label="URL">
              <Input className="font-mono" value={edit.url ?? ""} placeholder="https://hooks.example.com/gorget" onChange={(e) => setEdit({ ...edit, url: e.target.value })} />
            </Field>
            <fieldset>
              <legend className="mb-2 text-[13px] font-medium">Events (none selected = all)</legend>
              <div className="grid gap-1.5 sm:grid-cols-2">
                {data?.events.map((ev) => (
                  <Checkbox
                    key={ev}
                    checked={edit.events?.includes(ev) ?? false}
                    onChange={(v) => setEdit({ ...edit, events: v ? [...(edit.events ?? []), ev] : (edit.events ?? []).filter((x) => x !== ev) })}
                    label={<span className="font-mono text-xs">{ev}</span>}
                  />
                ))}
              </div>
            </fieldset>
            <Checkbox checked={edit.enabled ?? true} onChange={(v) => setEdit({ ...edit, enabled: v })} label="Enabled" />
          </div>
        )}
      </Dialog>
      <Dialog open={!!secret} onOpenChange={(o) => !o && setSecret("")} title="Signing secret" description="Verify deliveries with this secret. It won't be shown again.">
        <SecretBox value={secret} caption="Signature: hex(HMAC-SHA256(secret, timestamp + '.' + body)), sent as t=<timestamp>,v1=<signature>." />
      </Dialog>
    </Panel>
  );
}

function SystemTab() {
  const { data } = useQuery({
    queryKey: ["system"],
    queryFn: () =>
      get<{
        version: string;
        database: string;
        tls: { mode: string; domains: string[] | null; issuer: string; not_after: string; error?: string; staging: boolean };
        gateway: GatewayStatus;
        relay: { enabled: boolean; connections?: number; bytes?: number; dropped?: number };
        stun: { enabled: boolean; requests?: number };
        online_devices: number;
        cluster?: { enabled: boolean; instance_id: string; leader: boolean; instances: ClusterInstance[] };
        udp_relay?: boolean;
      }>("/system/status"),
    refetchInterval: 15_000,
  });
  if (!data) return <Skeleton className="h-64" />;
  const certOk = data.tls.mode === "off" || (!!data.tls.issuer && !data.tls.error);
  const notAfter = data.tls.not_after && !data.tls.not_after.startsWith("0001") ? new Date(data.tls.not_after) : null;
  return (
    <div className="grid gap-6 md:grid-cols-2">
      <StatusCard title="HTTPS certificate" ok={certOk}>
        <KV k="Mode" v={<Mono>{data.tls.mode}{data.tls.staging && " (staging)"}</Mono>} />
        <KV k="Domains" v={<Mono>{data.tls.domains?.join(", ") || "—"}</Mono>} />
        <KV k="Issuer" v={data.tls.issuer || (data.tls.mode === "off" ? "Handled by your reverse proxy" : "Obtaining…")} />
        <KV k="Valid until" v={notAfter ? `${fmtDate(notAfter.getTime() / 1000)} (${relTime(notAfter.getTime() / 1000)})` : "—"} />
        {data.tls.error && <p className="text-xs text-oxide">{data.tls.error}</p>}
      </StatusCard>
      <StatusCard title="WireGuard gateway" ok={!data.gateway.enabled || data.gateway.running}>
        <KV k="State" v={data.gateway.running ? `Running (${data.gateway.backend})` : data.gateway.enabled ? "Not running" : "Disabled"} />
        <KV k="Interface" v={<Mono>{data.gateway.interface}</Mono>} />
        <KV k="Endpoint" v={<Mono>{data.gateway.endpoint}</Mono>} />
        <KV k="Peers" v={data.gateway.peers} />
        <KV k="Public key" v={<Mono className="text-xs">{data.gateway.public_key.slice(0, 20)}…</Mono>} />
        {data.gateway.error && <p className="text-xs text-oxide">{data.gateway.error}</p>}
      </StatusCard>
      <StatusCard title="Relay & NAT traversal" ok>
        <KV k="Relay" v={data.relay.enabled ? `${data.relay.connections} sessions` : "Disabled"} />
        <KV k="STUN" v={data.stun.enabled ? `${data.stun.requests} requests answered` : "Disabled"} />
        <KV k="Connected Gorget apps" v={data.online_devices} />
      </StatusCard>
      <ClusterCard cluster={data.cluster} udpRelay={data.udp_relay} />
      <StatusCard title="Server" ok>
        <KV k="Version" v={<Mono>{data.version}</Mono>} />
        <KV k="Database" v={data.database === "sqlite" ? "SQLite" : "PostgreSQL"} />
        <KV k="Telemetry" v="None. Nothing leaves this server." />
      </StatusCard>
    </div>
  );
}

function StatusCard({ title, ok, children }: { title: string; ok: boolean; children: React.ReactNode }) {
  return (
    <Panel>
      <PanelHeader title={<span className="flex items-center gap-2">{ok ? <CheckCircle2 className="size-4 text-verdigris" /> : <XCircle className="size-4 text-oxide" />}{title}</span>} />
      <dl className="space-y-2 px-5 py-4 text-[13px]">{children}</dl>
    </Panel>
  );
}

function KV({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <dt className="text-ink-3">{k}</dt>
      <dd className="min-w-0 truncate text-right">{v}</dd>
    </div>
  );
}
