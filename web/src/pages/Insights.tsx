import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { get } from "@/lib/api";
import type { Stats } from "@/lib/types";
import { countryFlag, countryName, fmtBytes, fmtDate, fmtDuration, osLabel, relTime } from "@/lib/utils";
import { Badge, PageHeader, Panel, PanelHeader, Skeleton, Table, Td, Th } from "@/components/ui";
import { BarList, StatTile, TimeChart } from "@/components/charts";

const ranges = [
  { id: "24h", label: "24 hours" },
  { id: "7d", label: "7 days" },
  { id: "30d", label: "30 days" },
  { id: "90d", label: "90 days" },
];

// Device states are statuses: each keeps its status colour and is always labelled.
const stateColor: Record<string, string> = {
  online: "var(--verdigris)",
  offline: "var(--line-strong)",
  pending: "var(--straw)",
  disabled: "var(--oxide)",
};
const stateLabel: Record<string, string> = { online: "Online", offline: "Offline", pending: "Waiting for approval", disabled: "Disabled" };

export function Insights() {
  const [range, setRange] = useState("24h");
  const q = useQuery({ queryKey: ["stats", range], queryFn: () => get<Stats>(`/stats?range=${range}`), refetchInterval: 60_000 });
  const s = q.data;
  const online = s?.series.map((p) => p.online) ?? [];
  const totalTraffic = (s?.series ?? []).reduce((a, p) => a + p.rx + p.tx, 0);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Insights"
        description="How your network is used: who is connected, how much traffic moves, and where devices connect from."
        actions={
          <div className="inline-flex rounded-md border border-line bg-surface p-0.5" role="radiogroup" aria-label="Time range">
            {ranges.map((r) => (
              <button
                key={r.id}
                role="radio"
                aria-checked={range === r.id}
                onClick={() => setRange(r.id)}
                className={
                  "rounded px-2.5 py-1 text-xs font-medium transition-colors " + (range === r.id ? "bg-blued text-blued-ink" : "text-ink-2 hover:bg-sunken hover:text-ink")
                }
              >
                {r.label}
              </button>
            ))}
          </div>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {s ? (
          <>
            <StatTile label="Online now" value={s.current.online} sub={`of ${s.current.total} devices`} trend={online.slice(-24)} />
            <StatTile label={`Traffic, last ${ranges.find((r) => r.id === range)?.label}`} value={fmtBytes(totalTraffic)} sub="through the tunnel" />
            <StatTile
              label="Shared networks"
              value={`${s.current.routes_up}/${s.current.routes}`}
              sub={s.current.routes === 0 ? "none yet" : s.current.routes_up === s.current.routes ? "all reachable" : "some routers are offline"}
              tone={s.current.routes > 0 && s.current.routes_up < s.current.routes ? "warn" : s.current.routes > 0 ? "ok" : undefined}
            />
            <StatTile
              label="Blocked by security rules"
              value={s.current.blocked}
              sub={s.current.blocked ? "see Devices" : "every device complies"}
              tone={s.current.blocked ? "danger" : "ok"}
            />
          </>
        ) : (
          [0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-24" />)
        )}
      </div>

      <div className="grid gap-6 xl:grid-cols-2">
        <Panel className="p-5">
          {s ? (
            <TimeChart title="Devices online" points={s.series as never} series={[{ key: "online", label: "Online", color: "var(--series-1)" }]} integer />
          ) : (
            <Skeleton className="h-52" />
          )}
        </Panel>
        <Panel className="p-5">
          {s ? (
            <TimeChart
              title="Traffic"
              points={s.series as never}
              stacked
              format={(v) => fmtBytes(v)}
              series={[
                { key: "rx", label: "Download", color: "var(--series-1)" },
                { key: "tx", label: "Upload", color: "var(--series-2)" },
              ]}
            />
          ) : (
            <Skeleton className="h-52" />
          )}
        </Panel>
      </div>

      <div className="grid gap-6 lg:grid-cols-2 xl:grid-cols-4">
        <Panel>
          <PanelHeader title="Device status" />
          <div className="px-5 py-4">
            <BarList
              rows={(s?.breakdown.states ?? []).map((r) => ({ label: stateLabel[r.label] ?? r.label, value: r.count }))}
              colorFor={(l) => stateColor[Object.keys(stateLabel).find((k) => stateLabel[k] === l) ?? ""] ?? "var(--series-1)"}
            />
          </div>
        </Panel>
        <Panel>
          <PanelHeader title="Operating systems" />
          <div className="px-5 py-4">
            <BarList rows={(s?.breakdown.os ?? []).map((r) => ({ label: osLabel[r.label] ?? (r.label === "wireguard" ? "WireGuard app" : r.label), value: r.count }))} />
          </div>
        </Panel>
        <Panel>
          <PanelHeader title="Gorget app versions" />
          <div className="px-5 py-4">
            <BarList rows={(s?.breakdown.versions ?? []).map((r) => ({ label: r.label, value: r.count }))} empty="No Gorget apps connected yet." />
          </div>
        </Panel>
        <Panel>
          <PanelHeader title="Countries" description={s && !s.geo.loaded ? "Needs the country database" : undefined} />
          <div className="px-5 py-4">
            {s && !s.geo.loaded ? (
              <p className="text-[13px] text-ink-2">
                Turn on the free country database in <Link to="/settings?tab=health" className="text-blued underline-offset-2 hover:underline">Settings &gt; Device health</Link> to see where devices connect from.
              </p>
            ) : (
              <BarList rows={(s?.breakdown.countries ?? []).map((r) => ({ label: `${countryFlag(r.label)} ${countryName(r.label)}`.trim(), value: r.count }))} empty="No locations yet." />
            )}
            {s?.geo.attribution && <p className="mt-3 text-[11px] text-ink-3">{s.geo.attribution}</p>}
          </div>
        </Panel>
      </div>

      <div className="grid gap-6 xl:grid-cols-3">
        <Panel>
          <PanelHeader title="Most traffic" description="Since each device last connected" />
          <div className="px-5 py-4">
            <BarList rows={(s?.top ?? []).map((t) => ({ label: t.name, value: t.rx + t.tx, hint: `${fmtBytes(t.rx)} down, ${fmtBytes(t.tx)} up` }))} format={fmtBytes} empty="No traffic recorded yet." />
          </div>
        </Panel>
        <Panel className="xl:col-span-2">
          <PanelHeader title="Recent connections" description="When devices connected, for how long, and from which public address" />
          {s && s.sessions.length === 0 ? (
            <p className="px-5 py-8 text-center text-[13px] text-ink-3">No connections in this period.</p>
          ) : (
            <div className="max-h-96 overflow-auto">
              <Table>
                <thead>
                  <tr>
                    <Th>Device</Th>
                    <Th>Connected</Th>
                    <Th>Duration</Th>
                    <Th>Public address</Th>
                  </tr>
                </thead>
                <tbody>
                  {(s?.sessions ?? []).slice(0, 100).map((x) => (
                    <tr key={x.id}>
                      <Td>
                        <Link to={`/devices/${x.device_id}`} className="font-medium hover:text-blued">
                          {x.device_name}
                        </Link>
                      </Td>
                      <Td className="text-ink-2" title={fmtDate(x.started_at)}>
                        {relTime(x.started_at)}
                      </Td>
                      <Td className="tabular-nums text-ink-2">
                        {x.ended_at ? fmtDuration(x.ended_at - x.started_at) : <Badge tone="ok">Connected</Badge>}
                      </Td>
                      <Td className="font-mono text-xs">
                        {x.public_ip || "—"}
                        {x.country && (
                          <span className="ml-2 font-sans text-ink-3" title={countryName(x.country)}>
                            {countryFlag(x.country)} {x.country}
                          </span>
                        )}
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </div>
          )}
        </Panel>
      </div>
    </div>
  );
}
