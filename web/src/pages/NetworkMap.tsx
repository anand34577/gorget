import { useMemo } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Background, Controls, Handle, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Cable, Globe, Monitor, Server, Smartphone } from "lucide-react";
import { get } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge, PageHeader, Panel, Skeleton } from "@/components/ui";

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

type DevData = MapNode & Record<string, unknown>;

function icon(n: MapNode) {
  if (n.kind === "gateway") return <Server />;
  if (n.kind === "wireguard") return <Cable />;
  if (n.os === "android") return <Smartphone />;
  if (n.tags.length) return <Server />;
  return <Monitor />;
}

function DeviceNode({ data }: NodeProps<Node<DevData>>) {
  return (
    <div
      className={cn(
        "min-w-36 rounded-[10px_10px_18px_18px] border bg-surface px-3 py-2 shadow-panel",
        data.kind === "gateway" ? "border-blued" : data.online ? "border-verdigris/60" : "border-line",
        !data.active && "opacity-50",
      )}
    >
      <Handle type="target" position={Position.Top} className="!size-1.5 !border-0 !bg-line-strong" />
      <div className="flex items-center gap-2 [&_svg]:size-4">
        <span className={data.online ? "text-verdigris" : "text-ink-3"}>{icon(data)}</span>
        <span className="text-[12.5px] font-medium">{data.name}</span>
      </div>
      <div className="mt-0.5 font-mono text-[11px] text-ink-3">{data.ipv4}</div>
      {(data.exit_node || data.routes.length > 0) && (
        <div className="mt-1 flex flex-wrap gap-1">
          {data.exit_node && (
            <Badge tone="blued">
              <Globe className="size-3" /> exit
            </Badge>
          )}
          {data.routes.map((r) => (
            <Badge key={r} className="font-mono">
              {r}
            </Badge>
          ))}
        </div>
      )}
      <Handle type="source" position={Position.Bottom} className="!size-1.5 !border-0 !bg-line-strong" />
    </div>
  );
}

const nodeTypes = { device: DeviceNode };

export default function NetworkMap() {
  const navigate = useNavigate();
  const { data, isLoading } = useQuery({ queryKey: ["network-map"], queryFn: () => get<MapData>("/network-map"), refetchInterval: 30_000 });

  const { nodes, edges } = useMemo(() => {
    if (!data) return { nodes: [] as Node<DevData>[], edges: [] as Edge[] };
    // Concentric layout: gateway in the middle, native devices on the inner ring, WireGuard apps outside.
    const inner = data.nodes.filter((n) => n.kind === "native");
    const outer = data.nodes.filter((n) => n.kind === "wireguard");
    const pos = new Map<string, { x: number; y: number }>();
    pos.set(data.gateway_id, { x: 0, y: 0 });
    const ring = (list: MapNode[], r: number, offset = 0) =>
      list.forEach((n, i) => {
        const a = (i / Math.max(list.length, 1)) * Math.PI * 2 + offset;
        pos.set(n.id, { x: Math.cos(a) * r, y: Math.sin(a) * r * 0.7 });
      });
    ring(inner, Math.max(260, inner.length * 34));
    ring(outer, Math.max(480, inner.length * 34 + 220), 0.3);
    const nodes: Node<DevData>[] = data.nodes.map((n) => ({ id: n.id, type: "device", position: pos.get(n.id) ?? { x: 0, y: 0 }, data: n as DevData }));
    const edges: Edge[] = data.edges.flatMap((e, i) => {
      const style = { stroke: "var(--line-strong)", strokeWidth: 1.2 };
      if (e.via && e.via !== e.source && e.via !== e.target) {
        return [
          { id: `${i}a`, source: e.source, target: e.via, style: { ...style, strokeDasharray: "4 4" } },
          { id: `${i}b`, source: e.via, target: e.target, style: { ...style, strokeDasharray: "4 4" } },
        ];
      }
      return [{ id: String(i), source: e.source, target: e.target, style }];
    });
    return { nodes, edges };
  }, [data]);

  return (
    <>
      <PageHeader
        title="Network map"
        description="Lines show which devices are allowed to talk to each other. Dashed lines run through the gateway (standard WireGuard apps)."
      />
      <Panel className="h-[70vh] overflow-hidden">
        {isLoading ? (
          <Skeleton className="h-full w-full" />
        ) : (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            fitView
            minZoom={0.2}
            nodesConnectable={false}
            onNodeClick={(_, n) => n.data.kind === "native" && navigate(`/devices/${n.id}`)}
            proOptions={{ hideAttribution: true }}
          >
            <Background gap={24} color="var(--line)" />
            <Controls showInteractive={false} />
          </ReactFlow>
        )}
      </Panel>
    </>
  );
}
