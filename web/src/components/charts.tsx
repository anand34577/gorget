import { useEffect, useId, useMemo, useRef, useState } from "react";
import { cn } from "@/lib/utils";

/* Small, dependency-free charts in the console's style: hairline grid, 2px lines with
   a faint wash underneath, a crosshair tooltip, and a table view for every chart. */

export interface Series {
  key: string;
  label: string;
  /** CSS colour, normally a var(--series-n) token. */
  color: string;
}

type Point = Record<string, number> & { ts: number };

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [w, setW] = useState(600);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.max(240, Math.floor(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

/** niceTicks returns 3-5 round values from 0 to at least max. */
function niceTicks(max: number): number[] {
  if (max <= 0) return [0, 1];
  const raw = max / 4;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= raw) ?? raw;
  const out: number[] = [];
  for (let v = 0; v <= max + step * 0.999; v += step) out.push(Math.round(v * 1000) / 1000);
  return out;
}

function timeLabel(ts: number, span: number): string {
  const d = new Date(ts * 1000);
  if (span <= 2 * 86400) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

function fullTime(ts: number): string {
  return new Date(ts * 1000).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

/**
 * TimeChart draws one or more series over time on a single y-axis. Values are
 * stacked only when `stacked` is set (for parts of a whole, such as download + upload).
 */
export function TimeChart({
  points,
  series,
  format = (v) => String(Math.round(v)),
  height = 200,
  stacked = false,
  title,
  integer = false,
}: {
  points: Point[];
  series: Series[];
  format?: (v: number) => string;
  height?: number;
  stacked?: boolean;
  title: string;
  integer?: boolean;
}) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const [table, setTable] = useState(false);
  const gid = useId().replace(/:/g, "");
  const pad = { l: 52, r: 12, t: 10, b: 24 };
  const iw = width - pad.l - pad.r;
  const ih = height - pad.t - pad.b;

  const geo = useMemo(() => {
    if (points.length === 0) return null;
    const t0 = points[0].ts;
    const t1 = points[points.length - 1].ts;
    const span = Math.max(t1 - t0, 1);
    const tops = points.map((p) => (stacked ? series.reduce((a, s) => a + (p[s.key] ?? 0), 0) : Math.max(...series.map((s) => p[s.key] ?? 0))));
    let ticks = niceTicks(Math.max(...tops, integer ? 1 : 0));
    if (integer) ticks = [...new Set(ticks.map((t) => Math.round(t)))];
    const ymax = ticks[ticks.length - 1] || 1;
    const x = (ts: number) => pad.l + ((ts - t0) / span) * iw;
    const y = (v: number) => pad.t + ih - (v / ymax) * ih;
    // Lines: for stacked charts each series sits on top of the ones before it.
    const lines = series.map((s, si) => {
      const pts = points.map((p) => {
        const below = stacked ? series.slice(0, si).reduce((a, q) => a + (p[q.key] ?? 0), 0) : 0;
        return { x: x(p.ts), y0: y(below), y1: y(below + (p[s.key] ?? 0)) };
      });
      const line = pts.map((q, i) => `${i ? "L" : "M"}${q.x.toFixed(1)},${q.y1.toFixed(1)}`).join("");
      const area = line + pts.slice().reverse().map((q) => `L${q.x.toFixed(1)},${q.y0.toFixed(1)}`).join("") + "Z";
      return { s, line, area, last: pts[pts.length - 1] };
    });
    const xTicks = Array.from({ length: Math.min(6, points.length) }, (_, i) => points[Math.round((i * (points.length - 1)) / Math.max(1, Math.min(6, points.length) - 1))]);
    return { x, y, ticks, lines, span, xTicks };
  }, [points, series, stacked, iw, ih, integer, pad.l, pad.t]);

  const onMove = (e: React.PointerEvent<SVGRectElement>) => {
    if (!geo) return;
    const rect = (e.target as SVGRectElement).getBoundingClientRect();
    const px = e.clientX - rect.left + pad.l;
    let best = 0;
    let bd = Infinity;
    points.forEach((p, i) => {
      const d = Math.abs(geo.x(p.ts) - px);
      if (d < bd) {
        bd = d;
        best = i;
      }
    });
    setHover(best);
  };

  const hp = hover != null ? points[hover] : null;
  return (
    <figure className="m-0">
      <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1">
        <figcaption className="text-[13px] font-medium text-ink">{title}</figcaption>
        {series.length > 1 && (
          <ul className="flex flex-wrap gap-3 text-xs text-ink-2" aria-label="Legend">
            {series.map((s) => (
              <li key={s.key} className="flex items-center gap-1.5">
                <span className="inline-block h-0.5 w-3 rounded-full" style={{ background: s.color }} aria-hidden />
                {s.label}
              </li>
            ))}
          </ul>
        )}
        <button type="button" className="ml-auto text-xs text-ink-3 underline-offset-2 hover:text-ink hover:underline" onClick={() => setTable(!table)} aria-pressed={table}>
          {table ? "Show chart" : "Show table"}
        </button>
      </div>
      <div ref={ref} className="relative">
        {table ? (
          <ChartTable points={points} series={series} format={format} />
        ) : !geo ? (
          <div className="grid place-items-center text-[13px] text-ink-3" style={{ height }}>
            No data yet. Points appear every five minutes.
          </div>
        ) : (
          <>
            <svg width={width} height={height} role="img" aria-label={`${title}, ${points.length} points`} className="block overflow-visible">
              <defs>
                {geo.lines.map((l) => (
                  <linearGradient key={l.s.key} id={`${gid}-${l.s.key}`} x1="0" x2="0" y1="0" y2="1">
                    <stop offset="0" stopColor={l.s.color} stopOpacity="0.16" />
                    <stop offset="1" stopColor={l.s.color} stopOpacity="0.02" />
                  </linearGradient>
                ))}
              </defs>
              {geo.ticks.map((t) => (
                <g key={t}>
                  <line x1={pad.l} x2={width - pad.r} y1={geo.y(t)} y2={geo.y(t)} stroke="var(--grid)" strokeWidth={1} />
                  <text x={pad.l - 8} y={geo.y(t)} dy="0.32em" textAnchor="end" className="fill-ink-3 text-[11px] tabular-nums">
                    {format(t)}
                  </text>
                </g>
              ))}
              {geo.xTicks.map((p, i) => (
                <text key={i} x={geo.x(p.ts)} y={height - 6} textAnchor={i === 0 ? "start" : i === geo.xTicks.length - 1 ? "end" : "middle"} className="fill-ink-3 text-[11px]">
                  {timeLabel(p.ts, geo.span)}
                </text>
              ))}
              {geo.lines.map((l) => (
                <g key={l.s.key}>
                  <path d={l.area} fill={`url(#${gid}-${l.s.key})`} />
                  <path d={l.line} fill="none" stroke={l.s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
                </g>
              ))}
              {hp && (
                <g pointerEvents="none">
                  <line x1={geo.x(hp.ts)} x2={geo.x(hp.ts)} y1={pad.t} y2={pad.t + ih} stroke="var(--line-strong)" strokeWidth={1} />
                  {geo.lines.map((l, si) => {
                    const below = stacked ? series.slice(0, si).reduce((a, q) => a + (hp[q.key] ?? 0), 0) : 0;
                    return <circle key={l.s.key} cx={geo.x(hp.ts)} cy={geo.y(below + (hp[l.s.key] ?? 0))} r={4} fill={l.s.color} stroke="var(--surface)" strokeWidth={2} />;
                  })}
                </g>
              )}
              <rect x={pad.l} y={pad.t} width={iw} height={ih} fill="transparent" onPointerMove={onMove} onPointerLeave={() => setHover(null)} />
            </svg>
            {hp && (
              <div
                className="pointer-events-none absolute top-1 z-10 min-w-36 rounded-md border border-line bg-surface px-3 py-2 text-xs shadow-panel"
                style={{ left: Math.min(Math.max(geo.x(hp.ts) + 12, 0), width - 170) }}
                role="status"
              >
                <div className="mb-1 text-ink-3">{fullTime(hp.ts)}</div>
                {series.map((s) => (
                  <div key={s.key} className="flex items-center justify-between gap-4">
                    <span className="flex items-center gap-1.5 text-ink-2">
                      <span className="inline-block size-2 rounded-full" style={{ background: s.color }} aria-hidden />
                      {s.label}
                    </span>
                    <span className="font-medium tabular-nums text-ink">{format(hp[s.key] ?? 0)}</span>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </figure>
  );
}

function ChartTable({ points, series, format }: { points: Point[]; series: Series[]; format: (v: number) => string }) {
  return (
    <div className="max-h-64 overflow-auto rounded-md border border-line">
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-surface-2 text-ink-3">
          <tr>
            <th className="px-3 py-1.5 text-left font-medium">Time</th>
            {series.map((s) => (
              <th key={s.key} className="px-3 py-1.5 text-right font-medium">
                {s.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {points
            .slice()
            .reverse()
            .map((p) => (
              <tr key={p.ts}>
                <td className="px-3 py-1 text-ink-2">{fullTime(p.ts)}</td>
                {series.map((s) => (
                  <td key={s.key} className="px-3 py-1 text-right tabular-nums">
                    {format(p[s.key] ?? 0)}
                  </td>
                ))}
              </tr>
            ))}
        </tbody>
      </table>
    </div>
  );
}

/** BarList ranks categories by count: label, a thin bar, and the value. */
export function BarList({
  rows,
  format = (v) => v.toLocaleString(),
  color = "var(--series-1)",
  colorFor,
  empty = "Nothing to show yet.",
  max = 8,
}: {
  rows: { label: string; value: number; hint?: string }[];
  format?: (v: number) => string;
  color?: string;
  colorFor?: (label: string) => string;
  empty?: string;
  max?: number;
}) {
  if (rows.length === 0) return <p className="py-6 text-center text-[13px] text-ink-3">{empty}</p>;
  const shown = rows.slice(0, max);
  const rest = rows.slice(max).reduce((a, r) => a + r.value, 0);
  const list = rest > 0 ? [...shown, { label: "Other", value: rest, hint: `${rows.length - max} more` }] : shown;
  const top = Math.max(...list.map((r) => r.value), 1);
  return (
    <ul className="space-y-2.5">
      {list.map((r) => (
        <li key={r.label} title={r.hint ? `${r.label}: ${format(r.value)} (${r.hint})` : `${r.label}: ${format(r.value)}`}>
          <div className="mb-1 flex items-baseline justify-between gap-3 text-[13px]">
            <span className="truncate text-ink-2">{r.label}</span>
            <span className="font-medium tabular-nums text-ink">{format(r.value)}</span>
          </div>
          <div className="h-1.5 w-full rounded-full bg-sunken">
            <div className="h-full rounded-full" style={{ width: `${Math.max((r.value / top) * 100, 2)}%`, background: colorFor?.(r.label) ?? color }} />
          </div>
        </li>
      ))}
    </ul>
  );
}

/** Sparkline is a tiny single-series trend for stat tiles (no axes; the tile names it). */
export function Sparkline({ values, color = "var(--series-1)", className }: { values: number[]; color?: string; className?: string }) {
  const w = 120;
  const h = 32;
  if (values.length < 2) return <svg width={w} height={h} className={className} aria-hidden />;
  const max = Math.max(...values, 1);
  const pts = values.map((v, i) => `${((i / (values.length - 1)) * w).toFixed(1)},${(h - 3 - (v / max) * (h - 6)).toFixed(1)}`);
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className={cn("overflow-visible", className)} aria-hidden>
      <polyline points={pts.join(" ")} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={w} cy={pts[pts.length - 1].split(",")[1]} r={3} fill={color} stroke="var(--surface)" strokeWidth={2} />
    </svg>
  );
}

/** StatTile is a labelled headline number with an optional trend. */
export function StatTile({ label, value, sub, trend, tone }: { label: string; value: React.ReactNode; sub?: React.ReactNode; trend?: number[]; tone?: "ok" | "warn" | "danger" }) {
  return (
    <div className="flex items-end justify-between gap-3 rounded-lg border border-line bg-surface p-4 shadow-panel">
      <div className="min-w-0">
        <div className="text-[12px] font-medium text-ink-3">{label}</div>
        <div className="mt-1 font-display text-[28px] font-semibold leading-none tabular-nums">{value}</div>
        {sub && (
          <div className={cn("mt-1.5 text-xs", tone === "danger" ? "text-oxide" : tone === "warn" ? "text-straw" : tone === "ok" ? "text-verdigris" : "text-ink-3")}>{sub}</div>
        )}
      </div>
      {trend && <Sparkline values={trend} className="shrink-0" />}
    </div>
  );
}
