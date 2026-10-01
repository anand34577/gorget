import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { get } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge, Input, Mono, PageHeader, Panel, Skeleton } from "@/components/ui";

interface Op {
  summary?: string;
  description?: string;
  tags?: string[];
  requestBody?: { content?: Record<string, { schema?: { properties?: Record<string, { type?: string; description?: string }>; required?: string[] } }> };
  parameters?: { name: string; in: string }[];
  security?: unknown[];
}
interface Spec {
  info: { title: string; description: string };
  paths: Record<string, Record<string, Op>>;
}

const methodTone: Record<string, string> = {
  get: "text-verdigris",
  post: "text-blued",
  put: "text-straw",
  patch: "text-straw",
  delete: "text-oxide",
};

/** A readable reference for the REST API, rendered from the server's OpenAPI document. */
export function ApiDocs() {
  const spec = useQuery({ queryKey: ["openapi"], queryFn: () => get<Spec>("/openapi.json"), staleTime: Infinity });
  const [q, setQ] = useState("");
  const groups = useMemo(() => {
    const out: Record<string, { method: string; path: string; op: Op }[]> = {};
    for (const [path, ops] of Object.entries(spec.data?.paths ?? {})) {
      for (const [method, op] of Object.entries(ops)) {
        const text = `${method} ${path} ${op.summary ?? ""}`.toLowerCase();
        if (q && !text.includes(q.toLowerCase())) continue;
        (out[op.tags?.[0] ?? "Other"] ??= []).push({ method, path, op });
      }
    }
    return out;
  }, [spec.data, q]);
  if (!spec.data) return <Skeleton className="h-96" />;
  return (
    <>
      <PageHeader title="API reference" description={`${spec.data.info.description} The machine-readable document is at /api/v1/openapi.json.`} />
      <div className="relative mb-5 max-w-md">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-ink-3" />
        <Input placeholder="Search endpoints" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <div className="space-y-6">
        {Object.entries(groups).map(([tag, ops]) => (
          <Panel key={tag}>
            <div className="border-b border-line px-5 py-3 text-sm font-semibold">{tag}</div>
            <ul className="divide-y divide-line">
              {ops.map(({ method, path, op }) => {
                const body = op.requestBody?.content?.["application/json"]?.schema?.properties;
                return (
                  <li key={method + path} className="px-5 py-3 text-[13px]">
                    <div className="flex flex-wrap items-center gap-3">
                      <span className={cn("w-14 font-mono text-xs font-semibold uppercase", methodTone[method])}>{method}</span>
                      <Mono className="text-[12.5px]">/api/v1{path}</Mono>
                      {op.security && op.security.length === 0 && <Badge>No sign-in needed</Badge>}
                    </div>
                    <p className="mt-1 pl-[68px] text-ink-2">{op.summary}</p>
                    {op.description && <p className="pl-[68px] text-xs text-ink-3">{op.description}</p>}
                    {body && Object.keys(body).length > 0 && (
                      <p className="mt-1 pl-[68px] font-mono text-xs text-ink-3">{`{ ${Object.keys(body).join(", ")} }`}</p>
                    )}
                  </li>
                );
              })}
            </ul>
          </Panel>
        ))}
      </div>
    </>
  );
}
