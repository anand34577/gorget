import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, KeyRound, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { del, errMessage, get, post } from "@/lib/api";
import type { SetupKey } from "@/lib/types";
import { useSession } from "@/lib/session";
import { fmtDate, relTime } from "@/lib/utils";
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
  const allowed = can("manage_net") || me.features.user_setup_keys;

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
        {!keys.data?.length ? (
          <EmptyState icon={<KeyRound />} title="No setup keys">
            Create one, then run <span className="font-mono">gorget up --setup-key …</span> on the machine.
          </EmptyState>
        ) : (
          <Table>
            <thead>
              <tr>
                <Th>Name</Th>
                <Th>Key</Th>
                <Th className="hidden md:table-cell">Type</Th>
                <Th className="hidden md:table-cell">Used</Th>
                <Th className="hidden lg:table-cell">Expires</Th>
                <Th>Status</Th>
                <Th className="w-20" />
              </tr>
            </thead>
            <tbody>
              {keys.data.map((k) => {
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
                      {k.max_uses ? ` / ${k.max_uses}` : ""} {k.last_used_at > 0 && <span className="text-xs text-ink-3">· {relTime(k.last_used_at)}</span>}
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
              <div className="mb-1 text-[13px] font-medium">Join a Linux server</div>
              <pre className="overflow-x-auto rounded-md bg-sunken p-3 font-mono text-[12px]">
                gorget up --server {me.public_url} --setup-key {created}
              </pre>
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
          <ListEditor values={tags} onChange={setTags} placeholder="tag:server" />
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
