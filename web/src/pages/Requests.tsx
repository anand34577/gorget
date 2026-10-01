import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, Clock, Plus, ShieldPlus, X } from "lucide-react";
import { toast } from "sonner";
import { errMessage, get, post } from "@/lib/api";
import type { AccessRequest, Device, UserView } from "@/lib/types";
import { useSession } from "@/lib/session";
import { fmtDate, relTime } from "@/lib/utils";
import { Badge, Button, confirmAction, Dialog, EmptyState, ErrorNote, Field, Input, Mono, PageHeader, Panel, PanelHeader, Select, Skeleton, Textarea } from "@/components/ui";

const durations: [number, string][] = [
  [30, "30 minutes"],
  [60, "1 hour"],
  [240, "4 hours"],
  [480, "8 hours"],
  [1440, "1 day"],
  [4320, "3 days"],
  [10080, "7 days"],
];

function durationLabel(min: number) {
  return durations.find(([m]) => m === min)?.[1] ?? `${min} minutes`;
}

function Status({ r }: { r: AccessRequest }) {
  const now = Date.now() / 1000;
  switch (r.status) {
    case "pending":
      return <Badge tone="warn">Waiting</Badge>;
    case "approved":
      return r.granted_until > now ? <Badge tone="ok">Active until {fmtDate(r.granted_until)}</Badge> : <Badge>Expired</Badge>;
    case "denied":
      return <Badge tone="danger">Denied</Badge>;
    default:
      return <Badge>Ended early</Badge>;
  }
}

/** Temporary access: ask for a time-limited window to a device, and approve or grant them. */
export function Requests() {
  const { can, me } = useSession();
  const qc = useQueryClient();
  const admin = can("manage_net");
  const list = useQuery({ queryKey: ["access-requests"], queryFn: () => get<AccessRequest[]>("/access-requests"), refetchInterval: 30_000 });
  const [requestOpen, setRequestOpen] = useState(false);
  const [grantOpen, setGrantOpen] = useState(false);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["access-requests"] });
    qc.invalidateQueries({ queryKey: ["overview"] });
  };
  const act = async (id: string, what: "approve" | "deny" | "revoke", body: object = {}) => {
    try {
      await post(`/access-requests/${id}/${what}`, body);
      toast.success(what === "approve" ? "Access approved" : what === "deny" ? "Request denied" : "Access ended");
      refresh();
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  const { waiting, active, history } = useMemo(() => {
    const now = Date.now() / 1000;
    const all = list.data ?? [];
    return {
      waiting: all.filter((r) => r.status === "pending"),
      active: all.filter((r) => r.status === "approved" && r.granted_until > now),
      history: all.filter((r) => r.status !== "pending" && !(r.status === "approved" && r.granted_until > now)),
    };
  }, [list.data]);

  return (
    <>
      <PageHeader
        title="Temporary access"
        description="Ask for access to a device for a limited time, instead of keeping permanent rules. Approved access ends by itself."
        actions={
          <>
            {admin && (
              <Button onClick={() => setGrantOpen(true)}>
                <ShieldPlus /> Grant access
              </Button>
            )}
            <Button variant="primary" onClick={() => setRequestOpen(true)}>
              <Plus /> Request access
            </Button>
          </>
        }
      />
      {list.isLoading ? (
        <Skeleton className="h-48" />
      ) : !list.data?.length ? (
        <Panel>
          <EmptyState icon={<Clock />} title="No requests yet" action={<Button variant="primary" onClick={() => setRequestOpen(true)}>Request access</Button>}>
            {admin ? "When someone asks for temporary access it appears here for you to approve." : "Need to reach a device you don't normally have access to? Ask for a time-limited window."}
          </EmptyState>
        </Panel>
      ) : (
        <div className="space-y-6">
          {admin && waiting.length > 0 && (
            <Panel>
              <PanelHeader title={`Waiting for a decision (${waiting.length})`} />
              <ul className="divide-y divide-line">
                {waiting.map((r) => (
                  <li key={r.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-[13px]">
                    <div className="min-w-0 flex-1">
                      <div>
                        <span className="font-medium">{r.requester}</span> wants <Mono>{r.target}</Mono> <span className="text-ink-3">ports</span> <Mono>{r.ports}</Mono> <span className="text-ink-3">for</span> {durationLabel(r.minutes)}
                      </div>
                      {r.reason && <div className="mt-0.5 text-ink-2">“{r.reason}”</div>}
                      <div className="text-xs text-ink-3">{relTime(r.created_at)}</div>
                    </div>
                    <Button size="sm" variant="primary" onClick={() => act(r.id, "approve")}>
                      <Check /> Approve
                    </Button>
                    <Button size="sm" onClick={() => act(r.id, "deny")}>
                      <X /> Deny
                    </Button>
                  </li>
                ))}
              </ul>
            </Panel>
          )}
          {active.length > 0 && (
            <Panel>
              <PanelHeader title="Access in force" />
              <ul className="divide-y divide-line">
                {active.map((r) => (
                  <li key={r.id} className="flex flex-wrap items-center gap-3 px-5 py-3 text-[13px]">
                    <div className="min-w-0 flex-1">
                      <span className="font-medium">{r.requester === me.user.email ? "You" : r.requester}</span> can reach <Mono>{r.target}</Mono> <span className="text-ink-3">ports</span> <Mono>{r.ports}</Mono>
                      <div className="text-xs text-ink-3">ends {relTime(r.granted_until)} · approved by {r.decided_by}</div>
                    </div>
                    {admin && (
                      <Button
                        size="sm"
                        variant="danger-ghost"
                        onClick={async () => (await confirmAction({ title: "End this access now?", description: `${r.requester} loses access to ${r.target} immediately.`, confirm: "End access", danger: true })) && act(r.id, "revoke")}
                      >
                        <Ban /> End now
                      </Button>
                    )}
                  </li>
                ))}
              </ul>
            </Panel>
          )}
          {!admin && waiting.length > 0 && (
            <Panel>
              <PanelHeader title="Waiting for approval" />
              <ul className="divide-y divide-line">
                {waiting.map((r) => (
                  <li key={r.id} className="px-5 py-3 text-[13px]">
                    <Mono>{r.target}</Mono> <span className="text-ink-3">ports</span> <Mono>{r.ports}</Mono> · {durationLabel(r.minutes)} <span className="text-ink-3">· asked {relTime(r.created_at)}</span>
                  </li>
                ))}
              </ul>
            </Panel>
          )}
          {history.length > 0 && (
            <Panel>
              <PanelHeader title="History" />
              <ul className="divide-y divide-line">
                {history.slice(0, 50).map((r) => (
                  <li key={r.id} className="flex flex-wrap items-center gap-3 px-5 py-2.5 text-[13px]">
                    <span className="min-w-0 flex-1 truncate">
                      {admin && <span className="font-medium">{r.requester} · </span>}
                      <Mono>{r.target}</Mono> <span className="text-ink-3">· {durationLabel(r.minutes)}</span>
                    </span>
                    <Status r={r} />
                  </li>
                ))}
              </ul>
            </Panel>
          )}
        </div>
      )}
      <RequestDialog open={requestOpen} onOpenChange={setRequestOpen} onDone={refresh} />
      {admin && <GrantDialog open={grantOpen} onOpenChange={setGrantOpen} onDone={refresh} />}
    </>
  );
}

function useTargets() {
  const devices = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices") });
  return useMemo(() => {
    const out: { value: string; label: string }[] = [];
    const tags = new Set<string>();
    for (const d of devices.data ?? []) {
      if (d.kind === "gateway") continue;
      out.push({ value: `device:${d.name}`, label: `${d.name} (${d.user_email || "tagged"})` });
      d.tags.forEach((t) => tags.add(t));
    }
    tags.forEach((t) => out.push({ value: t, label: `All devices tagged ${t}` }));
    return out;
  }, [devices.data]);
}

function AccessForm({ onSubmit, busy, err, extra, submitLabel }: { onSubmit: (v: { target: string; ports: string; minutes: number; reason: string }) => void; busy: boolean; err: string; extra?: React.ReactNode; submitLabel: string }) {
  const targets = useTargets();
  const [target, setTarget] = useState("");
  const [ports, setPorts] = useState("*");
  const [minutes, setMinutes] = useState(60);
  const [reason, setReason] = useState("");
  const chosen = target || targets[0]?.value || "";
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit({ target: chosen, ports, minutes, reason });
      }}
    >
      {extra}
      <Field label="What do you need to reach?">
        <Select value={chosen} onChange={(e) => setTarget(e.target.value)}>
          {targets.map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </Select>
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Ports" hint="For example 22 or 443,8080 or * for all.">
          <Input value={ports} onChange={(e) => setPorts(e.target.value)} className="font-mono" />
        </Field>
        <Field label="For how long?">
          <Select value={String(minutes)} onChange={(e) => setMinutes(Number(e.target.value))}>
            {durations.map(([m, l]) => (
              <option key={m} value={m}>
                {l}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <Field label="Reason" hint="Shown to whoever approves it and kept in the activity log.">
        <Textarea rows={2} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Fixing the build server" />
      </Field>
      {err && <ErrorNote>{err}</ErrorNote>}
      <div className="flex justify-end">
        <Button type="submit" variant="primary" loading={busy} disabled={!chosen}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}

function RequestDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Request temporary access" description="An administrator will approve or deny it. Once approved the access ends on its own.">
      <AccessForm
        busy={busy}
        err={err}
        submitLabel="Send request"
        onSubmit={async (v) => {
          setBusy(true);
          setErr("");
          try {
            await post("/access-requests", v);
            toast.success("Request sent");
            onDone();
            onOpenChange(false);
          } catch (e) {
            setErr(errMessage(e));
          } finally {
            setBusy(false);
          }
        }}
      />
    </Dialog>
  );
}

function GrantDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => void }) {
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<UserView[]>("/users") });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [user, setUser] = useState("");
  const chosen = user || users.data?.[0]?.id || "";
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Grant temporary access" description="Give a person, for example a guest or contractor, access to one device for a limited time. It starts now and ends by itself.">
      <AccessForm
        busy={busy}
        err={err}
        submitLabel="Grant access"
        extra={
          <Field label="Who gets access?">
            <Select value={chosen} onChange={(e) => setUser(e.target.value)}>
              {(users.data ?? []).map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name ? `${u.name} · ${u.email}` : u.email}
                </option>
              ))}
            </Select>
          </Field>
        }
        onSubmit={async (v) => {
          setBusy(true);
          setErr("");
          try {
            await post("/access-grants", { ...v, user_id: chosen });
            toast.success("Access granted");
            onDone();
            onOpenChange(false);
          } catch (e) {
            setErr(errMessage(e));
          } finally {
            setBusy(false);
          }
        }}
      />
    </Dialog>
  );
}
