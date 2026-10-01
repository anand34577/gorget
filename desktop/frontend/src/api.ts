import { Call, Events } from "@wailsio/runtime";

export type Peer = {
  id: string;
  name: string;
  fqdn: string;
  ipv4: string;
  ipv6: string;
  os: string;
  user: string;
  online: boolean;
  direct: boolean;
  endpoint?: string;
  latency_ms: number;
  rx_bytes: number;
  tx_bytes: number;
  last_handshake: number;
  exit_node: boolean;
  gateway: boolean;
  routes: string[];
  post_quantum: boolean;
};

export type Prefs = {
  exit_node_id: string;
  allow_lan: boolean;
  accept_routes: boolean;
  use_dns: boolean;
  advertise_routes?: string[];
  advertise_exit_node?: boolean;
  kill_switch: boolean;
  want_running: boolean;
  no_post_quantum?: boolean;
};

export type Status = {
  state: string;
  error?: string;
  notice?: string;
  server_url: string;
  network_name: string;
  domain: string;
  login_url?: string;
  login_code?: string;
  self?: { id: string; name: string; fqdn: string; ipv4: string; ipv6: string; user: string };
  peers: Peer[];
  exit_node_id: string;
  exit_warning?: string;
  prefs: Prefs;
  relay_connected: boolean;
  relay_transport?: string;
  relay_url?: string;
  endpoints: string[];
  can_choose_exit: boolean;
  forced_exit: boolean;
  kill_switch_enforced: boolean;
  version: string;
};

export type Snapshot = { daemon: boolean; can_control: boolean; error?: string; status?: Status };

export type Incoming = { name: string; size: number; from: string; received: string };

// Methods of the Go service (desktop/service.go).
const call = <T = void>(name: string, ...args: unknown[]) => Call.ByName(`main.App.${name}`, ...args) as Promise<T>;

export const api = {
  snapshot: () => call<Snapshot>("Snapshot"),
  setServer: (url: string) => call("SetServer", url),
  login: () => call<{ url: string; code: string }>("Login"),
  loginSetupKey: (key: string) => call("LoginSetupKey", key),
  up: () => call("Up"),
  down: () => call("Down"),
  logout: () => call("Logout"),
  setPrefs: (patch: Partial<Prefs>) => call("SetPrefs", patch),
  setExitNode: (id: string) => call("SetExitNode", id),
  openURL: (url: string) => call("OpenURL", url),
  files: () => call<Incoming[]>("Files"),
  saveFile: (name: string) => call<string>("SaveFile", name),
  deleteFile: (name: string) => call("DeleteFile", name),
  sendFile: (peerID: string) => call<string>("SendFile", peerID),
  platform: () => call<string>("Platform"),
};

export type SendProgress = { name: string; sent: number; total: number; done: boolean };

export function onSendProgress(f: (p: SendProgress) => void): () => void {
  return Events.On("send-progress", (e: { data: SendProgress | SendProgress[] }) => f(Array.isArray(e.data) ? e.data[0] : e.data));
}

export function onSnapshot(f: (s: Snapshot) => void): () => void {
  return Events.On("snapshot", (e: { data: Snapshot | Snapshot[] }) => f(Array.isArray(e.data) ? e.data[0] : e.data));
}
