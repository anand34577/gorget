import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, ArrowRight, Cable, CheckCircle2, Circle, Clock, HeartPulse, KeyRound, Monitor, ShieldAlert, Waypoints, X } from "lucide-react";
import { get } from "@/lib/api";
import type { Device, Overview as OverviewT, Stats } from "@/lib/types";
import { TimeChart } from "@/components/charts";
import { useSession } from "@/lib/session";
import { cn, fmtBytes, osLabel, relTime } from "@/lib/utils";
import { Ago, useTick } from "@/components/data";
import { Badge, Button, EmptyState, Panel, PanelHeader, Skeleton, Tip } from "@/components/ui";

export function Overview() {
  const { me, isAdmin } = useSession();
  const ov = useQuery({ queryKey: ["overview"], queryFn: () => get<OverviewT>("/overview"), refetchInterval: 30_000 });
  const devices = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices") });
  const o = ov.data;

  return (
    <div className="space-y-6">
      <div>
        <p className="text-[13px] text-ink-3">{me.network.name}</p>
        <h1 className="font-display text-[30px] font-semibold leading-tight">
          {o ? (
            <>
              {o.devices.online} of {o.devices.total} devices connected
            </>
          ) : (
            <Skeleton className="h-9 w-72" />
          )}
        </h1>
      </div>

      {isAdmin && o && <GettingStarted o={o} />}

      <LamellarBand devices={devices.data} loading={devices.isLoading} />

      {o && <Attention o={o} />}

      {isAdmin && <Activity24h />}

      <div className="grid gap-6 lg:grid-cols-3">
        <Panel className="lg:col-span-2">
          <PanelHeader
            title="Recently active"
            actions={
              <Link to="/devices" className="inline-flex items-center gap-1 text-[13px] text-ink-2 hover:text-ink">
                All devices <ArrowRight className="size-3.5" />
              </Link>
            }
          />
          <RecentDevices devices={devices.data} />
        </Panel>
        <div className="space-y-6">
          <Panel>
            <PanelHeader title="Network" />
            <dl className="space-y-2.5 px-5 py-4 text-[13px]">
              <Row k="Address range" v={<span className="font-mono">{o?.network.ipv4}</span>} />
              <Row k="IPv6" v={<span className="font-mono text-xs">{o?.network.ipv6}</span>} />
              <Row k="Domain" v={<span className="font-mono">{o?.network.domain}</span>} />
              <Row k="Native clients" v={o?.devices.native} />
              <Row k="WireGuard apps" v={o?.devices.wireguard} />
              {isAdmin && <Row k="Access rules" v={o ? `version ${o.policy_version}` : ""} />}
            </dl>
          </Panel>
          {isAdmin && o?.gateway && (
            <Panel>
              <PanelHeader title="Gateway & relay" />
              <dl className="space-y-2.5 px-5 py-4 text-[13px]">
                <Row
                  k="WireGuard gateway"
                  v={o.gateway.running ? <Badge tone="ok">Running · {o.gateway.backend}</Badge> : o.gateway.enabled ? <Badge tone="danger">Not running</Badge> : <Badge>Off</Badge>}
                />
                <Row k="Endpoint" v={<span className="font-mono text-xs">{o.gateway.endpoint}</span>} />
                <Row k="Relay sessions" v={o.relay?.enabled ? o.relay.connections : "off"} />
                <Row k="Relayed traffic" v={o.relay?.enabled ? fmtBytes(o.relay.bytes ?? 0) : "—"} />
              </dl>
              {o.gateway.error && <p className="border-t border-line px-5 py-3 text-xs text-oxide">{o.gateway.error}</p>}
            </Panel>
          )}
        </div>
      </div>

      {isAdmin && o?.recent_activity && o.recent_activity.length > 0 && (
        <Panel>
          <PanelHeader
            title="Latest changes"
            actions={
              <Link to="/activity" className="inline-flex items-center gap-1 text-[13px] text-ink-2 hover:text-ink">
                Activity log <ArrowRight className="size-3.5" />
              </Link>
            }
          />
          <ul className="divide-y divide-line">
            {o.recent_activity.map((e) => (
              <li key={e.seq} className="flex items-center gap-3 px-5 py-2.5 text-[13px]">
                <span className="w-28 shrink-0 text-xs text-ink-3"><Ago ts={e.ts} /></span>
                <span className="truncate">
                  <span className="font-medium">{e.actor || "system"}</span> <span className="text-ink-2">{describeAction(e.action)}</span>{" "}
                  {e.target_name && <span className="font-mono text-[12.5px]">{e.target_name}</span>}
                </span>
              </li>
            ))}
          </ul>
        </Panel>
      )}
    </div>
  );
}

const startedKey = "gorget.gettingStarted.dismissed";

/** First-run checklist for administrators; hides itself once done or dismissed. */
function GettingStarted({ o }: { o: OverviewT }) {
  const navigate = useNavigate();
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem(startedKey) === "1";
    } catch {
      return false;
    }
  });
  const steps = [
    { done: true, title: "Create your network", body: `Addresses ${o.network.ipv4}, names under ${o.network.domain}.` },
    {
      done: o.devices.total >= 1,
      title: "Add your first device",
      body: "Install the Gorget app (Windows, macOS, Linux, Android) or use any standard WireGuard app.",
      action: () => navigate("/devices?add=1"),
      label: "Add a device",
    },
    {
      done: o.devices.total >= 2,
      title: "Add a second device",
      body: `Then they reach each other by name, for example laptop.${o.network.domain}.`,
      action: () => navigate("/devices?add=1"),
      label: "Add another",
    },
    {
      done: o.users >= 2,
      title: "Invite the people who should use it",
      body: "Each person signs in with their own account, so access rules can tell them apart.",
      action: () => navigate("/users"),
      label: "Invite people",
    },
    {
      done: o.policy_version > 1,
      title: "Review who can reach what",
      body: "New networks let every device reach every other. Narrow it down when you're ready.",
      action: () => navigate("/access"),
      label: "Open access rules",
    },
  ];
  const left = steps.filter((st) => !st.done).length;
  if (dismissed || left === 0) return null;
  const dismiss = () => {
    try {
      localStorage.setItem(startedKey, "1");
    } catch {
      /* private mode: hide for this visit only */
    }
    setDismissed(true);
  };
  const next = steps.find((st) => !st.done);
  return (
    <Panel>
      <PanelHeader
        title="Getting started"
        description={`${steps.length - left} of ${steps.length} done`}
        actions={
          <Button variant="ghost" size="icon" aria-label="Hide the getting started list" title="Hide" onClick={dismiss}>
            <X className="size-4" />
          </Button>
        }
      />
      <ol className="divide-y divide-line">
        {steps.map((st) => (
          <li key={st.title} className="flex items-start gap-3 px-5 py-3">
            {st.done ? <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-verdigris" aria-label="Done" /> : <Circle className="mt-0.5 size-4 shrink-0 text-ink-3" aria-label="To do" />}
            <div className="min-w-0 flex-1">
              <p className={cn("text-[13px] font-medium", st.done && "text-ink-3 line-through decoration-ink-3/40")}>{st.title}</p>
              {!st.done && <p className="mt-0.5 text-xs text-ink-2">{st.body}</p>}
            </div>
            {!st.done && st.action && (
              <Button size="sm" variant={st === next ? "primary" : "secondary"} onClick={st.action}>
                {st.label}
              </Button>
            )}
          </li>
        ))}
      </ol>
    </Panel>
  );
}

/** Activity24h is a compact view of the last day; Insights has the rest. */
function Activity24h() {
  const q = useQuery({ queryKey: ["stats", "24h"], queryFn: () => get<Stats>("/stats?range=24h"), refetchInterval: 60_000 });
  if (!q.data || q.data.series.length < 2) return null;
  return (
    <Panel className="p-5">
      <TimeChart title="Devices online, last 24 hours" points={q.data.series as never} series={[{ key: "online", label: "Online", color: "var(--series-1)" }]} height={150} integer />
      <div className="mt-2 text-right">
        <Link to="/insights" className="inline-flex items-center gap-1 text-[13px] text-ink-2 hover:text-ink">
          Traffic, locations and connection history <ArrowRight className="size-3.5" />
        </Link>
      </div>
    </Panel>
  );
}

function Row({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <dt className="text-ink-3">{k}</dt>
      <dd className="text-right">{v ?? "—"}</dd>
    </div>
  );
}

/** Signature element: every device is a lame (armour plate), overlapping like a gorget. */
function LamellarBand({ devices, loading }: { devices?: Device[]; loading: boolean }) {
  const navigate = useNavigate();
  useTick();
  if (loading) return <Skeleton className="h-24 w-full" />;
  const list = (devices ?? []).filter((d) => d.kind !== "gateway").sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name));
  if (!list.length) {
    return (
      <Panel>
        <EmptyState icon={<Monitor />} title="No devices yet" action={<Button variant="primary" onClick={() => navigate("/devices?add=1")}>Add your first device</Button>}>
          Install the Gorget app or add a standard WireGuard app to start building your network.
        </EmptyState>
      </Panel>
    );
  }
  const online = list.filter((d) => d.online).length;
  const pending = list.filter((d) => d.state === "pending").length;
  return (
    <Panel className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 border-b border-line px-5 py-2.5 text-xs text-ink-3">
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-sm bg-verdigris" /> {online} connected
        </span>
        <span className="flex items-center gap-1.5">
          <span className="size-2 rounded-sm bg-line-strong" /> {list.length - online - pending} offline
        </span>
        {pending > 0 && (
          <span className="flex items-center gap-1.5">
            <span className="size-2 rounded-sm bg-straw" /> {pending} awaiting approval
          </span>
        )}
      </div>
      <div className="flex flex-wrap gap-y-3 px-5 py-5 pl-7" role="list" aria-label="Devices">
        {list.map((d, i) => (
          <Tip
            key={d.id}
            content={
              <span>
                <b>{d.name}</b> · {d.ipv4}
                <br />
                {d.state === "pending" ? "Awaiting approval" : d.online ? "Connected" : `Last seen ${relTime(d.last_seen_at)}`}
              </span>
            }
          >
            <button
              role="listitem"
              onClick={() => navigate(d.kind === "wireguard" ? "/wireguard" : `/devices/${d.id}`)}
              aria-label={`${d.name}, ${d.online ? "connected" : "offline"}`}
              style={{ zIndex: list.length - i }}
              className={cn(
                "group relative -ml-2.5 h-16 w-13 rounded-[10px_10px_24px_24px] border shadow-sm transition-transform duration-150 hover:-translate-y-1 focus-visible:-translate-y-1",
                d.state === "pending"
                  ? "border-straw/50 bg-gradient-to-b from-straw-soft to-straw/30"
                  : d.online
                    ? "border-verdigris/50 bg-gradient-to-b from-verdigris-soft to-verdigris/35"
                    : "border-line-strong bg-gradient-to-b from-surface-2 to-sunken",
              )}
            >
              <span className="absolute inset-x-1.5 top-1.5 h-px bg-white/50 dark:bg-white/10" />
              <span className="absolute left-1.5 top-2.5 size-1 rounded-full bg-ink-3/40" />
              <span className="absolute right-1.5 top-2.5 size-1 rounded-full bg-ink-3/40" />
              <span className="absolute inset-x-0 bottom-1.5 truncate px-1 text-center font-mono text-[9px] text-ink-2">{d.name.slice(0, 7)}</span>
              {d.posture?.length > 0 && <span className="absolute -right-0.5 -top-0.5 size-2.5 rounded-full border-2 border-surface bg-oxide" aria-label="Breaks device health rules" />}
            </button>
          </Tip>
        ))}
      </div>
    </Panel>
  );
}

function Attention({ o }: { o: OverviewT }) {
  const items: { icon: React.ReactNode; text: string; to: string; tone: "warn" | "danger" }[] = [];
  if (o.devices.pending) items.push({ icon: <ShieldAlert />, text: `${o.devices.pending} device${o.devices.pending > 1 ? "s" : ""} waiting for approval`, to: "/devices?filter=pending", tone: "warn" });
  if (o.pending_routes) items.push({ icon: <Waypoints />, text: `${o.pending_routes} route${o.pending_routes > 1 ? "s" : ""} or exit node${o.pending_routes > 1 ? "s" : ""} waiting for approval`, to: "/routes", tone: "warn" });
  if (o.devices.non_compliant) items.push({ icon: <HeartPulse />, text: `${o.devices.non_compliant} device${o.devices.non_compliant > 1 ? "s" : ""} break${o.devices.non_compliant > 1 ? "" : "s"} your device health rules`, to: "/devices?filter=health", tone: "warn" });
  if (o.pending_access) items.push({ icon: <Clock />, text: `${o.pending_access} temporary access request${o.pending_access > 1 ? "s" : ""} waiting for a decision`, to: "/requests", tone: "warn" });
  if (o.devices.expiring_soon) items.push({ icon: <KeyRound />, text: `${o.devices.expiring_soon} device key${o.devices.expiring_soon > 1 ? "s" : ""} expire within 7 days`, to: "/devices", tone: "warn" });
  if (o.policy_error) items.push({ icon: <AlertTriangle />, text: "The saved access rules are invalid, so all traffic is blocked", to: "/access", tone: "danger" });
  if (o.gateway?.enabled && !o.gateway.running) items.push({ icon: <Cable />, text: "The WireGuard gateway isn't running, so WireGuard apps can't connect", to: "/settings?tab=system", tone: "danger" });
  if (!items.length)
    return (
      <div className="flex items-center gap-2 text-[13px] text-ink-3">
        <CheckCircle2 className="size-4 text-verdigris" /> Nothing needs your attention.
      </div>
    );
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {items.map((it) => (
        <Link
          key={it.text}
          to={it.to}
          className={cn(
            "flex items-center gap-3 rounded-lg border px-4 py-3 text-[13px] transition-colors [&_svg]:size-4 [&_svg]:shrink-0",
            it.tone === "danger" ? "border-oxide/30 bg-oxide-soft text-oxide hover:border-oxide/60" : "border-straw/30 bg-straw-soft hover:border-straw/60",
          )}
        >
          <span className={it.tone === "danger" ? "" : "text-straw"}>{it.icon}</span>
          <span className="flex-1 text-ink">{it.text}</span>
          <ArrowRight className="text-ink-3" />
        </Link>
      ))}
    </div>
  );
}

function RecentDevices({ devices }: { devices?: Device[] }) {
  useTick();
  const list = (devices ?? [])
    .filter((d) => d.kind !== "gateway")
    .sort((a, b) => Number(b.online) - Number(a.online) || b.last_seen_at - a.last_seen_at)
    .slice(0, 7);
  if (!list.length) return <p className="px-5 py-6 text-[13px] text-ink-3">No devices yet.</p>;
  return (
    <ul className="divide-y divide-line">
      {list.map((d) => (
        <li key={d.id}>
          <Link to={d.kind === "wireguard" ? "/wireguard" : `/devices/${d.id}`} className="flex items-center gap-3 px-5 py-2.5 hover:bg-surface-2">
            <span className={cn("size-2 rounded-full", d.online ? "bg-verdigris" : d.state === "pending" ? "bg-straw" : "bg-line-strong")} />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[13px] font-medium">{d.name}</span>
              <span className="block truncate text-xs text-ink-3">
                {osLabel[d.os] ?? d.os} {d.user_email && `· ${d.user_email}`}
              </span>
            </span>
            <span className="hidden font-mono text-[12.5px] text-ink-2 sm:block">{d.ipv4}</span>
            <span className="w-24 text-right text-xs text-ink-3">{d.online ? "now" : <Ago ts={d.last_seen_at} />}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

const actionText: Record<string, string> = {
  "device.register": "registered",
  "device.update": "updated device",
  "device.delete": "removed device",
  "device.approve": "approved",
  "device.expire_key": "expired the key of",
  "device.login_approve": "approved a sign-in from",
  "wgconfig.create": "added WireGuard app",
  "wgconfig.rotate_psk": "rotated the pre-shared key of",
  "policy.update": "changed access rules",
  "policy.restore": "restored access rules",
  "route.update": "updated route",
  "route.delete": "removed route",
  "user.create": "added",
  "user.update": "updated",
  "user.delete": "removed",
  "settings.update": "changed settings:",
  "setup_key.create": "created setup key",
  "setup_key.revoke": "revoked setup key",
  "auth.login": "signed in",
  "auth.login_failed": "failed to sign in",
  "setup.complete": "created the network",
  "access.request": "asked for temporary access to",
  "access.grant": "granted temporary access to",
  "access.approve": "approved temporary access to",
  "access.deny": "denied access to",
  "access.revoke": "ended temporary access to",
  "scim.token": "created a provisioning token",
  "scim.disable": "turned provisioning off",
  "group.create": "created group",
  "group.update": "updated group",
  "group.delete": "removed group",
};

export function describeAction(a: string) {
  return actionText[a] ?? a.replace(/[._]/g, " ");
}
