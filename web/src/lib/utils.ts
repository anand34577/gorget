import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

/** "3 minutes ago" / "in 2 days" from unix seconds. */
export function relTime(unix: number): string {
  if (!unix) return "never";
  const diff = unix - Date.now() / 1000;
  const abs = Math.abs(diff);
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 31536000],
    ["month", 2592000],
    ["day", 86400],
    ["hour", 3600],
    ["minute", 60],
  ];
  for (const [u, s] of units) if (abs >= s) return rtf.format(Math.round(diff / s), u);
  return abs < 30 ? "just now" : rtf.format(Math.round(diff), "second");
}

export function fmtDate(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1);
  return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${u[i]}`;
}

export const roleLabel: Record<string, string> = {
  owner: "Owner",
  admin: "Admin",
  network_admin: "Network admin",
  auditor: "Auditor",
  user: "Member",
};

export const osLabel: Record<string, string> = {
  linux: "Linux",
  windows: "Windows",
  darwin: "macOS",
  android: "Android",
  wireguard: "WireGuard app",
};

export async function copy(text: string) {
  await navigator.clipboard.writeText(text);
}

export function download(filename: string, text: string, type = "text/plain") {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/** fmtDuration turns seconds into "3 h 12 min" style text. */
export function fmtDuration(sec: number): string {
  if (sec < 60) return `${Math.max(0, Math.round(sec))} s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ${m % 60} min`;
  return `${Math.floor(h / 24)} days`;
}

const regionNames = typeof Intl !== "undefined" && "DisplayNames" in Intl ? new Intl.DisplayNames(undefined, { type: "region" }) : null;

/** countryName gives the readable name for an ISO code ("DE" -> "Germany"). */
export function countryName(code: string): string {
  if (!code) return "";
  try {
    return regionNames?.of(code) ?? code;
  } catch {
    return code;
  }
}

/** countryFlag returns the flag emoji for an ISO code. */
export function countryFlag(code: string): string {
  if (!/^[A-Z]{2}$/.test(code)) return "";
  return String.fromCodePoint(...[...code].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}
