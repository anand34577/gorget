import { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Background, Controls, Handle, MiniMap, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Cable, Globe, LayoutGrid, Monitor, Network, Server, Share2, Smartphone, Wand2, X } from "lucide-react";
import { get } from "@/lib/api";
import { cn, osLabel } from "@/lib/utils";
import { circleLayout, forceLayout, gridInBox, hubLayout, type Pos } from "@/lib/graphLayout";
import { Badge, Button, Checkbox, EmptyState, PageHeader, Panel, Skeleton } from "@/components/ui";
import { Ago, SearchInput } from "@/components/data";

interface MapNode {
  id: string;
  name: string;
  kind: string;
  ipv4: string;
  os: string;
  online: boolean;
  active: boolean;
  exit_node: boolean;
  routes: string[];
  user: string;
  tags: string[];
}

interface MapData {
  nodes: MapNode[];
  edges: { source: string; target: string; via?: string }[];
  gateway_id: string;
}

const NODE_W = 188;
const NODE_H = 66;
const HUB_ID = "__hub";

type View = "groups" | "links";

type DevData = { n: MapNode; dim: boolean; hit: boolean; selected: boolean; links: number } & Record<string, unknown>;
type ClusterData = { label: string; count: number; online: number; tagged: boolean; dim: boolean } & Record<string, unknown>;
type HubData = { name: string; sub: string; online: boolean; dim: boolean; selected: boolean } & Record<string, unknown>;

// One invisible handle in the middle of every node: edges run centre to centre, and the node sits on top.
const centre = "!left-1/2 !top-1/2 !size-1 !-translate-x-1/2 !-translate-y-1/2 !border-0 !opacity-0";
const Handles = () => (
  <>
    <Handle type="target" position={Position.Top} className={centre} isConnectable={false} />
    <Handle type="source" position={Position.Bottom} className={centre} isConnectable={false} />
  </>
);

function deviceIcon(n: MapNode) {
  if (n.kind === "gateway") return <Server />;
  if (n.kind === "wireguard") return <Cable />;
  if (n.os === "android") return <Smartphone />;
  if (n.tags.length) return <Server />;
  return <Monitor />;
}

function DeviceNode({ data }: NodeProps<Node<DevData>>) {
  const n = data.n;
  return (
    <div
      style={{ width: NODE_W, height: NODE_H }}
      className={cn(
        "rounded-[10px_10px_18px_18px] border bg-surface px-3 py-2 shadow-panel transition-[opacity,box-shadow,border-color]",
        data.selected ? "border-blued ring-2 ring-blued/30" : n.online ? "border-verdigris/60" : "border-line",
        data.hit && "ring-2 ring-straw/60",
        (!n.active || data.dim) && "opacity-40",
      )}
    >
      <Handles />
      <div className="flex items-center gap-2 [&_svg]:size-4">
        <span className={n.online ? "text-verdigris" : "text-ink-3"}>{deviceIcon(n)}</span>
        <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium" title={n.name}>
          {n.name}
        </span>
        <span className={cn("size-2 shrink-0 rounded-full", n.online ? "bg-verdigris" : "bg-line-strong")} title={n.online ? "Online" : "Offline"} />
      </div>
      <div className="mt-0.5 flex items-center justify-between gap-2">
        <span className="font-mono text-[11px] text-ink-3">{n.ipv4}</span>
        <span className="flex gap-1">
          {n.exit_node && (
            <Badge tone="blued">
              <Globe className="size-3" /> exit
            </Badge>
          )}
          {n.routes.length > 0 && <Badge className="font-mono">{n.routes.length === 1 ? n.routes[0] : `${n.routes.length} networks`}</Badge>}
          {n.kind === "wireguard" && <Badge>WG</Badge>}
        </span>
      </div>
    </div>
  );
}

function HubNode({ data }: NodeProps<Node<HubData>>) {
  return (
    <div
      className={cn(
        "grid min-w-52 place-items-center rounded-2xl border-2 bg-surface px-5 py-3 text-center shadow-panel transition-opacity",
        data.selected ? "border-blued ring-2 ring-blued/30" : "border-blued/60",
        data.dim && "opacity-40",
      )}
    >
      <Handles />
      <div className="flex items-center gap-2 text-[13px] font-semibold [&_svg]:size-4">
        <span className="text-blued">
          <Server />
        </span>
        {data.name}
      </div>
      <div className="mt-0.5 text-[11px] text-ink-3">{data.sub}</div>
    </div>
  );
}

function ClusterNode({ data, width, height }: NodeProps<Node<ClusterData>>) {
  return (
    <div style={{ width, height }} className={cn("rounded-2xl border border-dashed border-line-strong bg-surface-2/70 transition-opacity", data.dim && "opacity-40")}>
      <Handles />
      <div className="flex items-center gap-2 px-4 pt-2.5 text-[12px] font-medium">
        <span className="truncate" title={data.label}>
          {data.label}
        </span>
        <span className="ml-auto shrink-0 text-[11px] font-normal text-ink-3">
          {data.online}/{data.count} online
        </span>
      </div>
    </div>
  );
}

const nodeTypes = { device: DeviceNode, hub: HubNode, cluster: ClusterNode };

interface Base {
  nodes: Node[];
  edges: { id: string; source: string; target: string; dashed: boolean; online: boolean; kind: "hub" | "link" }[];
  adjacency: Map<string, Set<string>>;
  dense: boolean;
}

export default function NetworkMap() {
  const navigate = useNavigate();
  const [view, setView] = useState<View>("groups");
  const [query, setQuery] = useState("");
  const [onlyOnline, setOnlyOnline] = useState(false);
  const [hideWG, setHideWG] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [seed, setSeed] = useState(0);
  const { data, isLoading, dataUpdatedAt } = useQuery({ queryKey: ["network-map"], queryFn: () => get<MapData>("/network-map"), refetchInterval: 30_000 });

  // Layout: only recomputed when the data, the view or the filters change, never when you select something.
  const base = useMemo<Base | null>(() => {
    if (!data) return null;
    const gw = data.gateway_id;
    const kept = data.nodes.filter((n) => n.id === gw || ((!onlyOnline || n.online) && (!hideWG || n.kind !== "wireguard")));
    const keep = new Set(kept.map((n) => n.id));
    const byId = new Map(kept.map((n) => [n.id, n]));

    // Real device-to-device links; a link through the gateway (standard WireGuard app) is drawn as two.
    const links = new Map<string, { source: string; target: string; dashed: boolean }>();
    const addLink = (a: string, b: string, dashed: boolean) => {
      if (a === b || !keep.has(a) || !keep.has(b)) return;
      const [x, y] = a < b ? [a, b] : [b, a];
      links.set(`${x}|${y}`, { source: x, target: y, dashed });
    };
    for (const e of data.edges) {
      if (e.via && e.via !== e.source && e.via !== e.target) {
        addLink(e.source, e.via, true);
        addLink(e.via, e.target, true);
      } else addLink(e.source, e.target, false);
    }
    const adjacency = new Map<string, Set<string>>();
    for (const l of links.values()) {
      (adjacency.get(l.source) ?? adjacency.set(l.source, new Set()).get(l.source)!).add(l.target);
      (adjacency.get(l.target) ?? adjacency.set(l.target, new Set()).get(l.target)!).add(l.source);
    }
    const devices = kept.filter((n) => n.id !== gw);
    const possible = (devices.length * (devices.length - 1)) / 2;
    const real = [...links.values()].filter((l) => l.source !== gw && l.target !== gw).length;
    const dense = devices.length > 5 && real >= possible * 0.6;
    const gwNode = gw ? byId.get(gw) : undefined;
    const hubData = (): HubData => ({
      name: gwNode ? gwNode.name : "Network",
      sub: gwNode ? `Gateway · ${gwNode.ipv4}` : `${devices.length} devices`,
      online: true,
      dim: false,
      selected: false,
    });

    const nodes: Node[] = [];
    const edges: Base["edges"] = [];

    if (view === "groups") {
      // One box per person (or per tag for servers), around the gateway.
      const groups = new Map<string, MapNode[]>();
      for (const n of devices) {
        const key = n.user || (n.tags[0] ? `Tagged: ${n.tags[0]}` : "Tagged devices");
        (groups.get(key) ?? groups.set(key, []).get(key)!).push(n);
      }
      const boxes = [...groups.entries()]
        .sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]))
        .map(([key, members]) => {
          const g = gridInBox(members.length, { w: NODE_W, h: NODE_H });
          return { key, members: members.sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name)), ...g, id: `g:${key}` };
        });
      const hubSize = { w: 220, h: 64 };
      const placed = hubLayout(boxes.map((b) => ({ id: b.id, w: b.w, h: b.h })), hubSize);
      nodes.push({ id: gw || HUB_ID, type: "hub", position: placed.hub, data: hubData() });
      for (const b of boxes) {
        const p = placed.boxes.get(b.id)!;
        nodes.push({
          id: b.id,
          type: "cluster",
          position: p,
          width: b.w,
          height: b.h,
          selectable: false,
          draggable: true,
          zIndex: 0,
          data: { label: b.key, count: b.members.length, online: b.members.filter((m) => m.online).length, tagged: !b.key.includes("@"), dim: false } satisfies ClusterData,
        });
        b.members.forEach((m, i) => {
          nodes.push({ id: m.id, type: "device", parentId: b.id, extent: "parent", position: b.positions[i], zIndex: 1, data: { n: m, dim: false, hit: false, selected: false, links: adjacency.get(m.id)?.size ?? 0 } satisfies DevData });
        });
        edges.push({ id: `h:${b.id}`, source: gw || HUB_ID, target: b.id, dashed: false, online: b.members.some((m) => m.online), kind: "hub" });
      }
    } else {
      // Real links: force layout when sparse, a ring with faint lines when almost everything reaches everything.
      const lnodes = kept.map((n) => ({ id: n.id, w: n.id === gw ? 220 : NODE_W, h: n.id === gw ? 64 : NODE_H, group: n.user || "~" }));
      const pos: Map<string, Pos> = dense
        ? circleLayout(
            [...lnodes].sort((a, b) => (a.group ?? "").localeCompare(b.group ?? "") || a.id.localeCompare(b.id)).filter((x) => x.id !== gw),
            Math.max(260, devices.length * 30),
          )
        : forceLayout(lnodes, [...links.values()]);
      if (dense && gw) pos.set(gw, { x: -110, y: -32 });
      for (const n of kept) {
        const p = pos.get(n.id) ?? { x: 0, y: 0 };
        if (n.id === gw) nodes.push({ id: n.id, type: "hub", position: p, data: hubData() });
        else nodes.push({ id: n.id, type: "device", position: p, data: { n, dim: false, hit: false, selected: false, links: adjacency.get(n.id)?.size ?? 0 } satisfies DevData });
      }
      let i = 0;
      for (const l of links.values()) {
        const a = byId.get(l.source);
        const b = byId.get(l.target);
        edges.push({ id: `l${i++}`, source: l.source, target: l.target, dashed: l.dashed, online: !!a?.online && !!b?.online, kind: "link" });
      }
    }
    return { nodes, edges, adjacency, dense };
    // seed only forces a recompute ("Re-arrange")
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, view, onlyOnline, hideWG, seed]);

  // Styling that depends on the search and the selection.
  const { nodes, edges } = useMemo(() => {
    if (!base) return { nodes: [] as Node[], edges: [] as Edge[] };
    const q = query.trim().toLowerCase();
    const hitsOf = (n: MapNode) => [n.name, n.ipv4, n.user, n.os, ...n.tags, ...n.routes].join(" ").toLowerCase().includes(q);
    const neighbours = selected ? base.adjacency.get(selected) : undefined;
    const nodes = base.nodes.map((nd) => {
      if (nd.type === "device") {
        const d = nd.data as DevData;
        const hit = !!q && hitsOf(d.n);
        const related = !selected || nd.id === selected || !!neighbours?.has(nd.id);
        return { ...nd, data: { ...d, hit, selected: nd.id === selected, dim: (!!q && !hit) || (view === "links" && !related) } };
      }
      if (nd.type === "hub") {
        const d = nd.data as HubData;
        return { ...nd, data: { ...d, selected: nd.id === selected, dim: view === "links" && !!selected && nd.id !== selected && !neighbours?.has(nd.id) } };
      }
      return nd;
    });
    const faint = base.dense && view === "links";
    const edges: Edge[] = base.edges.map((e) => {
      const touches = !!selected && (e.source === selected || e.target === selected);
      const dimmed = !!selected && !touches && e.kind === "link";
      return {
        id: e.id,
        source: e.source,
        target: e.target,
        type: e.kind === "hub" ? "default" : "straight",
        animated: e.kind === "hub" ? e.online : touches && e.online,
        style: {
          stroke: touches ? "var(--blued)" : e.online ? "var(--verdigris)" : "var(--line-strong)",
          strokeWidth: touches ? 2 : e.kind === "hub" ? 1.6 : 1.2,
          strokeDasharray: e.dashed ? "4 4" : undefined,
          opacity: dimmed ? 0.08 : faint && !touches ? 0.18 : e.online || touches ? 0.85 : 0.6,
        },
      };
    });
    return { nodes, edges };
  }, [base, query, selected, view]);

  const sel = selected && data ? data.nodes.find((n) => n.id === selected) : undefined;
  const peers = sel && base ? [...(base.adjacency.get(sel.id) ?? [])].map((id) => data!.nodes.find((n) => n.id === id)).filter((n): n is MapNode => !!n) : [];
  const total = data?.nodes.filter((n) => n.kind !== "gateway").length ?? 0;
  const online = data?.nodes.filter((n) => n.kind !== "gateway" && n.online).length ?? 0;

  return (
    <>
      <PageHeader
        title="Network map"
        description={
          view === "groups"
            ? "Devices grouped by the person they belong to. Click a device for its details and connections."
            : "Which devices are allowed to talk to each other. Click a device to highlight its connections; dashed lines run through the gateway (standard WireGuard apps)."
        }
        actions={
          <span className="text-[13px] text-ink-3">
            {online} of {total} devices online
          </span>
        }
      />
      <Panel className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3">
          <div className="inline-flex rounded-md border border-line bg-surface-2 p-0.5" role="radiogroup" aria-label="Layout">
            {(
              [
                ["groups", "Groups", <LayoutGrid key="g" className="size-3.5" />],
                ["links", "Connections", <Share2 key="l" className="size-3.5" />],
              ] as const
            ).map(([id, label, icon]) => (
              <button
                key={id}
                role="radio"
                aria-checked={view === id}
                onClick={() => {
                  setView(id);
                  setSelected(null);
                }}
                className={cn("inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium", view === id ? "bg-surface text-ink shadow-sm" : "text-ink-3 hover:text-ink")}
              >
                {icon} {label}
              </button>
            ))}
          </div>
          <SearchInput value={query} onChange={setQuery} placeholder="Find a device, address or person" className="sm:w-64" />
          <Checkbox checked={onlyOnline} onChange={setOnlyOnline} label="Online only" />
          <Checkbox checked={hideWG} onChange={setHideWG} label="Hide WireGuard apps" />
          <Button size="sm" className="ml-auto" onClick={() => setSeed((s) => s + 1)}>
            <Wand2 /> Re-arrange
          </Button>
        </div>
        <div className="relative h-[68vh] min-h-[420px]">
          {isLoading ? (
            <Skeleton className="h-full w-full rounded-none" />
          ) : !data || data.nodes.filter((n) => n.kind !== "gateway").length === 0 ? (
            <EmptyState icon={<Network />} title="No devices to show yet">
              Add a device and it appears here, grouped by owner.
            </EmptyState>
          ) : (
            <ReactFlow
              key={`${view}-${seed}-${onlyOnline}-${hideWG}`}
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              fitView
              fitViewOptions={{ padding: 0.18, maxZoom: 1.1 }}
              minZoom={0.15}
              maxZoom={1.8}
              nodesConnectable={false}
              elementsSelectable
              onNodeClick={(_, n) => n.type !== "cluster" && setSelected((s) => (s === n.id ? null : n.id))}
              onPaneClick={() => setSelected(null)}
              proOptions={{ hideAttribution: true }}
            >
              <Background gap={24} color="var(--line)" />
              <Controls showInteractive={false} />
              <MiniMap pannable zoomable nodeColor={(n) => (n.type === "cluster" ? "transparent" : (n.data as { n?: MapNode })?.n?.online ? "var(--verdigris)" : "var(--line-strong)")} nodeStrokeColor="var(--line-strong)" nodeBorderRadius={4} className="!hidden md:!block" />
            </ReactFlow>
          )}

          {base?.dense && view === "links" && (
            <div className="pointer-events-none absolute left-3 top-3 max-w-sm rounded-md border border-line bg-surface/95 px-3 py-2 text-xs text-ink-2 shadow-panel">
              Almost every device may reach every other, so the lines are faint. Select a device to see just its connections, or switch to <b>Groups</b>.
            </div>
          )}

          {sel && (
            <div className="absolute right-3 top-3 w-72 max-w-[calc(100%-1.5rem)] rounded-lg border border-line bg-surface p-4 shadow-panel" role="dialog" aria-label={`${sel.name} details`}>
              <div className="flex items-start gap-2">
                <span className={cn("mt-1 size-2.5 shrink-0 rounded-full", sel.online ? "bg-verdigris" : "bg-line-strong")} />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-semibold">{sel.name}</div>
                  <div className="text-xs text-ink-3">
                    {sel.online ? "Online" : "Offline"} · {sel.kind === "wireguard" ? "WireGuard app" : sel.kind === "gateway" ? "Gateway" : (osLabel[sel.os] ?? sel.os)}
                  </div>
                </div>
                <Button variant="ghost" size="icon" aria-label="Close" onClick={() => setSelected(null)}>
                  <X />
                </Button>
              </div>
              <dl className="mt-3 space-y-1.5 text-[13px]">
                <div className="flex justify-between gap-3">
                  <dt className="text-ink-3">Address</dt>
                  <dd className="font-mono">{sel.ipv4}</dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-ink-3">Owner</dt>
                  <dd className="truncate">{sel.user || "tagged"}</dd>
                </div>
                {sel.tags.length > 0 && (
                  <div className="flex justify-between gap-3">
                    <dt className="text-ink-3">Tags</dt>
                    <dd className="truncate font-mono text-xs">{sel.tags.join(", ")}</dd>
                  </div>
                )}
                {sel.routes.length > 0 && (
                  <div className="flex justify-between gap-3">
                    <dt className="text-ink-3">Shares</dt>
                    <dd className="truncate font-mono text-xs">{sel.routes.join(", ")}</dd>
                  </div>
                )}
              </dl>
              {peers.length > 0 && (
                <div className="mt-3">
                  <div className="mb-1 text-[11px] uppercase tracking-wider text-ink-3">Connected to {peers.length}</div>
                  <div className="flex max-h-24 flex-wrap gap-1 overflow-y-auto">
                    {peers.map((p) => (
                      <button key={p.id} onClick={() => setSelected(p.id)} className="rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[11.5px] hover:border-line-strong">
                        {p.name}
                      </button>
                    ))}
                  </div>
                </div>
              )}
              {sel.kind !== "gateway" && (
                <Button size="sm" className="mt-3 w-full" onClick={() => navigate(sel.kind === "wireguard" ? "/wireguard" : `/devices/${sel.id}`)}>
                  Open device
                </Button>
              )}
            </div>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1 border-t border-line px-4 py-2 text-[11.5px] text-ink-3">
          <span className="flex items-center gap-1.5">
            <span className="h-0.5 w-4 bg-verdigris" /> both online
          </span>
          <span className="flex items-center gap-1.5">
            <span className="h-0.5 w-4 bg-line-strong" /> an end is offline
          </span>
          <span className="flex items-center gap-1.5">
            <span className="w-4 border-t-2 border-dashed border-line-strong" /> through the gateway
          </span>
          {data && (
            <span className="ml-auto">
              Updated <Ago ts={Math.floor(dataUpdatedAt / 1000)} /> · <Link to="/devices" className="underline-offset-2 hover:underline">device list</Link>
            </span>
          )}
        </div>
      </Panel>
    </>
  );
}
