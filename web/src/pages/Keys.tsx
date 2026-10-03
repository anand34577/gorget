import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, KeyRound, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { del, errMessage, get, post } from "@/lib/api";
import type { Device, SetupKey } from "@/lib/types";
import { useSession } from "@/lib/session";
import { fmtDate } from "@/lib/utils";
import { Ago, matchesQuery, Pagination, SearchInput, SortTh, useDebounced, useTable, useTick } from "@/components/data";
import { Badge, Button, Checkbox, confirmAction, Dialog, EmptyState, ErrorNote, Field, Input, ListEditor, Mono, Note, PageHeader, Panel, SecretBox, Select, Table, Tag, Td, Th } from "@/components/ui";

function status(k: SetupKey): { label: string; tone: "ok" | "danger" | "neutral" } {
  if (k.revoked) return { label: "Revoked", tone: "danger" };
  if (k.expires_at && k.expires_at < Date.now() / 1000) return { label: "Expired", tone: "neutral" };
  if ((!k.reusable && k.uses > 0) || (k.max_uses && k.uses >= k.max_uses)) return { label: "Used up", tone: "neutral" };
  return { label: "Active", tone: "ok" };
}

export function SetupKeys() {
  const [params, setParams] = useSearchParams();
  const { can, me } = useSession();
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["setup-keys"], queryFn: () => get<SetupKey[]>("/setup-keys") });
  const [open, setOpen] = useState(params.get("new") === "1");
  const [created, setCreated] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const dq = useDebounced(q, 200);
  const [show, setShow] = useState<"all" | "active" | "inactive">("all");
  useTick();
  const allowed = can("manage_net") || me.features.user_setup_keys;
  const list = useMemo(
    () => (keys.data ?? []).filter((k) => (show === "all" || (show === "active") === (status(k).label === "Active")) && matchesQuery(dq, k.name, k.key_prefix, k.tags, k.created_by)),
    [keys.data, dq, show],
  );
  const table = useTable(list, {
    defaultSort: { key: "created", dir: "asc" },
    sorters: {
      name: (a, b) => a.name.localeCompare(b.name),
      created: (a, b) => b.created_at - a.created_at,
      used: (a, b) => b.uses - a.uses,
      expires: (a, b) => (a.expires_at || Infinity) - (b.expires_at || Infinity),
      status: (a, b) => status(a).label.localeCompare(status(b).label),
    },
  });

  useEffect(() => {
    if (params.get("new") === "1") setOpen(true);
  }, [params]);

  const act = async (fn: () => Promise<unknown>, msg: string) => {
    try {
      await fn();
      toast.success(msg);
      qc.invalidateQueries({ queryKey: ["setup-keys"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  return (
    <>
      <PageHeader
        title="Setup keys"
        description="Join servers, containers and routers without an interactive sign-in. Devices joined with a tagged key belong to the tag and don't expire."
        actions={
          allowed && (
            <Button variant="primary" onClick={() => setOpen(true)}>
              <Plus /> Create setup key
            </Button>
          )
        }
      />
      <Panel>
        {!!keys.data?.length && (
          <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3">
            <div className="flex rounded-md border border-line bg-surface-2 p-0.5" role="group" aria-label="Filter keys">
              {(["all", "active", "inactive"] as const).map((f) => (
                <button key={f} aria-pressed={show === f} onClick={() => setShow(f)} className={"rounded px-2.5 py-1 text-xs font-medium capitalize " + (show === f ? "bg-surface text-ink shadow-sm" : "text-ink-3 hover:text-ink")}>
                  {f}
                </button>
              ))}
            </div>
            <SearchInput value={q} onChange={setQ} placeholder="Name, tag or key prefix" label="Search setup keys" className="ml-auto" />
          </div>
        )}
        {keys.isLoading ? (
          <div className="space-y-2 p-4">
            <div className="h-10 animate-pulse rounded bg-sunken" />
            <div className="h-10 animate-pulse rounded bg-sunken" />
          </div>
        ) : !keys.data?.length ? (
          <EmptyState icon={<KeyRound />} title="No setup keys">
            Create one, then run <span className="font-mono">gorget up --setup-key …</span> on the machine.
          </EmptyState>
        ) : list.length === 0 ? (
          <EmptyState icon={<KeyRound />} title="No keys match" action={<Button onClick={() => (setQ(""), setShow("all"))}>Clear search and filters</Button>}>
            Try another name or filter.
          </EmptyState>
        ) : (
          <>
          <Table>
            <thead>
              <tr>
                <SortTh label="Name" sortKey="name" table={table} />
                <Th>Key</Th>
                <Th className="hidden md:table-cell">Type</Th>
                <SortTh label="Used" sortKey="used" table={table} className="hidden md:table-cell" />
                <SortTh label="Expires" sortKey="expires" table={table} className="hidden lg:table-cell" />
                <SortTh label="Status" sortKey="status" table={table} />
                <Th className="w-20" />
              </tr>
            </thead>
            <tbody>
              {table.rows.map((k) => {
                const s = status(k);
                return (
                  <tr key={k.id} className="hover:bg-surface-2">
                    <Td>
                      <div className="font-medium">{k.name}</div>
                      <div className="mt-0.5 flex flex-wrap gap-1">
                        {k.tags.map((t) => (
                          <Tag key={t}>{t}</Tag>
                        ))}
                      </div>
                    </Td>
                    <Td>
                      <Mono className="text-ink-2">{k.key_prefix}…</Mono>
                    </Td>
                    <Td className="hidden md:table-cell">
                      <div className="flex flex-wrap gap-1">
                        <Badge>{k.reusable ? "Reusable" : "One-time"}</Badge>
                        {k.ephemeral && <Badge>Ephemeral</Badge>}
                        {!k.auto_approve && <Badge tone="warn">Needs approval</Badge>}
                      </div>
                    </Td>
                    <Td className="hidden md:table-cell text-ink-2">
                      {k.uses}
                      {k.max_uses ? ` / ${k.max_uses}` : ""} {k.last_used_at > 0 && <span className="text-xs text-ink-3">· <Ago ts={k.last_used_at} /></span>}
                    </Td>
                    <Td className="hidden lg:table-cell text-ink-2">{k.expires_at ? fmtDate(k.expires_at) : "Never"}</Td>
                    <Td>
                      <Badge tone={s.tone}>{s.label}</Badge>
                    </Td>
                    <Td>
                      <div className="flex justify-end gap-1">
                        {!k.revoked && (
                          <Button variant="ghost" size="icon" aria-label={`Revoke ${k.name}`} onClick={() => act(() => post(`/setup-keys/${k.id}/revoke`), "Key revoked")}>
                            <Ban />
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Delete ${k.name}`}
                          onClick={async () => (await confirmAction({ title: `Delete ${k.name}?`, description: "Devices already joined with it stay connected.", confirm: "Delete key", danger: true })) && act(() => del(`/setup-keys/${k.id}`), "Key deleted")}
                        >
                          <Trash2 />
                        </Button>
                      </div>
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </Table>
          <Pagination table={table} noun="keys" />
          </>
        )}
      </Panel>
      <CreateKey
        open={open}
        onOpenChange={(v) => {
          setOpen(v);
          if (!v && params.get("new")) setParams({});
        }}
        onCreated={(key) => {
          setCreated(key);
          qc.invalidateQueries({ queryKey: ["setup-keys"] });
        }}
      />
      <Dialog open={!!created} onOpenChange={(o) => !o && setCreated(null)} title="Your setup key" description="Copy it now. Only a hash is stored, so it can't be shown again.">
        {created && (
          <div className="space-y-4">
            <SecretBox value={created} />
            <div>
              <div className="mb-1 text-[13px] font-medium">Linux or Mac: install and join in one step</div>
              <pre className="overflow-x-auto rounded-md bg-sunken p-3 font-mono text-[12px]">curl -fsSL {me.public_url}/install.sh | GORGET_SETUP_KEY={created} sh</pre>
            </div>
            <div>
              <div className="mb-1 text-[13px] font-medium">Windows (PowerShell)</div>
              <pre className="overflow-x-auto rounded-md bg-sunken p-3 font-mono text-[12px]">$env:GORGET_SETUP_KEY='{created}'; irm {me.public_url}/install.ps1 | iex</pre>
            </div>
            <div>
              <div className="mb-1 text-[13px] font-medium">Already installed</div>
              <pre className="overflow-x-auto rounded-md bg-sunken p-3 font-mono text-[12px]">gorget up -server {me.public_url.replace(/^https?:\/\//, "")} -setup-key {created}</pre>
            </div>
          </div>
        )}
      </Dialog>
    </>
  );
}

function CreateKey({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (v: boolean) => void; onCreated: (key: string) => void }) {
  const { can } = useSession();
  const admin = can("manage_net");
  const [name, setName] = useState("");
  const [reusable, setReusable] = useState(false);
  const [ephemeral, setEphemeral] = useState(false);
  const [autoApprove, setAutoApprove] = useState(true);
  const [tags, setTags] = useState<string[]>([]);
  const [days, setDays] = useState("30");
  const [maxUses, setMaxUses] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const devs = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices"), enabled: open });
  const keysQ = useQuery({ queryKey: ["setup-keys"], queryFn: () => get<SetupKey[]>("/setup-keys"), enabled: open });
  const knownTags = useMemo(() => [...new Set([...(devs.data ?? []).flatMap((d) => d.tags), ...(keysQ.data ?? []).flatMap((k) => k.tags), "tag:server", "tag:router"])].sort(), [devs.data, keysQ.data]);
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Create a setup key"
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!name.trim()}
            onClick={async () => {
              setBusy(true);
              setErr("");
              try {
                const r = await post<{ key: string }>("/setup-keys", {
                  name,
                  reusable,
                  ephemeral,
                  auto_approve: admin ? autoApprove : undefined,
                  tags,
                  max_uses: Number(maxUses) || 0,
                  expires_in_days: Number(days),
                });
                onOpenChange(false);
                onCreated(r.key);
                setName("");
                setTags([]);
              } catch (e) {
                setErr(errMessage(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            Create key
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {err && <ErrorNote>{err}</ErrorNote>}
        <Field label="Name" hint="Describe where it's used, e.g. 'k8s nodes'.">
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Tags" hint={admin ? "Devices joined with this key get these tags." : "You can only use tags you own (see tagOwners in the access rules)."}>
          <ListEditor values={tags} onChange={setTags} placeholder="tag:server" suggestions={knownTags} validate={(v) => (/^tag:[a-z0-9][a-z0-9-]*$/.test(v) ? null : "A tag looks like tag:server (lowercase letters, digits and dashes)")} />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Key expires after">
            <Select value={days} onChange={(e) => setDays(e.target.value)}>
              <option value="1">1 day</option>
              <option value="7">7 days</option>
              <option value="30">30 days</option>
              <option value="90">90 days</option>
              <option value="365">1 year</option>
              <option value="0">Never</option>
            </Select>
          </Field>
          {reusable && (
            <Field label="Maximum uses" hint="Empty for unlimited.">
              <Input type="number" min={0} value={maxUses} onChange={(e) => setMaxUses(e.target.value)} />
            </Field>
          )}
        </div>
        <div className="space-y-2">
          <Checkbox checked={reusable} onChange={setReusable} label="Reusable: join many machines with one key" />
          <Checkbox checked={ephemeral} onChange={setEphemeral} label="Ephemeral: remove devices automatically when they go offline (CI, containers)" />
          {admin && <Checkbox checked={autoApprove} onChange={setAutoApprove} label="Approve devices automatically" />}
        </div>
        {!tags.length && <Note>Without tags, devices joined with this key belong to you.</Note>}
      </div>
    </Dialog>
  );
}
