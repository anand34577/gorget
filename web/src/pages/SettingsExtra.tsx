import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Globe2, HeartPulse, Plus, Server, ShieldCheck, Trash2, XCircle } from "lucide-react";
import { toast } from "sonner";
import { del, errMessage, get, post } from "@/lib/api";
import type { ClusterInstance, GeoStatus, Posture, SettingsResponse } from "@/lib/types";
import { useSession } from "@/lib/session";
import { countryName, fmtDate, relTime } from "@/lib/utils";
import { Badge, Button, Checkbox, confirmAction, ErrorNote, Field, Input, ListEditor, Mono, Note, Panel, PanelHeader, SecretBox, Select, Skeleton, ToggleRow } from "@/components/ui";
import { SaveBar, useSection } from "./Settings";

const osNames: [string, string][] = [
  ["windows", "Windows"],
  ["darwin", "macOS"],
  ["linux", "Linux"],
  ["android", "Android"],
];

/** Device health: rules that devices must meet to stay on the network. */
export function HealthTab({ data }: { data: SettingsResponse }) {
  const s = useSection("posture", data);
  if (!s.draft) return null;
  const p: Posture = s.draft;
  const set = (patch: Partial<Posture>) => s.setDraft({ ...p, ...patch });
  const minOS = p.min_os_version ?? {};
  const setOS = (os: string, v: string) => {
    const next = { ...minOS };
    if (v.trim()) next[os] = v.trim();
    else delete next[os];
    set({ min_os_version: next });
  };
  return (
    <div className="space-y-6">
      <Panel>
        <PanelHeader
          title={
            <span className="flex items-center gap-2">
              <HeartPulse className="size-4 text-ink-3" /> Device health
            </span>
          }
          description="Each Gorget app reports its operating system version, app version, disk encryption, firewall and where it connects from. A device that breaks a rule is blocked (or only flagged) until it is fixed."
        />
        <div className="divide-y divide-line px-5">
          <ToggleRow title="Check device health" description="Turn the rules below on. Servers and other tagged devices can be exempted." checked={p.enabled} onChange={(v) => set({ enabled: v })} />
        </div>
        <fieldset disabled={!p.enabled} className="space-y-5 border-t border-line px-5 py-4 disabled:opacity-60">
          <Field label="When a device breaks a rule" hint="Report only is a safe way to see who would be affected before you enforce.">
            <Select value={p.mode} onChange={(e) => set({ mode: e.target.value as Posture["mode"] })}>
              <option value="enforce">Block it from the network until it is fixed</option>
              <option value="report">Only flag it in the console</option>
            </Select>
          </Field>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Oldest allowed Gorget app version" hint="For example 0.3.0. Leave empty to allow any version.">
              <Input value={p.min_client_version} placeholder="0.3.0" className="font-mono" onChange={(e) => set({ min_client_version: e.target.value })} />
            </Field>
          </div>
          <div>
            <div className="mb-2 text-[13px] font-medium">Oldest allowed operating system</div>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              {osNames.map(([os, label]) => (
                <Field key={os} label={label} hint={os === "windows" ? "e.g. 10.0.19045" : os === "darwin" ? "e.g. 13" : os === "android" ? "e.g. 10" : "e.g. 6.1"}>
                  <Input value={minOS[os] ?? ""} className="font-mono" onChange={(e) => setOS(os, e.target.value)} />
                </Field>
              ))}
            </div>
          </div>
          <div className="divide-y divide-line rounded-lg border border-line px-4">
            <ToggleRow title="Require disk encryption" description="BitLocker, FileVault or LUKS must be on. Android always reports encryption as on." checked={p.require_disk_encryption} onChange={(v) => set({ require_disk_encryption: v })} />
            <ToggleRow title="Require the system firewall" description="Windows Defender Firewall, the macOS firewall, or ufw / firewalld / nftables on Linux." checked={p.require_firewall} onChange={(v) => set({ require_firewall: v })} />
          </div>
          <div>
            <div className="mb-1 text-[13px] font-medium">Allowed operating systems</div>
            <p className="mb-2 text-xs text-ink-3">Leave all unticked to allow every system.</p>
            <div className="flex flex-wrap gap-x-6 gap-y-2">
              {osNames.map(([os, label]) => (
                <Checkbox
                  key={os}
                  label={label}
                  checked={(p.allowed_os ?? []).includes(os)}
                  onChange={(v) => set({ allowed_os: v ? [...(p.allowed_os ?? []), os] : (p.allowed_os ?? []).filter((x) => x !== os) })}
                />
              ))}
            </div>
          </div>
          <Field label="Only allow connections from these networks" hint="Public addresses or ranges, for example your office: 203.0.113.0/24. Empty allows any.">
            <ListEditor values={p.allowed_networks ?? []} onChange={(v) => set({ allowed_networks: v })} placeholder="203.0.113.0/24" />
          </Field>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Only allow these countries" hint="Two-letter codes such as IN, DE, US. A device whose location can't be determined is then blocked too. Empty allows any.">
              <ListEditor values={p.allowed_countries ?? []} onChange={(v) => set({ allowed_countries: v.map((c) => c.toUpperCase()) })} placeholder="IN" />
            </Field>
            <Field label="Block these countries" hint="Connections from these countries are refused.">
              <ListEditor values={p.blocked_countries ?? []} onChange={(v) => set({ blocked_countries: v.map((c) => c.toUpperCase()) })} placeholder="KP" />
            </Field>
          </div>
          {[...(p.allowed_countries ?? []), ...(p.blocked_countries ?? [])].length > 0 && (
            <p className="-mt-2 text-xs text-ink-3">
              {[...(p.allowed_countries ?? []), ...(p.blocked_countries ?? [])].map((c) => `${c} = ${countryName(c)}`).join(" · ")}
            </p>
          )}
          <GeoDatabase autoUpdate={p.geo_auto_update} setAutoUpdate={(v) => set({ geo_auto_update: v })} />
          <Field label="Exempt devices with these tags" hint="Servers and IoT devices often cannot report health.">
            <ListEditor values={p.exempt_tags ?? []} onChange={(v) => set({ exempt_tags: v })} placeholder="tag:server" />
          </Field>
          <Note>Devices only report what their app can detect; a modified client could report false values, so treat these rules as hygiene, not as a security boundary against a hostile device.</Note>
        </fieldset>
        <SaveBar {...s} />
      </Panel>
    </div>
  );
}

/** GeoDatabase shows the country database used by country rules and lets admins fetch it. */
function GeoDatabase({ autoUpdate, setAutoUpdate }: { autoUpdate: boolean; setAutoUpdate: (v: boolean) => void }) {
  const qc = useQueryClient();
  const { can } = useSession();
  const q = useQuery({ queryKey: ["geoip"], queryFn: () => get<GeoStatus>("/geoip") });
  const [busy, setBusy] = useState(false);
  const db = q.data?.database;
  const download = async () => {
    setBusy(true);
    try {
      await post("/geoip/update");
      toast.success("Country database downloaded");
      qc.invalidateQueries({ queryKey: ["geoip"] });
      qc.invalidateQueries({ queryKey: ["devices"] });
    } catch (e) {
      toast.error(errMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="rounded-lg border border-line p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-[13px] font-medium">
            <Globe2 className="size-4 text-ink-3" /> Country database
            {db?.loaded ? <Badge tone="ok">Loaded</Badge> : <Badge tone="warn">Not downloaded</Badge>}
          </div>
          <p className="mt-1 max-w-xl text-xs text-ink-2">
            Countries are looked up on this server from a local file; no addresses are sent anywhere. The free DB-IP Lite database (about 8 MB, updated monthly) works out of the box, or put your own MaxMind-format <Mono className="text-[11px]">.mmdb</Mono> file in the <Mono className="text-[11px]">geoip</Mono> folder of the data directory.
          </p>
          {db?.loaded && (
            <p className="mt-1 text-xs text-ink-3">
              {db.file} · built {db.built_at ? relTime(db.built_at) : "at an unknown date"}
              {db.source ? ` · ${db.source}` : ""}
            </p>
          )}
        </div>
        {can("manage_net") && (
          <Button type="button" size="sm" loading={busy} onClick={download}>
            {db?.loaded ? "Update now" : "Download now"}
          </Button>
        )}
      </div>
      <div className="mt-2">
        <Checkbox checked={autoUpdate} onChange={setAutoUpdate} label="Keep it up to date automatically (checked daily, downloaded monthly)" />
      </div>
    </div>
  );
}

/** Domain routing: send traffic for named sites through a chosen exit node. */
export function RoutingTab({ data }: { data: SettingsResponse }) {
  const s = useSection("routing", data);
  const exits = useQuery({ queryKey: ["routes"], queryFn: () => get<{ exit_nodes: { device_id: string; device_name: string; approved: boolean }[] }>("/routes") });
  if (!s.draft) return null;
  const routes = s.draft.domain_routes ?? [];
  const approved = (exits.data?.exit_nodes ?? []).filter((e) => e.approved);
  const setRoutes = (next: { domain: string; via: string }[]) => s.setDraft({ ...s.draft!, domain_routes: next });
  return (
    <Panel>
      <PanelHeader
        title={
          <span className="flex items-center gap-2">
            <Globe2 className="size-4 text-ink-3" /> Domain routing
          </span>
        }
        description="Send traffic for specific websites or services through one exit node while everything else keeps its normal route. Useful for services that only accept your office or server address."
        actions={
          approved.length > 0 && (
            <Button size="sm" onClick={() => setRoutes([...routes, { domain: "", via: approved[0].device_name }])}>
              <Plus /> Add domain
            </Button>
          )
        }
      />
      {approved.length === 0 ? (
        <div className="px-5 py-5">
          <Note tone="warn">No exit node is approved yet. Approve one under Routes &amp; exit nodes first: domain routes use an exit node to carry the traffic.</Note>
        </div>
      ) : routes.length === 0 ? (
        <p className="px-5 py-6 text-[13px] text-ink-3">No domain routes. Add a domain such as <span className="font-mono">example.com</span>: it and all its subdomains go through the exit node you choose.</p>
      ) : (
        <ul className="divide-y divide-line">
          {routes.map((r, i) => (
            <li key={i} className="grid items-center gap-3 px-5 py-3 sm:grid-cols-[1fr_auto_1fr_auto]">
              <Input value={r.domain} placeholder="example.com" className="font-mono" onChange={(e) => setRoutes(routes.map((x, j) => (j === i ? { ...x, domain: e.target.value } : x)))} aria-label="Domain" />
              <span className="hidden text-ink-3 sm:block">through</span>
              <Select value={r.via} onChange={(e) => setRoutes(routes.map((x, j) => (j === i ? { ...x, via: e.target.value } : x)))} aria-label="Exit node">
                {approved.map((e) => (
                  <option key={e.device_id} value={e.device_name}>
                    {e.device_name}
                  </option>
                ))}
              </Select>
              <Button variant="ghost" size="icon" aria-label="Remove" onClick={() => setRoutes(routes.filter((_, j) => j !== i))}>
                <Trash2 />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="border-t border-line px-5 py-3 text-xs text-ink-3">Devices need “Use Gorget DNS” turned on (the default) and access to the exit node in your access rules. New addresses start working a moment after the first lookup.</div>
      <SaveBar {...s} />
    </Panel>
  );
}

/** Identity-provider provisioning (SCIM 2.0). */
export function ProvisioningTab() {
  const qc = useQueryClient();
  const { can } = useSession();
  const st = useQuery({ queryKey: ["scim"], queryFn: () => get<{ enabled: boolean; created_at: number; base_url: string }>("/scim") });
  const [token, setToken] = useState<{ token: string; base_url: string } | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  if (!st.data) return <Skeleton className="h-48" />;
  const canEdit = can("manage_sys");
  const rotate = async () => {
    setBusy(true);
    setErr("");
    try {
      setToken(await post<{ token: string; base_url: string }>("/scim/token"));
      qc.invalidateQueries({ queryKey: ["scim"] });
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const disable = async () => {
    if (!(await confirmAction({ title: "Turn off provisioning?", description: "Your identity provider can no longer create, update or remove people here. Existing people stay.", confirm: "Turn off", danger: true }))) return;
    try {
      await del("/scim");
      setToken(null);
      toast.success("Provisioning is off");
      qc.invalidateQueries({ queryKey: ["scim"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };
  return (
    <Panel>
      <PanelHeader
        title={
          <span className="flex items-center gap-2">
            <ShieldCheck className="size-4 text-ink-3" /> Automatic provisioning (SCIM)
          </span>
        }
        description="Let Okta, Microsoft Entra ID, Keycloak or another identity provider create people and groups here, and deactivate them when they leave."
        actions={st.data.enabled ? <Badge tone="ok">On</Badge> : <Badge>Off</Badge>}
      />
      <div className="space-y-4 px-5 py-4 text-[13px]">
        <div>
          <div className="mb-1 text-ink-3">SCIM base URL</div>
          <div className="flex items-center gap-2 rounded-md border border-line bg-sunken px-3 py-2">
            <Mono className="flex-1 truncate">{st.data.base_url}</Mono>
          </div>
        </div>
        {token && <SecretBox value={token.token} caption="Bearer token. Copy it now: it is shown only once." />}
        {err && <ErrorNote>{err}</ErrorNote>}
        {st.data.enabled && !token && <p className="text-ink-3">A token was created {fmtDate(st.data.created_at)}. Create a new one to replace it.</p>}
        <p className="text-ink-3">
          People provisioned this way have no password; they sign in through your single sign-on provider, which links to them by email address. Groups created by SCIM are managed by your identity provider.
        </p>
        {canEdit && (
          <div className="flex gap-2">
            <Button variant="primary" loading={busy} onClick={rotate}>
              {st.data.enabled ? "Create a new token" : "Turn on and create a token"}
            </Button>
            {st.data.enabled && (
              <Button variant="danger-ghost" onClick={disable}>
                Turn off
              </Button>
            )}
          </div>
        )}
      </div>
    </Panel>
  );
}

/** Running instances (cluster mode) and relay transports. */
export function ClusterCard({ cluster, udpRelay }: { cluster?: { enabled: boolean; instance_id: string; leader: boolean; instances: ClusterInstance[] }; udpRelay?: boolean }) {
  return (
    <Panel>
      <PanelHeader
        title={
          <span className="flex items-center gap-2">
            {cluster?.enabled ? <CheckCircle2 className="size-4 text-verdigris" /> : <Server className="size-4 text-ink-3" />}
            {cluster?.enabled ? "Server cluster" : "Relay transports"}
          </span>
        }
      />
      <div className="space-y-3 px-5 py-4 text-[13px]">
        <div className="flex items-center justify-between">
          <span className="text-ink-3">UDP relay</span>
          <span>{udpRelay ? "On (WebSocket fallback)" : "Off (WebSocket only)"}</span>
        </div>
        {cluster?.enabled ? (
          <>
            <div className="flex items-center justify-between">
              <span className="text-ink-3">This instance</span>
              <span>
                <Mono>{cluster.instance_id}</Mono> {cluster.leader && <Badge tone="blued">Leader</Badge>}
              </span>
            </div>
            <ul className="divide-y divide-line rounded-lg border border-line">
              {cluster.instances.map((i) => (
                <li key={i.id} className="flex items-center gap-3 px-3 py-2">
                  {i.id ? <CheckCircle2 className="size-4 shrink-0 text-verdigris" /> : <XCircle className="size-4 text-oxide" />}
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">
                      {i.name || i.id} {i.self && <span className="text-ink-3">(this one)</span>}
                    </span>
                    <span className="block truncate font-mono text-xs text-ink-3">{i.relay_url}</span>
                  </span>
                  <Badge>{i.region || "default"}</Badge>
                </li>
              ))}
            </ul>
            <p className="text-xs text-ink-3">Clients connect to the nearest relay and reach devices on the others automatically.</p>
          </>
        ) : (
          <p className="text-xs text-ink-3">Running as a single server. Several servers can share one PostgreSQL database for high availability; see the cluster guide.</p>
        )}
      </div>
    </Panel>
  );
}
