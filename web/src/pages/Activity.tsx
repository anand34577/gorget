import { useState } from "react";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Download, Search, ShieldCheck, ShieldX } from "lucide-react";
import { get } from "@/lib/api";
import type { AuditEntry } from "@/lib/types";
import { useSession } from "@/lib/session";
import { fmtDate, relTime } from "@/lib/utils";
import { Badge, Button, EmptyState, Input, Mono, PageHeader, Panel, Select, Skeleton, Tip } from "@/components/ui";
import { describeAction } from "./Overview";

const categories = [
  { v: "", l: "All activity" },
  { v: "device", l: "Devices" },
  { v: "wgconfig", l: "WireGuard apps" },
  { v: "policy", l: "Access rules" },
  { v: "user", l: "People" },
  { v: "auth", l: "Sign-ins" },
  { v: "settings", l: "Settings" },
  { v: "route", l: "Routes" },
  { v: "setup_key", l: "Setup keys" },
];

export function ActivityLog() {
  const { isAdmin } = useSession();
  const [q, setQ] = useState("");
  const [cat, setCat] = useState("");
  const [open, setOpen] = useState<number | null>(null);
  const qs = (before?: number) => {
    const p = new URLSearchParams({ limit: "50" });
    if (q) p.set("q", q);
    if (cat) p.set("action", cat);
    if (before) p.set("before", String(before));
    return p.toString();
  };
  const list = useInfiniteQuery({
    queryKey: ["audit", q, cat],
    queryFn: ({ pageParam }) => get<AuditEntry[]>(`/audit?${qs(pageParam)}`),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.length === 50 ? last[last.length - 1].seq : undefined),
  });
  const verify = useQuery({ queryKey: ["audit-verify"], queryFn: () => get<{ ok: boolean; entries: number; first_broken_seq: number }>("/audit/verify"), enabled: isAdmin });
  const entries = list.data?.pages.flat() ?? [];

  return (
    <>
      <PageHeader
        title="Activity log"
        description={isAdmin ? "Every change and sign-in, in order. Entries are chained with hashes, so edits to the stored log are detectable." : "Your own sign-ins and changes."}
        actions={
          isAdmin && (
            <>
              {verify.data &&
                (verify.data.ok ? (
                  <Tip content={`${verify.data.entries} entries verified`}>
                    <span>
                      <Badge tone="ok">
                        <ShieldCheck className="size-3" /> Log intact
                      </Badge>
                    </span>
                  </Tip>
                ) : (
                  <Badge tone="danger">
                    <ShieldX className="size-3" /> Tampering detected at entry {verify.data.first_broken_seq}
                  </Badge>
                ))}
              <Button size="sm" onClick={() => (window.location.href = `/api/v1/audit?format=csv&limit=1000&${qs()}`)}>
                <Download /> Export CSV
              </Button>
            </>
          )
        }
      />
      <Panel>
        <div className="flex flex-wrap gap-3 border-b border-line px-4 py-3">
          <div className="relative w-full sm:w-72">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-ink-3" />
            <Input placeholder="Search people, devices, actions" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <Select className="w-44" value={cat} onChange={(e) => setCat(e.target.value)} aria-label="Category">
            {categories.map((c) => (
              <option key={c.v} value={c.v}>
                {c.l}
              </option>
            ))}
          </Select>
        </div>
        {list.isLoading ? (
          <div className="space-y-2 p-4">
            <Skeleton className="h-8" />
            <Skeleton className="h-8" />
          </div>
        ) : !entries.length ? (
          <EmptyState title="No activity matches" />
        ) : (
          <ul className="divide-y divide-line">
            {entries.map((e) => {
              let details: Record<string, unknown> = {};
              try {
                details = JSON.parse(e.details);
              } catch {
                /* ignore */
              }
              const hasDetails = Object.keys(details).length > 0;
              return (
                <li key={e.seq}>
                  <button className="flex w-full items-center gap-3 px-5 py-2.5 text-left text-[13px] hover:bg-surface-2" onClick={() => setOpen(open === e.seq ? null : e.seq)} aria-expanded={open === e.seq}>
                    <Tip content={fmtDate(e.ts)}>
                      <span className="w-28 shrink-0 text-xs text-ink-3">{relTime(e.ts)}</span>
                    </Tip>
                    <span className="min-w-0 flex-1 truncate">
                      <span className="font-medium">{e.actor || "system"}</span> <span className="text-ink-2">{describeAction(e.action)}</span>{" "}
                      {e.target_name && <Mono>{e.target_name}</Mono>}
                    </span>
                    {e.action.includes("failed") && <Badge tone="danger">Failed</Badge>}
                    <span className="hidden w-32 text-right font-mono text-[11px] text-ink-3 md:block">{e.ip}</span>
                  </button>
                  {open === e.seq && (
                    <div className="border-t border-line bg-surface-2 px-5 py-3 text-xs">
                      <dl className="grid gap-x-6 gap-y-1 sm:grid-cols-[120px_1fr]">
                        <dt className="text-ink-3">Time</dt>
                        <dd>{fmtDate(e.ts)}</dd>
                        <dt className="text-ink-3">Action</dt>
                        <dd className="font-mono">{e.action}</dd>
                        {e.target_type && (
                          <>
                            <dt className="text-ink-3">Target</dt>
                            <dd className="font-mono">
                              {e.target_type} {e.target_id}
                            </dd>
                          </>
                        )}
                        <dt className="text-ink-3">Entry</dt>
                        <dd className="font-mono">#{e.seq}</dd>
                      </dl>
                      {hasDetails && <pre className="mt-2 overflow-x-auto rounded bg-sunken p-2 font-mono text-[11px]">{JSON.stringify(details, null, 2)}</pre>}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
        {list.hasNextPage && (
          <div className="border-t border-line p-3 text-center">
            <Button size="sm" loading={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>
              Load older activity
            </Button>
          </div>
        )}
      </Panel>
    </>
  );
}
