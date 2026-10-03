import * as React from "react";
import { ArrowDown, ArrowUp, ChevronLeft, ChevronRight, ChevronsUpDown, Search, X } from "lucide-react";
import { cn, relTime } from "@/lib/utils";
import { Button, Select, Th } from "./ui";

// ---------- debounce ----------

/** Returns value after it stopped changing for delay ms (keeps typing smooth and requests few). */
export function useDebounced<T>(value: T, delay = 250): T {
  const [v, setV] = React.useState(value);
  React.useEffect(() => {
    const t = setTimeout(() => setV(value), delay);
    return () => clearTimeout(t);
  }, [value, delay]);
  return v;
}

// ---------- live clock for "5 minutes ago" ----------

let tickNow = Date.now();
const tickSubs = new Set<() => void>();
let tickTimer: ReturnType<typeof setInterval> | undefined;

function subscribeTick(cb: () => void) {
  tickSubs.add(cb);
  if (!tickTimer) {
    tickTimer = setInterval(() => {
      tickNow = Date.now();
      tickSubs.forEach((f) => f());
    }, 15_000);
  }
  return () => {
    tickSubs.delete(cb);
    if (!tickSubs.size && tickTimer) {
      clearInterval(tickTimer);
      tickTimer = undefined;
    }
  };
}

/** Re-renders the caller every 15 seconds, so relative times don't go stale. */
export function useTick(): number {
  return React.useSyncExternalStore(subscribeTick, () => tickNow);
}

/** "3 minutes ago" that keeps itself current. */
export function Ago({ ts, className }: { ts: number; className?: string }) {
  useTick();
  return <span className={className}>{relTime(ts)}</span>;
}

// ---------- search box ----------

export function SearchInput({ value, onChange, placeholder, className, label }: { value: string; onChange: (v: string) => void; placeholder?: string; className?: string; label?: string }) {
  const ref = React.useRef<HTMLInputElement>(null);
  return (
    <div className={cn("relative w-full sm:w-72", className)}>
      <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-ink-3" />
      <input
        ref={ref}
        type="search"
        value={value}
        placeholder={placeholder}
        aria-label={label ?? placeholder ?? "Search"}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => e.key === "Escape" && value && (e.stopPropagation(), onChange(""))}
        className="h-8.5 w-full rounded-md border border-line bg-surface pl-8 pr-7 text-[13px] text-ink placeholder:text-ink-3 transition-colors hover:border-line-strong focus:border-blued focus:outline-none focus:ring-2 focus:ring-blued/20 [&::-webkit-search-cancel-button]:hidden"
      />
      {value && (
        <button
          type="button"
          aria-label="Clear search"
          onClick={() => {
            onChange("");
            ref.current?.focus();
          }}
          className="absolute right-1.5 top-1/2 -translate-y-1/2 rounded p-1 text-ink-3 hover:bg-sunken hover:text-ink"
        >
          <X className="size-3" />
        </button>
      )}
    </div>
  );
}

// ---------- sorting + paging ----------

export type SortDir = "asc" | "desc";
export interface SortState {
  key: string;
  dir: SortDir;
}

const sizeKey = "gorget.pageSize";
export const pageSizes = [10, 25, 50, 100];

function loadPageSize(fallback: number) {
  try {
    const n = Number(localStorage.getItem(sizeKey));
    if (pageSizes.includes(n)) return n;
  } catch {
    /* storage unavailable */
  }
  return fallback;
}

export interface TableState<T> {
  rows: T[];
  total: number;
  page: number;
  pageCount: number;
  pageSize: number;
  from: number;
  to: number;
  sort: SortState | null;
  setPage: (p: number) => void;
  setPageSize: (n: number) => void;
  toggleSort: (key: string) => void;
}

/**
 * Sorts and pages a list. Sorters compare two rows for one column; clicking a header cycles
 * ascending, descending, and the original order. The page resets when the list shrinks.
 */
export function useTable<T>(all: T[], opts: { sorters?: Record<string, (a: T, b: T) => number>; defaultSort?: SortState; pageSize?: number } = {}): TableState<T> {
  const { sorters = {}, defaultSort, pageSize: initialSize = 25 } = opts;
  const [sort, setSort] = React.useState<SortState | null>(defaultSort ?? null);
  const [page, setPageRaw] = React.useState(1);
  const [pageSize, setPageSizeRaw] = React.useState(() => loadPageSize(initialSize));

  const sorted = React.useMemo(() => {
    if (!sort || !sorters[sort.key]) return all;
    const cmp = sorters[sort.key];
    const out = [...all].sort(cmp);
    return sort.dir === "desc" ? out.reverse() : out;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [all, sort]);

  const total = sorted.length;
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const cur = Math.min(page, pageCount);
  const start = (cur - 1) * pageSize;
  const rows = React.useMemo(() => sorted.slice(start, start + pageSize), [sorted, start, pageSize]);

  return {
    rows,
    total,
    page: cur,
    pageCount,
    pageSize,
    from: total ? start + 1 : 0,
    to: Math.min(start + pageSize, total),
    sort,
    setPage: (p) => setPageRaw(Math.min(Math.max(1, p), pageCount)),
    setPageSize: (n) => {
      setPageSizeRaw(n);
      setPageRaw(1);
      try {
        localStorage.setItem(sizeKey, String(n));
      } catch {
        /* storage unavailable */
      }
    },
    toggleSort: (key) => {
      setPageRaw(1);
      setSort((s) => (s?.key !== key ? { key, dir: "asc" } : s.dir === "asc" ? { key, dir: "desc" } : defaultSort && defaultSort.key !== key ? defaultSort : null));
    },
  };
}

/** Table header cell that sorts when clicked. */
export function SortTh({ label, sortKey, table, className }: { label: React.ReactNode; sortKey: string; table: Pick<TableState<unknown>, "sort" | "toggleSort">; className?: string }) {
  const active = table.sort?.key === sortKey;
  const Icon = !active ? ChevronsUpDown : table.sort!.dir === "asc" ? ArrowUp : ArrowDown;
  return (
    <Th className={className}>
      <button
        type="button"
        onClick={() => table.toggleSort(sortKey)}
        aria-label={`Sort by ${typeof label === "string" ? label : sortKey}`}
        className={cn("inline-flex items-center gap-1 uppercase tracking-wider hover:text-ink", active && "text-ink")}
      >
        {label}
        <Icon className={cn("size-3", !active && "opacity-40")} />
      </button>
    </Th>
  );
}

/** "1–25 of 80", previous/next and a page-size picker. Hidden while everything fits on one small page. */
export function Pagination({ table, noun = "items" }: { table: Pick<TableState<unknown>, "total" | "page" | "pageCount" | "pageSize" | "from" | "to" | "setPage" | "setPageSize">; noun?: string }) {
  if (table.total <= pageSizes[0] && table.pageSize >= pageSizes[0]) {
    return table.total ? <div className="border-t border-line px-4 py-2 text-xs text-ink-3">{table.total} {noun}</div> : null;
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line px-4 py-2.5 text-xs text-ink-2">
      <span aria-live="polite">
        {table.from}–{table.to} of {table.total} {noun}
      </span>
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1.5 text-ink-3">
          Rows
          <Select value={String(table.pageSize)} onChange={(e) => table.setPageSize(Number(e.target.value))} className="h-7 w-16 px-1.5 text-xs" aria-label="Rows per page">
            {pageSizes.map((n) => (
              <option key={n}>{n}</option>
            ))}
          </Select>
        </label>
        <Button size="icon" variant="ghost" aria-label="Previous page" disabled={table.page <= 1} onClick={() => table.setPage(table.page - 1)}>
          <ChevronLeft />
        </Button>
        <span className="min-w-14 text-center tabular-nums">
          {table.page} / {table.pageCount}
        </span>
        <Button size="icon" variant="ghost" aria-label="Next page" disabled={table.page >= table.pageCount} onClick={() => table.setPage(table.page + 1)}>
          <ChevronRight />
        </Button>
      </div>
    </div>
  );
}

/** Case-insensitive match of every word in query against the joined text. */
export function matchesQuery(query: string, ...parts: (string | undefined | null | string[])[]): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  const hay = parts.flat().filter(Boolean).join(" ").toLowerCase();
  return q.split(/\s+/).every((t) => hay.includes(t));
}
