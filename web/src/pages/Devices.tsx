import { useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, Download, HeartPulse, KeyRound, MoreHorizontal, Monitor, Plus, Trash2, Waypoints } from "lucide-react";
import { toast } from "sonner";
import { del, errMessage, get, patch, post } from "@/lib/api";
import type { Device, DeviceSession } from "@/lib/types";
import { useSession } from "@/lib/session";
import { cn, countryFlag, countryName, download, fmtBytes, fmtDate, fmtDuration, osLabel, relTime } from "@/lib/utils";
import { Ago, matchesQuery, Pagination, SearchInput, SortTh, useDebounced, useTable, useTick } from "@/components/data";
import {
  Badge,
  Button,
  Checkbox,
  confirmAction,
  CopyButton,
  Dialog,
  EmptyState,
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
  Skeleton,
  StatusDot,
  Table,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Tag,
  Td,
  Th,
  Tip,
} from "@/components/ui";

type Filter = "all" | "online" | "offline" | "pending" | "blocked" | "health";

export function useDeviceActions() {
  const qc = useQueryClient();
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["devices"] });
    qc.invalidateQueries({ queryKey: ["overview"] });
  };
  return {
    refresh,
    update: async (id: string, body: object, okMsg = "Device updated") => {
      try {
        await patch(`/devices/${id}`, body);
        toast.success(okMsg);
        refresh();
        return true;
      } catch (e) {
        toast.error(errMessage(e));
        return false;
      }
    },
    approve: async (d: Device) => {
      try {
        await post(`/devices/${d.id}/approve`);
        toast.success(`${d.name} approved`);
        refresh();
      } catch (e) {
        toast.error(errMessage(e));
      }
    },
    expireKey: async (d: Device) => {
      if (!(await confirmAction({ title: `Sign ${d.name} out?`, description: "The device disconnects and must sign in again before it can rejoin.", confirm: "Sign out device" }))) return;
      try {
        await post(`/devices/${d.id}/expire-key`);
        toast.success(`${d.name} signed out`);
        refresh();
      } catch (e) {
        toast.error(errMessage(e));
      }
    },
    toggleBlock: async (d: Device) => {
      if (d.state === "disabled") {
        try {
          await patch(`/devices/${d.id}`, { state: "active" });
          toast.success(`${d.name} can connect again`);
          refresh();
        } catch (e) {
          toast.error(errMessage(e));
        }
        return;
      }
      if (
        !(await confirmAction({
          title: `Block ${d.name}?`,
          description: "It loses access to the network right away and stays blocked until you unblock it. Its settings and address are kept.",
          confirm: "Block device",
          danger: true,
        }))
      )
        return;
      try {
        await patch(`/devices/${d.id}`, { state: "disabled" });
        toast.success(`${d.name} is blocked`);
        refresh();
      } catch (e) {
        toast.error(errMessage(e));
      }
    },
    remove: async (d: Device) => {
      if (!(await confirmAction({ title: `Remove ${d.name}?`, description: "It disconnects immediately and its address is freed. This cannot be undone.", confirm: "Remove device", danger: true }))) return false;
      try {
        await del(`/devices/${d.id}`);
        toast.success(`${d.name} removed`);
        refresh();
        return true;
      } catch (e) {
        toast.error(errMessage(e));
        return false;
      }
    },
  };
}

function ipKey(ip: string): number {
  const p = ip.split(".").map(Number);
  return p.length === 4 && p.every((n) => Number.isFinite(n)) ? ((p[0] * 256 + p[1]) * 256 + p[2]) * 256 + p[3] : 0;
}

function csvCell(v: unknown) {
  const s = String(v ?? "");
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

function statusRank(d: Device) {
  if (d.state === "pending") return 0;
  if (d.state === "disabled") return 3;
  return d.online ? 1 : 2;
}

export function Devices() {
  const { id } = useParams();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { can } = useSession();
  const manage = can("manage_net");
  const [q, setQ] = useState(params.get("q") ?? "");
  const dq = useDebounced(q, 200);
  const filter = (params.get("filter") as Filter) || "all";
  const [addOpen, setAddOpen] = useState(params.get("add") === "1");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const { data, isLoading } = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices"), refetchInterval: 30_000 });
  const actions = useDeviceActions();
  useTick();

  const list = useMemo(() => {
    return (data ?? [])
      .filter((d) => d.kind !== "wireguard")
      .filter((d) => {
        if (filter === "online") return d.online;
        if (filter === "offline") return !d.online && d.state !== "pending";
        if (filter === "pending") return d.state === "pending";
        if (filter === "blocked") return d.state === "disabled";
        if (filter === "health") return d.posture?.length > 0;
        return true;
      })
      .filter((d) => matchesQuery(dq, d.name, d.hostname, d.ipv4, d.fqdn, d.user_email, d.remote_ip, d.country, d.os, d.tags));
  }, [data, dq, filter]);

  const table = useTable(list, {
    defaultSort: { key: "name", dir: "asc" },
    sorters: {
      name: (a, b) => a.name.localeCompare(b.name),
      address: (a, b) => ipKey(a.ipv4) - ipKey(b.ipv4),
      owner: (a, b) => (a.user_email || "~").localeCompare(b.user_email || "~"),
      status: (a, b) => statusRank(a) - statusRank(b) || b.last_seen_at - a.last_seen_at,
      seen: (a, b) => b.last_seen_at - a.last_seen_at,
    },
  });

  const counts = useMemo(() => {
    const ds = (data ?? []).filter((d) => d.kind !== "wireguard");
    return { all: ds.length, online: ds.filter((d) => d.online).length, offline: ds.filter((d) => !d.online && d.state !== "pending").length, pending: ds.filter((d) => d.state === "pending").length, blocked: ds.filter((d) => d.state === "disabled").length, health: ds.filter((d) => d.posture?.length > 0).length };
  }, [data]);

  const selected = id ? data?.find((d) => d.id === id) : undefined;
  const pageIds = table.rows.filter((d) => d.kind !== "gateway").map((d) => d.id);
  const allOnPage = pageIds.length > 0 && pageIds.every((i) => picked.has(i));
  const chosen = (data ?? []).filter((d) => picked.has(d.id));
  const toggleAll = () =>
    setPicked((p) => {
      const n = new Set(p);
      if (allOnPage) pageIds.forEach((i) => n.delete(i));
      else pageIds.forEach((i) => n.add(i));
      return n;
    });

  const bulk = async (what: "approve" | "block" | "unblock" | "remove") => {
    const targets = chosen.filter((d) => (what === "approve" ? d.state === "pending" : what === "unblock" ? d.state === "disabled" : what === "block" ? d.state !== "disabled" : true));
    if (!targets.length) return toast.info("Nothing to do for the selected devices.");
    if (what === "remove" || what === "block") {
      const ok = await confirmAction({
        title: `${what === "remove" ? "Remove" : "Block"} ${targets.length} device${targets.length === 1 ? "" : "s"}?`,
        description: what === "remove" ? "They disconnect immediately and their addresses are freed. This cannot be undone." : "They lose access to the network right away. You can unblock them later.",
        confirm: what === "remove" ? "Remove devices" : "Block devices",
        danger: true,
      });
      if (!ok) return;
    }
    const results = await Promise.allSettled(
      targets.map((d) => (what === "approve" ? post(`/devices/${d.id}/approve`) : what === "remove" ? del(`/devices/${d.id}`) : patch(`/devices/${d.id}`, { state: what === "block" ? "disabled" : "active" }))),
    );
    const failed = results.filter((r) => r.status === "rejected").length;
    if (failed) toast.error(`${targets.length - failed} done, ${failed} failed`);
    else toast.success(`${targets.length} device${targets.length === 1 ? "" : "s"} updated`);
    setPicked(new Set());
    actions.refresh();
  };

  const exportCsv = () => {
    const head = ["name", "address", "ipv6", "owner", "tags", "os", "client_version", "status", "last_seen", "public_address", "country"];
    const rows = list.map((d) => [d.name, d.ipv4, d.ipv6, d.user_email, d.tags.join(" "), d.os, d.client_version, d.state === "active" ? (d.online ? "online" : "offline") : d.state, d.last_seen_at ? new Date(d.last_seen_at * 1000).toISOString() : "", d.remote_ip, d.country]);
    download("gorget-devices.csv", [head, ...rows].map((r) => r.map(csvCell).join(",")).join("\n"), "text/csv");
  };

  const setFilter = (f: Filter) => {
    const next = new URLSearchParams(params);
    if (f === "all") next.delete("filter");
    else next.set("filter", f);
    setParams(next, { replace: true });
  };

  return (
    <>
      <PageHeader
        title="Devices"
        description="Computers, phones and servers running the Gorget app, plus the built-in gateway. Standard WireGuard apps are listed separately."
        actions={
          <>
            <Button onClick={exportCsv} disabled={!list.length}>
              <Download /> Export
            </Button>
            <Button variant="primary" onClick={() => setAddOpen(true)}>
              <Plus /> Add device
            </Button>
          </>
        }
      />
      <Panel>
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3">
          <div className="flex flex-wrap rounded-md border border-line bg-surface-2 p-0.5" role="group" aria-label="Filter devices">
            {(["all", "online", "offline", "pending", "blocked", "health"] as Filter[]).filter((f) => !["health", "blocked", "pending"].includes(f) || counts[f] > 0 || filter === f).map((f) => (
              <button
                key={f}
                aria-pressed={filter === f}
                onClick={() => setFilter(f)}
                className={cn("rounded px-2.5 py-1 text-xs font-medium capitalize", filter === f ? "bg-surface text-ink shadow-sm" : "text-ink-3 hover:text-ink")}
              >
                {f === "pending" ? "Needs approval" : f === "health" ? "Health issues" : f === "blocked" ? "Blocked" : f} <span className="ml-0.5 text-ink-3">{counts[f]}</span>
              </button>
            ))}
          </div>
          <SearchInput value={q} onChange={setQ} placeholder="Name, address, owner, country or tag" label="Search devices" className="ml-auto" />
        </div>
        {manage && picked.size > 0 && (
          <div className="flex flex-wrap items-center gap-2 border-b border-line bg-blued-soft px-4 py-2 text-[13px]" role="toolbar" aria-label="Actions for selected devices">
            <span className="font-medium">{picked.size} selected</span>
            {chosen.some((d) => d.state === "pending") && (
              <Button size="sm" onClick={() => bulk("approve")}>
                <Check /> Approve
              </Button>
            )}
            <Button size="sm" onClick={() => bulk("block")}>
              <Ban /> Block
            </Button>
            {chosen.some((d) => d.state === "disabled") && (
              <Button size="sm" onClick={() => bulk("unblock")}>
                Unblock
              </Button>
            )}
            <Button size="sm" variant="danger-ghost" onClick={() => bulk("remove")}>
              <Trash2 /> Remove
            </Button>
            <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setPicked(new Set())}>
              Clear
            </Button>
          </div>
        )}
        {isLoading ? (
          <div className="space-y-2 p-4">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-10" />
            ))}
          </div>
        ) : list.length === 0 ? (
          <EmptyState icon={<Monitor />} title={counts.all ? "No devices match" : "No devices yet"} action={!counts.all ? <Button variant="primary" onClick={() => setAddOpen(true)}>Add device</Button> : q || filter !== "all" ? <Button onClick={() => { setQ(""); setFilter("all"); }}>Clear search and filters</Button> : undefined}>
            {counts.all ? "Try another filter or search." : "Install the Gorget app on a computer or phone, or connect a server with a setup key."}
          </EmptyState>
        ) : (
          <>
            <Table>
              <thead>
                <tr>
                  {manage && (
                    <Th className="w-9 pr-0">
                      <input type="checkbox" aria-label="Select all devices on this page" className="size-4 accent-[var(--blued)]" checked={allOnPage} onChange={toggleAll} />
                    </Th>
                  )}
                  <SortTh label="Device" sortKey="name" table={table} />
                  <SortTh label="Address" sortKey="address" table={table} />
                  <SortTh label="Owner" sortKey="owner" table={table} className="hidden md:table-cell" />
                  <Th className="hidden xl:table-cell">Connecting from</Th>
                  <SortTh label="Status" sortKey="status" table={table} className="hidden lg:table-cell" />
                  <Th className="w-10" />
                </tr>
              </thead>
              <tbody>
                {table.rows.map((d) => (
                  <tr key={d.id} className={cn("cursor-pointer hover:bg-surface-2", picked.has(d.id) && "bg-blued-soft/50")} onClick={() => navigate(`/devices/${d.id}`)}>
                    {manage && (
                      <Td className="w-9 pr-0" onClick={(e) => e.stopPropagation()}>
                        {d.kind !== "gateway" && (
                          <input
                            type="checkbox"
                            aria-label={`Select ${d.name}`}
                            className="size-4 accent-[var(--blued)]"
                            checked={picked.has(d.id)}
                            onChange={() =>
                              setPicked((p) => {
                                const n = new Set(p);
                                if (!n.delete(d.id)) n.add(d.id);
                                return n;
                              })
                            }
                          />
                        )}
                      </Td>
                    )}
                    <Td>
                      <div className="flex items-center gap-2.5">
                        <StatusDot online={d.online} pending={d.state === "pending"} />
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="font-medium">{d.name}</span>
                            {d.kind === "gateway" && <Badge tone="blued">Gateway</Badge>}
                            {d.state === "pending" && <Badge tone="warn">Needs approval</Badge>}
                            {d.state === "disabled" && <Badge tone="danger">Disabled</Badge>}
                            {d.key_expired && <Badge tone="danger">Signed out</Badge>}
                            {d.posture?.length > 0 && (
                              <Tip content={<span>{d.posture.map((r) => <span key={r} className="block">{r}</span>)}</span>}>
                                <span><Badge tone="danger"><HeartPulse className="mr-1 inline size-3" />Health</Badge></span>
                              </Tip>
                            )}
                            {d.exit_advertised && d.exit_approved && <Badge>Exit node</Badge>}
                            {d.ephemeral && <Badge>Ephemeral</Badge>}
                            {d.tags.map((t) => (
                              <Tag key={t}>{t}</Tag>
                            ))}
                          </div>
                          <div className="text-xs text-ink-3">
                            {osLabel[d.os] ?? d.os} {d.os_version && `· ${d.os_version}`}
                          </div>
                        </div>
                      </div>
                    </Td>
                    <Td>
                      <Mono>{d.ipv4}</Mono>
                      <div className="truncate text-xs text-ink-3">{d.fqdn}</div>
                    </Td>
                    <Td className="hidden md:table-cell text-ink-2">{d.user_email || <span className="text-ink-3">tagged</span>}</Td>
                    <Td className="hidden xl:table-cell">
                      {d.remote_ip ? (
                        <span className="whitespace-nowrap">
                          <Mono className="text-xs">{d.remote_ip}</Mono>
                          {d.country && (
                            <span className="ml-1.5 text-xs text-ink-3" title={countryName(d.country)}>
                              {countryFlag(d.country)} {d.country}
                            </span>
                          )}
                        </span>
                      ) : (
                        <span className="text-xs text-ink-3">—</span>
                      )}
                    </Td>
                    <Td className="hidden lg:table-cell text-ink-2">
                      {d.state === "disabled" ? (
                        <span className="text-oxide">Blocked</span>
                      ) : d.state === "pending" ? (
                        <span className="text-straw">Waiting for approval</span>
                      ) : d.online ? (
                        <span className="font-medium text-verdigris">Online</span>
                      ) : (
                        <span title={d.last_seen_at ? fmtDate(d.last_seen_at) : undefined}>Offline{d.last_seen_at ? <> · <Ago ts={d.last_seen_at} /></> : ""}</span>
                      )}
                    </Td>
                    <Td onClick={(e) => e.stopPropagation()}>
                      {d.kind !== "gateway" && (
                        <Menu trigger={<Button variant="ghost" size="icon" aria-label={`Actions for ${d.name}`}><MoreHorizontal /></Button>}>
                          {d.state === "pending" && manage && (
                            <MenuItem onSelect={() => actions.approve(d)}>
                              <Check /> Approve
                            </MenuItem>
                          )}
                          <MenuItem onSelect={() => navigate(`/devices/${d.id}`)}>
                            <Monitor /> Details
                          </MenuItem>
                          <MenuItem onSelect={() => actions.expireKey(d)}>
                            <KeyRound /> Sign out device
                          </MenuItem>
                          {manage && (
                            <MenuItem onSelect={() => actions.toggleBlock(d)}>
                              <Ban /> {d.state === "disabled" ? "Unblock" : "Block"}
                            </MenuItem>
                          )}
                          <MenuSeparator />
                          <MenuItem danger onSelect={() => actions.remove(d)}>
                            <Trash2 /> Remove
                          </MenuItem>
                        </Menu>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
            <Pagination table={table} noun="devices" />
          </>
        )}
      </Panel>
      {selected && <DeviceDetail device={selected} onClose={() => navigate("/devices")} />}
      <AddDeviceDialog open={addOpen} onOpenChange={setAddOpen} />
    </>
  );
}

function AddDeviceDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const { me } = useSession();
  const navigate = useNavigate();
  const server = me.public_url;
  const host = server.replace(/^https?:\/\//, "");
  const [os, setOs] = useState<"linux" | "windows" | "android">("linux");
  const commands = {
    linux: { label: "Linux or Mac", run: `curl -fsSL ${server}/install.sh | sh`, note: "Run this in a terminal. It installs Gorget, starts the service and opens the sign-in page." },
    windows: { label: "Windows", run: `irm ${server}/install.ps1 | iex`, note: "Run this in PowerShell. Windows asks for administrator approval once, then the sign-in page opens." },
    android: { label: "Android", run: host, note: "Install the Gorget app, enter this server address and sign in." },
  } as const;
  const c = commands[os];
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Add a device" wide>
      <div className="mb-4 rounded-lg border border-blued/25 bg-blued-soft p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-[13px] font-semibold">Install Gorget on a computer or phone</h3>
          <div className="inline-flex rounded-md border border-line bg-surface p-0.5" role="radiogroup" aria-label="Operating system">
            {(Object.keys(commands) as (keyof typeof commands)[]).map((k) => (
              <button key={k} type="button" role="radio" aria-checked={os === k} onClick={() => setOs(k)} className={cn("rounded px-2.5 py-1 text-xs font-medium", os === k ? "bg-blued text-blued-ink" : "text-ink-2 hover:bg-sunken")}>
                {commands[k].label}
              </button>
            ))}
          </div>
        </div>
        <p className="mt-2 text-xs text-ink-2">{c.note} Downloads are checked against the release checksums.</p>
        <div className="mt-2 flex items-center gap-1 rounded border border-line bg-surface px-2 py-1.5">
          <Mono className="flex-1 overflow-x-auto whitespace-nowrap text-[12px]">{c.run}</Mono>
          <CopyButton value={c.run} />
        </div>
        {os !== "android" && (
          <p className="mt-2 text-[11px] text-ink-3">
            For a server with no browser, add a setup key: <span className="font-mono">{os === "windows" ? `$env:GORGET_SETUP_KEY='gsk_…'; ${c.run}` : `curl -fsSL ${server}/install.sh | GORGET_SETUP_KEY=gsk_… sh`}</span>
          </p>
        )}
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        <div className="rounded-lg border border-line p-4">
          <h3 className="text-[13px] font-semibold">Server or router</h3>
          <p className="mt-1 text-xs text-ink-3">Join headless machines with a setup key. They can carry tags and never expire.</p>
          <pre className="mt-3 overflow-x-auto rounded bg-sunken px-2 py-1.5 font-mono text-[11px]">gorget up -server {host} -setup-key gsk_…</pre>
          <Button
            className="mt-3 w-full"
            size="sm"
            onClick={() => {
              onOpenChange(false);
              navigate("/keys?new=1");
            }}
          >
            Create a setup key
          </Button>
        </div>
        <div className="rounded-lg border border-line p-4">
          <h3 className="text-[13px] font-semibold">Any WireGuard app</h3>
          <p className="mt-1 text-xs text-ink-3">Use the official WireGuard app or a router. Connects through the gateway with a QR code or config file.</p>
          <Button
            className="mt-3 w-full"
            size="sm"
            variant="primary"
            onClick={() => {
              onOpenChange(false);
              navigate("/wireguard?new=1");
            }}
          >
            Add WireGuard app
          </Button>
        </div>
      </div>
      <p className="mt-4 text-xs text-ink-3">Devices connect directly to each other and fall back to a relay when a direct path isn't possible.</p>
    </Dialog>
  );
}

function DeviceDetail({ device: d, onClose }: { device: Device; onClose: () => void }) {
  const { can } = useSession();
  const actions = useDeviceActions();
  const manage = can("manage_net");
  const [name, setName] = useState(d.name);
  const [tags, setTags] = useState<string[]>(d.tags);
  const [ip, setIp] = useState(d.ipv4);
  useEffect(() => {
    setName(d.name);
    setTags(d.tags);
    setIp(d.ipv4);
  }, [d]);

  const all = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices") });
  const knownTags = useMemo(() => [...new Set((all.data ?? []).flatMap((x) => x.tags))].sort(), [all.data]);
  const access = useQuery({
    queryKey: ["device-access", d.id],
    queryFn: () =>
      get<{
        active: boolean;
        peers: { id: string; name: string; kind: string; ipv4: string }[];
        inbound: { rule_id: string; src: string[]; dst: string[]; ports: string; proto: string }[];
        outbound: { rule_id: string; src: string[]; dst: string[]; ports: string; proto: string }[];
        can_use_exit: boolean;
      }>(`/devices/${d.id}/access`),
  });

  const save = useMutation({
    mutationFn: async () => {
      const body: Record<string, unknown> = {};
      if (name !== d.name) body.name = name;
      if (manage && JSON.stringify(tags) !== JSON.stringify(d.tags)) body.tags = tags;
      if (manage && ip !== d.ipv4) body.ipv4 = ip;
      if (Object.keys(body).length) await actions.update(d.id, body);
    },
  });

  const editable = d.kind !== "gateway";
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={d.name}
      description={
        <span className="flex flex-wrap items-center gap-2">
          <StatusDot online={d.online} pending={d.state === "pending"} />
          {d.state === "disabled" ? "Blocked" : d.state === "pending" ? "Waiting for approval" : d.online ? "Connected" : <>Offline · last seen <Ago ts={d.last_seen_at} /></>}
          <span className="text-ink-3">·</span>
          <span className="font-mono">{d.fqdn}</span>
        </span>
      }
      wide
    >
      <Tabs defaultValue="details">
        <TabsList>
          <TabsTrigger value="details">Details</TabsTrigger>
          <TabsTrigger value="access">Access</TabsTrigger>
          <TabsTrigger value="history">Connections</TabsTrigger>
          {editable && <TabsTrigger value="manage">Manage</TabsTrigger>}
        </TabsList>
        <TabsContent value="details">
          {d.posture?.length > 0 && (
            <div className="mb-4">
              <Note tone="warn">
                <div className="font-medium">This device doesn't meet your device health rules</div>
                <ul className="mt-1 list-disc pl-5 text-[13px]">
                  {d.posture.map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              </Note>
            </div>
          )}
          {d.state === "pending" && manage && (
            <div className="mb-4">
              <Note tone="warn">
                <div className="flex items-center justify-between gap-3">
                  <span>This device can't connect until it's approved.</span>
                  <Button size="sm" variant="primary" onClick={() => actions.approve(d)}>
                    <Check /> Approve
                  </Button>
                </div>
              </Note>
            </div>
          )}
          <dl className="grid gap-x-8 gap-y-3 text-[13px] sm:grid-cols-2">
            <KV k="IPv4" v={<Mono>{d.ipv4}</Mono>} copy={d.ipv4} />
            <KV k="IPv6" v={<Mono className="text-xs">{d.ipv6}</Mono>} copy={d.ipv6} />
            <KV k="Owner" v={d.user_email || "Tagged device"} />
            <KV k="Operating system" v={`${osLabel[d.os] ?? d.os} ${d.os_version}`} />
            <KV k="Hostname" v={d.hostname || "—"} />
            <KV k="Client version" v={d.client_version || "—"} />
            <KV
              k="Connecting from"
              v={
                d.remote_ip ? (
                  <span>
                    <Mono className="text-xs">{d.remote_ip}</Mono>
                    {d.country && <span className="ml-1.5 text-xs text-ink-3">{countryFlag(d.country)} {countryName(d.country)}</span>}
                  </span>
                ) : (
                  "—"
                )
              }
              copy={d.remote_ip || undefined}
            />
            <KV k="Disk encryption" v={triState(d.disk_encrypted)} />
            <KV k="Firewall" v={triState(d.firewall_on)} />
            <KV k="Added" v={fmtDate(d.created_at)} />
            <KV k="Key expires" v={d.key_expiry_disabled || !d.key_expires_at ? "Never" : `${fmtDate(d.key_expires_at)} (${relTime(d.key_expires_at)})`} />
            <KV k="Endpoints" v={d.endpoints.length ? <Mono className="text-xs">{d.endpoints.join(", ")}</Mono> : "—"} />
            <KV k="Home relay" v={d.home_relay || "—"} />
            <KV k="Traffic" v={`${fmtBytes(d.rx_bytes)} in · ${fmtBytes(d.tx_bytes)} out`} />
            <KV k="WireGuard key" v={<Mono className="text-xs">{d.wg_public_key.slice(0, 22)}…</Mono>} copy={d.wg_public_key} />
          </dl>
          {(d.routes.length > 0 || d.exit_advertised) && (
            <div className="mt-5 rounded-lg border border-line p-4">
              <h3 className="mb-2 flex items-center gap-2 text-[13px] font-semibold">
                <Waypoints className="size-4 text-ink-3" /> Shared networks
              </h3>
              <ul className="space-y-1.5 text-[13px]">
                {d.exit_advertised && (
                  <li className="flex items-center justify-between">
                    <span>Exit node (internet traffic)</span>
                    {d.exit_approved ? <Badge tone="ok">Approved</Badge> : <Badge tone="warn">Needs approval</Badge>}
                  </li>
                )}
                {d.routes.map((r) => (
                  <li key={r.id} className="flex items-center justify-between">
                    <Mono>{r.cidr}</Mono>
                    {!r.advertised ? <Badge>Not advertised</Badge> : r.approved ? <Badge tone="ok">Approved</Badge> : <Badge tone="warn">Needs approval</Badge>}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </TabsContent>
        <TabsContent value="access">
          {access.data && !access.data.active ? (
            <Note tone="warn">This device isn't active, so it has no connections.</Note>
          ) : (
            <div className="space-y-5">
              <section>
                <h3 className="mb-2 text-[13px] font-semibold">Connected to {access.data?.peers.length ?? "…"} devices</h3>
                <div className="flex flex-wrap gap-1.5">
                  {access.data?.peers.map((p) => (
                    <span key={p.id} className="rounded-md border border-line bg-surface-2 px-2 py-0.5 text-xs">
                      {p.name} <span className="font-mono text-ink-3">{p.ipv4}</span>
                    </span>
                  ))}
                  {access.data?.peers.length === 0 && <span className="text-[13px] text-ink-3">No access rule connects this device to anything.</span>}
                </div>
                {access.data?.can_use_exit && <p className="mt-2 text-xs text-ink-3">May send internet traffic through exit nodes.</p>}
              </section>
              <RuleList title="Who can reach this device" rules={access.data?.inbound} />
              <RuleList title="What this device can reach" rules={access.data?.outbound} />
            </div>
          )}
        </TabsContent>
        <TabsContent value="history">
          <SessionHistory deviceId={d.id} />
        </TabsContent>
        {editable && (
          <TabsContent value="manage">
            <div className="space-y-4">
              <Field label="Name" hint="Used in the device's DNS name. Lowercase letters, digits and dashes.">
                <Input value={name} onChange={(e) => setName(e.target.value)} className="font-mono" />
              </Field>
              {manage && (
                <>
                  <Field label="Tags" hint="Tagged devices are owned by the tag instead of a person. Use tags like tag:server in access rules.">
                    <ListEditor values={tags} onChange={setTags} placeholder="tag:server" suggestions={knownTags} validate={(v) => (v.startsWith("tag:") && v.length > 4 ? null : "A tag looks like tag:server")} />
                  </Field>
                  <Field label="IPv4 address" hint="Pin a fixed address inside the network range.">
                    <Input value={ip} onChange={(e) => setIp(e.target.value)} className="font-mono" />
                  </Field>
                  <Checkbox checked={!d.key_expiry_disabled} onChange={(v) => actions.update(d.id, { key_expiry_disabled: !v })} label="Require periodic sign-in (key expiry)" />
                  {d.exit_advertised && <Checkbox checked={d.exit_approved} onChange={(v) => actions.update(d.id, { exit_approved: v })} label="Approve as exit node" />}
                </>
              )}
              <div className="flex flex-wrap gap-2 border-t border-line pt-4">
                <Button variant="primary" loading={save.isPending} onClick={() => save.mutate()}>
                  Save changes
                </Button>
                <Button onClick={() => actions.expireKey(d)}>
                  <KeyRound /> Sign out device
                </Button>
                {manage && (
                  <Button onClick={() => actions.toggleBlock(d)}>
                    <Ban /> {d.state === "disabled" ? "Unblock" : "Block"}
                  </Button>
                )}
                <Button variant="danger-ghost" className="ml-auto" onClick={async () => (await actions.remove(d)) && onClose()}>
                  <Trash2 /> Remove
                </Button>
              </div>
            </div>
          </TabsContent>
        )}
      </Tabs>
    </Dialog>
  );
}

/** SessionHistory lists when the device was connected and from which public address. */
function SessionHistory({ deviceId }: { deviceId: string }) {
  const q = useQuery({ queryKey: ["device-sessions", deviceId], queryFn: () => get<DeviceSession[]>(`/devices/${deviceId}/sessions`), refetchInterval: 30_000 });
  if (q.isLoading) return <Skeleton className="h-32" />;
  const rows = q.data ?? [];
  if (rows.length === 0) return <p className="py-6 text-center text-[13px] text-ink-3">No connections recorded yet. They appear here from the next time the device connects.</p>;
  const ips = new Set(rows.map((r) => r.public_ip).filter(Boolean));
  return (
    <div className="space-y-3">
      <p className="text-xs text-ink-3">
        Last {rows.length} connections from {ips.size} public {ips.size === 1 ? "address" : "addresses"}. Unfamiliar places are worth a look; you can block the device from the Manage tab.
      </p>
      <div className="max-h-80 overflow-auto rounded-lg border border-line">
        <Table>
          <thead>
            <tr>
              <Th>Connected</Th>
              <Th>Duration</Th>
              <Th>Public address</Th>
              <Th className="hidden sm:table-cell">App version</Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id}>
                <Td className="whitespace-nowrap text-ink-2">{fmtDate(r.started_at)}</Td>
                <Td className="whitespace-nowrap tabular-nums">{r.ended_at ? fmtDuration(r.ended_at - r.started_at) : <Badge tone="ok">Now</Badge>}</Td>
                <Td>
                  <Mono className="text-xs">{r.public_ip || "—"}</Mono>
                  {r.country && (
                    <span className="ml-1.5 text-xs text-ink-3" title={countryName(r.country)}>
                      {countryFlag(r.country)} {r.country}
                    </span>
                  )}
                </Td>
                <Td className="hidden text-xs text-ink-3 sm:table-cell">{r.client_version || "—"}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </div>
    </div>
  );
}

function RuleList({ title, rules }: { title: string; rules?: { rule_id: string; src: string[]; dst: string[]; ports: string; proto: string }[] }) {
  return (
    <section>
      <h3 className="mb-2 text-[13px] font-semibold">{title}</h3>
      {!rules?.length ? (
        <p className="text-[13px] text-ink-3">Nothing.</p>
      ) : (
        <ul className="divide-y divide-line rounded-lg border border-line">
          {rules.map((r, i) => (
            <li key={i} className="grid gap-1 px-3 py-2 text-xs sm:grid-cols-[1fr_auto_1fr_auto] sm:items-center sm:gap-3">
              <Mono className="truncate text-xs">{r.src.join(", ")}</Mono>
              <span className="text-ink-3">→</span>
              <Mono className="truncate text-xs">{r.dst.slice(0, 4).join(", ")}{r.dst.length > 4 && ` +${r.dst.length - 4}`}</Mono>
              <span className="text-ink-3">
                {r.proto || "any"} : {r.ports} {r.rule_id && <Tag>{r.rule_id}</Tag>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function KV({ k, v, copy }: { k: string; v: React.ReactNode; copy?: string }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-line pb-2">
      <dt className="text-ink-3">{k}</dt>
      <dd className="flex min-w-0 items-center gap-1 truncate text-right">
        {v}
        {copy && <CopyButton value={copy} className="h-6 px-1" />}
      </dd>
    </div>
  );
}

/** 0 = not reported, 1 = yes, 2 = no. */
function triState(v: number): React.ReactNode {
  if (v === 1) return <Badge tone="ok">On</Badge>;
  if (v === 2) return <Badge tone="danger">Off</Badge>;
  return <span className="text-ink-3">Not reported</span>;
}
