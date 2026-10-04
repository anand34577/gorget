// Thin typed client for the Gorget REST API (/api/v1).
import { toast } from "sonner";

export class ApiError extends Error {
  status: number;
  code: string;
  details?: unknown;
  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

let csrfToken = "";
export function setCsrf(token: string | undefined) {
  if (token) csrfToken = token;
}

type Listener = (err: ApiError) => void;
const authListeners = new Set<Listener>();
/** Called when the session is gone or the account needs setup steps. */
export function onAuthError(fn: Listener) {
  authListeners.add(fn);
  return () => {
    authListeners.delete(fn);
  };
}

// In-flight request counter: the shell shows a thin progress bar while anything is loading or saving.
let pending = 0;
const pendingListeners = new Set<() => void>();
export const subscribePending = (fn: () => void) => {
  pendingListeners.add(fn);
  return () => {
    pendingListeners.delete(fn);
  };
};
export const pendingCount = () => pending;
const track = (d: number) => {
  pending += d;
  pendingListeners.forEach((l) => l());
};

let offlineShown = false;
function networkFailure(): ApiError {
  if (!offlineShown) {
    offlineShown = true;
    toast.error("Can't reach the server", { id: "offline", description: "Check your connection. This page keeps trying.", duration: 8000 });
    setTimeout(() => (offlineShown = false), 15000);
  }
  return new ApiError(0, "network", "Can't reach the server. Check your connection and try again.");
}

export async function api<T = unknown>(path: string, opts: { method?: string; body?: unknown; raw?: boolean } = {}): Promise<T> {
  const method = opts.method ?? (opts.body !== undefined ? "POST" : "GET");
  const headers: Record<string, string> = { Accept: "application/json" };
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET" && csrfToken) headers["X-CSRF-Token"] = csrfToken;
  track(1);
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, {
      method,
      headers,
      credentials: "same-origin",
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
  } catch {
    throw networkFailure();
  } finally {
    track(-1);
  }
  if (!res.ok) {
    let code = "http_" + res.status;
    let message = res.statusText || "Request failed";
    let details: unknown;
    try {
      const j = await res.json();
      code = j.error?.code ?? code;
      message = j.error?.message ?? message;
      details = j.error?.details;
    } catch {
      /* not JSON */
    }
    const err = new ApiError(res.status, code, message, details);
    if (res.status === 401 || code === "mfa_enrollment_required" || code === "password_change_required") {
      authListeners.forEach((l) => l(err));
    }
    throw err;
  }
  if (opts.raw) return (await res.text()) as T;
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const get = <T,>(p: string) => api<T>(p);
export const post = <T,>(p: string, body: unknown = {}) => api<T>(p, { method: "POST", body });
export const put = <T,>(p: string, body: unknown) => api<T>(p, { method: "PUT", body });
export const patch = <T,>(p: string, body: unknown) => api<T>(p, { method: "PATCH", body });
export const del = <T,>(p: string) => api<T>(p, { method: "DELETE" });

/** Raw POST of a WebAuthn credential (JSON produced by the browser). */
export async function postRaw<T>(path: string, json: string): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken },
    credentials: "same-origin",
    body: json,
  });
  const j = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, j.error?.code ?? "error", j.error?.message ?? "Request failed");
  return j as T;
}

export function errMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}
