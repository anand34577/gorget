import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import CodeMirror from "@uiw/react-codemirror";
import { json } from "@codemirror/lang-json";
import { ArrowRight, Ban, CheckCircle2, CircleSlash, FlaskConical, GripVertical, History, Pencil, Plus, Server, ShieldCheck, Trash2, Users, Wand2, XCircle } from "lucide-react";
import { Explainer, Flow } from "@/components/flow";
import { toast } from "sonner";
import { ApiError, errMessage, get, post, put } from "@/lib/api";
import type { Device, Group, Policy, PolicyAnalysis, PolicyRule, PolicyVersion, UserView } from "@/lib/types";
import { useSession } from "@/lib/session";
import { cn, relTime, useUnsavedGuard } from "@/lib/utils";
import { SuggestInput, type Suggestion } from "@/components/suggest";
import {
  Badge,
  Button,
  confirmAction,
  Dialog,
  ErrorNote,
  Field,
  Input,
  ListEditor,
  Menu,
  MenuItem,
  Mono,
  Note,
  PageHeader,
  Panel,
  PanelHeader,
  Select,
  Skeleton,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui";

export default function AccessRules() {
  const { can } = useSession();
  const qc = useQueryClient();
  const manage = can("manage_net");
  const current = useQuery({ queryKey: ["policy"], queryFn: () => get<PolicyVersion>("/policy") });
  const [doc, setDoc] = useState<string | null>(null);
  const [analysis, setAnalysis] = useState<PolicyAnalysis | null>(null);
  const [tab, setTab] = useState("rules");
  const [saveOpen, setSaveOpen] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  useEffect(() => {
    if (current.data && doc === null) setDoc(current.data.document);
  }, [current.data, doc]);

  // Validate the draft as it changes.
  useEffect(() => {
    if (doc === null) return;
    clearTimeout(timer.current);
    timer.current = setTimeout(async () => {
      try {
        setAnalysis(await post<PolicyAnalysis>("/policy/validate", { document: doc }));
      } catch {
        /* ignore transient errors */
      }
    }, 350);
    return () => clearTimeout(timer.current);
  }, [doc]);

  const dirtyNow = !!current.data && doc !== null && doc !== current.data.document;
  useUnsavedGuard(dirtyNow);
  if (!current.data || doc === null) return <Skeleton className="h-96" />;
  const dirty = doc !== current.data.document;
  const policy = analysis?.parsed;

  const setPolicy = (p: Policy) => setDoc(JSON.stringify(p, null, 2) + "\n");

  return (
    <>
      <PageHeader
        title="Access rules"
        description="Everything is blocked unless a rule allows it. Rules apply on every device and at the gateway, and changes reach connected devices within seconds."
        actions={
          manage && (
            <>
              {dirty && (
                <Button
                  onClick={async () => {
                    if (await confirmAction({ title: "Discard your changes?", confirm: "Discard" })) setDoc(current.data.document);
                  }}
                >
                  Discard
                </Button>
              )}
              <Button variant="primary" disabled={!dirty || !analysis?.valid} onClick={() => setSaveOpen(true)}>
                Save rules
              </Button>
            </>
          )
        }
      />
      <ValidationBar analysis={analysis} dirty={dirty} version={current.data.version} />
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="rules">Rules</TabsTrigger>
          <TabsTrigger value="file">Policy file</TabsTrigger>
          <TabsTrigger value="try">Try access</TabsTrigger>
          <TabsTrigger value="history">History</TabsTrigger>
        </TabsList>
        <TabsContent value="rules">
          {policy ? <VisualRules policy={policy} onChange={setPolicy} readOnly={!manage} /> : <Note tone="warn">Fix the errors in the policy file to use the visual editor.</Note>}
        </TabsContent>
        <TabsContent value="file">
          <Panel className="overflow-hidden">
            <CodeMirror value={doc} height="60vh" extensions={[json()]} editable={manage} onChange={setDoc} basicSetup={{ foldGutter: true, highlightActiveLine: true }} theme={document.documentElement.dataset.theme === "dark" ? "dark" : "light"} />
          </Panel>
          <p className="mt-2 text-xs text-ink-3">
            JSON with comments and trailing commas. Selectors: <Mono>*</Mono>, an email, <Mono>group:name</Mono>, <Mono>tag:name</Mono>, <Mono>device:name</Mono>, host aliases, IPs and CIDRs, <Mono>autogroup:member</Mono>,{" "}
            <Mono>autogroup:self</Mono> and <Mono>autogroup:internet</Mono>. Destinations add ports: <Mono>tag:server:22,443</Mono>.
          </p>
        </TabsContent>
        <TabsContent value="try">
          <TryAccess document={dirty ? doc : undefined} />
        </TabsContent>
        <TabsContent value="history">
          <HistoryTab
            onLoad={(d) => {
              setDoc(d);
              setTab("file");
              toast.info("Loaded into the editor. Save to make it active.");
            }}
          />
        </TabsContent>
      </Tabs>
      <SaveDialog
        open={saveOpen}
        onOpenChange={setSaveOpen}
        onSave={async (comment) => {
          try {
            await put("/policy", { document: doc, comment, expect_version: current.data.version });
            toast.success("Access rules saved");
            setSaveOpen(false);
            await qc.invalidateQueries({ queryKey: ["policy"] });
            setDoc(null);
          } catch (e) {
            if (e instanceof ApiError && e.details) setAnalysis(e.details as PolicyAnalysis);
            toast.error(errMessage(e));
          }
        }}
      />
    </>
  );
}

function ValidationBar({ analysis, dirty, version }: { analysis: PolicyAnalysis | null; dirty: boolean; version: number }) {
  if (!analysis) return null;
  const failed = analysis.tests.filter((t) => !t.passed);
  return (
    <div className="mb-5 space-y-2">
      <div className="flex flex-wrap items-center gap-3 text-[13px]">
        {analysis.problems.length === 0 && failed.length === 0 ? (
          <span className="inline-flex items-center gap-1.5 text-verdigris">
            <CheckCircle2 className="size-4" /> Valid · {analysis.rules} rule{analysis.rules === 1 ? "" : "s"}
            {analysis.tests.length > 0 && ` · ${analysis.tests.length} test${analysis.tests.length === 1 ? "" : "s"} passing`}
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5 text-oxide">
            <XCircle className="size-4" /> {analysis.problems.length + failed.length} problem{analysis.problems.length + failed.length === 1 ? "" : "s"}
          </span>
        )}
        <span className="text-ink-3">{dirty ? "Unsaved changes" : `Version ${version} is active`}</span>
      </div>
      {(analysis.problems.length > 0 || failed.length > 0) && (
        <ErrorNote>
          <ul className="list-disc space-y-0.5 pl-4">
            {analysis.problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
            {failed.map((t, i) => (
              <li key={i}>
                Test failed: <Mono>{t.src}</Mono> → <Mono>{t.dst}</Mono> should be {t.expect === "accept" ? "allowed" : "blocked"}. {t.error || t.decision.reason}
              </li>
            ))}
          </ul>
        </ErrorNote>
      )}
    </div>
  );
}

function useSelectorOptions() {
  const groups = useQuery({ queryKey: ["groups"], queryFn: () => get<Group[]>("/groups") });
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<UserView[]>("/users") });
  const devices = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices") });
  return useMemo(() => {
    const tags = new Set<string>();
    devices.data?.forEach((d) => d.tags.forEach((t) => tags.add(t)));
    return [
      { label: "Everyone & everything", options: ["*", "autogroup:member"] },
      { label: "Groups", options: (groups.data ?? []).map((g) => `group:${g.name}`) },
      { label: "Tags", options: [...tags] },
      { label: "People", options: (users.data ?? []).map((u) => u.email) },
      { label: "Devices", options: (devices.data ?? []).filter((d) => d.kind !== "gateway").map((d) => `device:${d.name}`) },
    ];
  }, [groups.data, users.data, devices.data]);
}

function Selector({ s, kind }: { s: string; kind: "src" | "dst" }) {
  const special: Record<string, string> = {
    "*": kind === "src" ? "Anyone" : "Anything",
    "autogroup:member": "All members' devices",
    "autogroup:self": "Their own devices",
    "autogroup:internet": "The internet (exit nodes)",
  };
  let sel = s;
  let ports = "";
  if (kind === "dst") {
    const i = s.lastIndexOf(":");
    sel = s.slice(0, i);
    ports = s.slice(i + 1);
  }
  const label = special[sel];
  return (
    <span className={cn("inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[12.5px]", label ? "border-blued/30 bg-blued-soft text-blued" : "border-line bg-surface-2")}>
      {label ?? <span className="font-mono">{sel}</span>}
      {ports && ports !== "*" && <span className="font-mono text-ink-3">:{ports}</span>}
    </span>
  );
}

function VisualRules({ policy, onChange, readOnly }: { policy: Policy; onChange: (p: Policy) => void; readOnly: boolean }) {
  const [editing, setEditing] = useState<{ index: number; rule: PolicyRule } | null>(null);
  const rules = policy.acls ?? [];
  const update = (i: number, r: PolicyRule | null) => {
    const acls = [...rules];
    if (r === null) acls.splice(i, 1);
    else if (i >= acls.length) acls.push(r);
    else acls[i] = r;
    onChange({ ...policy, acls });
  };
  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir;
    if (j < 0 || j >= rules.length) return;
    const acls = [...rules];
    [acls[i], acls[j]] = [acls[j], acls[i]];
    onChange({ ...policy, acls });
  };
  return (
    <div className="space-y-3">
      <Explainer id="access" title="How access rules work">
        <Flow
          steps={[
            { icon: <Users />, title: "Who", sub: "people, groups, tags, devices" },
            { icon: <ShieldCheck />, title: "May reach", sub: "each rule allows one thing" },
            { icon: <Server />, title: "What", sub: "devices, networks, the internet, ports" },
            { icon: <Ban />, title: "Everything else", sub: "blocked", state: "off" },
          ]}
        />
        <p className="mt-4 text-center text-xs text-ink-3">
          Rules only add access, and their order doesn't matter. To let devices use an exit node, add a rule to <b>The internet (exit nodes)</b>. Use <b>Try access</b> to check a change before you rely on it.
        </p>
      </Explainer>
      <Note>Editing rules here rewrites the policy file and drops comments. Order doesn't change what's allowed, but it helps readability.</Note>
      {rules.length === 0 && <Panel className="px-5 py-8 text-center text-[13px] text-ink-3">No rules yet, so nothing can reach anything.</Panel>}
      {rules.map((r, i) => (
        <Panel key={i} className={cn("px-4 py-3", r.disabled && "opacity-60")}>
          <div className="flex items-start gap-3">
            {!readOnly && (
              <div className="flex flex-col pt-0.5 text-ink-3">
                <button aria-label="Move up" className="hover:text-ink disabled:opacity-30" disabled={i === 0} onClick={() => move(i, -1)}>
                  ▲
                </button>
                <GripVertical className="size-3.5" />
                <button aria-label="Move down" className="hover:text-ink disabled:opacity-30" disabled={i === rules.length - 1} onClick={() => move(i, 1)}>
                  ▼
                </button>
              </div>
            )}
            <div className="min-w-0 flex-1">
              <div className="mb-2 flex flex-wrap items-center gap-2">
                <span className="text-[13px] font-medium">{r.description || r.id || `Rule ${i + 1}`}</span>
                {r.id && r.description && <Mono className="text-xs text-ink-3">{r.id}</Mono>}
                {r.proto && <Badge>{r.proto.toUpperCase()} only</Badge>}
                {r.expires && <Badge tone={new Date(r.expires) < new Date() ? "danger" : "warn"}>{new Date(r.expires) < new Date() ? "Expired" : `Until ${new Date(r.expires).toLocaleDateString()}`}</Badge>}
                {r.disabled && <Badge>Off</Badge>}
              </div>
              <div className="flex flex-wrap items-center gap-1.5">
                {r.src.map((s) => (
                  <Selector key={s} s={s} kind="src" />
                ))}
                <ArrowRight className="mx-1 size-4 text-ink-3" />
                {r.dst.map((s) => (
                  <Selector key={s} s={s} kind="dst" />
                ))}
              </div>
            </div>
            {!readOnly && (
              <div className="flex items-center gap-1">
                <Switch label="Rule enabled" checked={!r.disabled} onCheckedChange={(v) => update(i, { ...r, disabled: !v || undefined })} />
                <Button variant="ghost" size="icon" aria-label="Edit rule" onClick={() => setEditing({ index: i, rule: r })}>
                  <Pencil />
                </Button>
                <Button variant="ghost" size="icon" aria-label="Delete rule" onClick={() => update(i, null)}>
                  <Trash2 />
                </Button>
              </div>
            )}
          </div>
        </Panel>
      ))}
      {!readOnly && (
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => setEditing({ index: rules.length, rule: { action: "accept", src: [], dst: [] } })}>
            <Plus /> Add rule
          </Button>
          <Menu trigger={<Button variant="ghost"><Wand2 /> Start from a template</Button>}>
            {ruleTemplates.map((t) => (
              <MenuItem key={t.label} onSelect={() => setEditing({ index: rules.length, rule: structuredClone(t.rule) })}>
                <span>
                  <span className="block">{t.label}</span>
                  <span className="block text-[11px] text-ink-3">{t.hint}</span>
                </span>
              </MenuItem>
            ))}
          </Menu>
        </div>
      )}
      {editing && <RuleDialog rule={editing.rule} hosts={Object.keys(policy.hosts ?? {})} onClose={() => setEditing(null)} onSave={(r) => (update(editing.index, r), setEditing(null))} />}
    </div>
  );
}

const portPresets: [string, string][] = [
  ["Everything", "*"],
  ["Web", "80,443"],
  ["SSH", "22"],
  ["Remote desktop", "3389"],
  ["File sharing", "445"],
  ["Router web", "80,443,22"],
];

const ruleTemplates: { label: string; hint: string; rule: PolicyRule }[] = [
  { label: "Everyone reaches everything", hint: "Fine for a personal network.", rule: { action: "accept", description: "Everyone reaches everything", src: ["*"], dst: ["*:*"] } },
  { label: "A person or group reaches a home network", hint: "Pick who, then the network.", rule: { action: "accept", description: "Reach the home network", src: [], dst: ["192.168.1.0/24:*"] } },
  { label: "Someone reaches one device", hint: "For example the NAS on its file-sharing port.", rule: { action: "accept", description: "Reach one device", src: [], dst: [] } },
  { label: "Everyone can use the internet through an exit node", hint: "Lets people send their traffic through your server.", rule: { action: "accept", description: "Use exit nodes", src: ["*"], dst: ["autogroup:internet:*"] } },
  { label: "Everyone reaches their own devices only", hint: "A good starting point for shared networks.", rule: { action: "accept", description: "Own devices only", src: ["autogroup:member"], dst: ["autogroup:self:*"] } },
];

function RuleDialog({ rule, hosts, onClose, onSave }: { rule: PolicyRule; hosts: string[]; onClose: () => void; onSave: (r: PolicyRule) => void }) {
  const groups = useSelectorOptions();
  const [r, setR] = useState<PolicyRule>(rule);
  const labels: Record<string, string> = { "*": "Anyone / anything", "autogroup:member": "All members' devices", "autogroup:self": "Their own devices", "autogroup:internet": "The internet (exit nodes)" };
  const toSuggestions = (extra: string[] = []): Suggestion[] => [
    ...extra.map((o) => ({ value: o, label: labels[o], group: "Special" })),
    ...groups.flatMap((g) => g.options.map((o) => ({ value: o, label: labels[o], group: g.label }))),
    ...hosts.map((h) => ({ value: h, group: "Named hosts" })),
  ];
  const srcSuggestions = useMemo(() => toSuggestions(), [groups, hosts]); // eslint-disable-line react-hooks/exhaustive-deps
  const dstSuggestions = useMemo(() => toSuggestions(["autogroup:self", "autogroup:internet"]), [groups, hosts]); // eslint-disable-line react-hooks/exhaustive-deps
  const [dstSel, setDstSel] = useState("");
  const [ports, setPorts] = useState("*");
  const addDst = () => {
    if (!dstSel) return;
    const v = `${dstSel}:${ports || "*"}`;
    if (!r.dst.includes(v)) setR({ ...r, dst: [...r.dst, v] });
    setDstSel("");
    setPorts("*");
  };
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={rule.src.length ? "Edit rule" : "New rule"}
      wide
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" disabled={!r.src.length || !r.dst.length} onClick={() => onSave({ ...r, proto: r.proto || undefined, expires: r.expires || undefined, id: r.id || undefined, description: r.description || undefined })}>
            Apply
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Description">
            <Input value={r.description ?? ""} placeholder="Engineers can SSH to servers" onChange={(e) => setR({ ...r, description: e.target.value })} />
          </Field>
          <Field label="ID" hint="Optional, used in tests and logs.">
            <Input className="font-mono" value={r.id ?? ""} placeholder="eng-ssh" onChange={(e) => setR({ ...r, id: e.target.value })} />
          </Field>
        </div>
        <Field label="Who (sources)" hint="Start typing a person, group, tag, device or address, or pick from the list.">
          <ListEditor values={r.src} onChange={(v) => setR({ ...r, src: v })} placeholder="Everyone, a group, a person, a device…" suggestions={srcSuggestions} />
        </Field>
        <Field label="Can reach (destinations)" hint="Ports: * for all, or a list like 22,80,443 or a range 8000-8100.">
          <div className="space-y-2">
            <div className="flex gap-2">
              <div className="flex-1">
                <SuggestInput value={dstSel} onChange={setDstSel} onPick={setDstSel} onEnter={addDst} suggestions={dstSuggestions} placeholder="A device, group, tag or network (192.168.1.0/24)" aria-label="Destination" mono />
              </div>
              <Input className="w-36 font-mono" value={ports} onChange={(e) => setPorts(e.target.value)} aria-label="Ports" placeholder="22,443 or *" />
              <Button onClick={addDst} disabled={!dstSel.trim()}>
                Add
              </Button>
            </div>
            <div className="flex flex-wrap items-center gap-1.5 text-xs text-ink-3">
              Ports:
              {portPresets.map(([label, value]) => (
                <button key={label} type="button" onClick={() => setPorts(value)} className={cn("rounded-md border px-2 py-0.5 text-[11.5px]", ports === value ? "border-blued bg-blued-soft text-blued" : "border-line bg-surface-2 text-ink-2 hover:border-line-strong")}>
                  {label} <span className="font-mono text-ink-3">{value}</span>
                </button>
              ))}
            </div>
            <ListEditor values={r.dst} onChange={(v) => setR({ ...r, dst: v })} placeholder="or type a full entry: 192.168.1.0/24:445" />
          </div>
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Protocol">
            <Select value={r.proto ?? ""} onChange={(e) => setR({ ...r, proto: e.target.value })}>
              <option value="">Any</option>
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="icmp">ICMP (ping)</option>
            </Select>
          </Field>
          <Field label="Ends at" hint="Leave empty for a permanent rule.">
            <Input type="datetime-local" value={r.expires ? r.expires.slice(0, 16) : ""} onChange={(e) => setR({ ...r, expires: e.target.value ? new Date(e.target.value).toISOString().replace(/\.\d+Z$/, "Z") : "" })} />
          </Field>
        </div>
      </div>
    </Dialog>
  );
}

function TryAccess({ document }: { document?: string }) {
  const devices = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices") });
  const [src, setSrc] = useState("");
  const [dst, setDst] = useState("");
  const [port, setPort] = useState("22");
  const [proto, setProto] = useState("tcp");
  const [res, setRes] = useState<{ decision: { allowed: boolean; reason: string }; src_ip: string; dst_ip: string } | null>(null);
  const [err, setErr] = useState("");
  const names = (devices.data ?? []).filter((d) => d.kind !== "gateway").map((d) => d.name);
  return (
    <Panel>
      <PanelHeader title="Try access" description={document ? "Checks against your unsaved draft." : "Checks against the active rules."} />
      <div className="space-y-4 px-5 py-4">
        <datalist id="device-names">
          {names.map((n) => (
            <option key={n} value={n} />
          ))}
        </datalist>
        <div className="grid gap-3 md:grid-cols-[1fr_1fr_100px_110px_auto] md:items-end">
          <Field label="From">
            <Input list="device-names" placeholder="device name or IP" value={src} onChange={(e) => setSrc(e.target.value)} />
          </Field>
          <Field label="To">
            <Input list="device-names" placeholder="device name or IP" value={dst} onChange={(e) => setDst(e.target.value)} />
          </Field>
          <Field label="Port">
            <Input className="font-mono" value={port} onChange={(e) => setPort(e.target.value)} />
          </Field>
          <Field label="Protocol">
            <Select value={proto} onChange={(e) => setProto(e.target.value)}>
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="icmp">ICMP</option>
            </Select>
          </Field>
          <Button
            variant="primary"
            disabled={!src || !dst}
            onClick={async () => {
              setErr("");
              try {
                setRes(await post("/policy/check", { src, dst, port: Number(port) || 0, proto, document }));
              } catch (e) {
                setRes(null);
                setErr(errMessage(e));
              }
            }}
          >
            <FlaskConical /> Check
          </Button>
        </div>
        {err && <ErrorNote>{err}</ErrorNote>}
        {res && (
          <div className={cn("flex items-center gap-3 rounded-lg border px-4 py-3", res.decision.allowed ? "border-verdigris/30 bg-verdigris-soft" : "border-oxide/30 bg-oxide-soft")}>
            {res.decision.allowed ? <CheckCircle2 className="size-5 text-verdigris" /> : <CircleSlash className="size-5 text-oxide" />}
            <div className="text-[13px]">
              <div className="font-medium">{res.decision.allowed ? "Allowed" : "Blocked"}</div>
              <div className="text-ink-2">
                <Mono>{res.src_ip}</Mono> → <Mono>
                  {res.dst_ip}:{port}
                </Mono>{" "}
                : {res.decision.reason}
              </div>
            </div>
          </div>
        )}
        <p className="text-xs text-ink-3">To keep rules from regressing, add the same checks as tests in the policy file (the "tests" section). Saving fails if any test fails.</p>
      </div>
    </Panel>
  );
}

function HistoryTab({ onLoad }: { onLoad: (doc: string) => void }) {
  const { can } = useSession();
  const qc = useQueryClient();
  const versions = useQuery({ queryKey: ["policy", "versions"], queryFn: () => get<PolicyVersion[]>("/policy/versions") });
  return (
    <Panel>
      <PanelHeader title="Saved versions" description="Every save is kept. Restore an older version if a change went wrong." />
      <ul className="divide-y divide-line">
        {versions.data?.map((v, i) => (
          <li key={v.version} className="flex flex-wrap items-center gap-3 px-5 py-3 text-[13px]">
            <History className="size-4 text-ink-3" />
            <span className="w-24 font-mono">v{v.version}</span>
            <span className="min-w-0 flex-1 truncate">
              {v.comment || <span className="text-ink-3">No comment</span>}
              <span className="ml-2 text-xs text-ink-3">
                {v.created_by} · {relTime(v.created_at)}
              </span>
            </span>
            {i === 0 ? (
              <Badge tone="ok">Active</Badge>
            ) : (
              <>
                <Button size="sm" variant="ghost" onClick={async () => onLoad((await get<PolicyVersion>(`/policy/versions/${v.version}`)).document)}>
                  Open in editor
                </Button>
                {can("manage_net") && (
                  <Button
                    size="sm"
                    onClick={async () => {
                      if (!(await confirmAction({ title: `Restore version ${v.version}?`, description: "It becomes the active policy as a new version. Connected devices update within seconds.", confirm: "Restore" }))) return;
                      try {
                        await post(`/policy/versions/${v.version}/restore`);
                        toast.success(`Version ${v.version} restored`);
                        qc.invalidateQueries({ queryKey: ["policy"] });
                      } catch (e) {
                        toast.error(errMessage(e));
                      }
                    }}
                  >
                    Restore
                  </Button>
                )}
              </>
            )}
          </li>
        ))}
      </ul>
    </Panel>
  );
}

function SaveDialog({ open, onOpenChange, onSave }: { open: boolean; onOpenChange: (v: boolean) => void; onSave: (comment: string) => Promise<void> }) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Save access rules"
      description="Connected devices receive the new rules within seconds."
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            variant="primary"
            loading={busy}
            onClick={async () => {
              setBusy(true);
              await onSave(comment);
              setBusy(false);
              setComment("");
            }}
          >
            Save rules
          </Button>
        </>
      }
    >
      <Field label="What changed?" hint="Shown in the history.">
        <Input autoFocus value={comment} onChange={(e) => setComment(e.target.value)} placeholder="Let contractors reach the staging server until Friday" />
      </Field>
    </Dialog>
  );
}
