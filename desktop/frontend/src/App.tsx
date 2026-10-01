import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, onSendProgress, onSnapshot, type Incoming, type Peer, type SendProgress, type Snapshot, type Status } from "./api";
import { Icon } from "./icons";

type View = "devices" | "files" | "settings";

const bytes = (n: number) => {
  if (n < 1024) return `${n} B`;
  const u = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${u[i]}`;
};

export function App() {
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [view, setView] = useState<View>("devices");
  const [toast, setToast] = useState<{ text: string; ok?: boolean } | null>(null);
  const [sending, setSending] = useState<SendProgress | null>(null);
  const toastTimer = useRef<number | undefined>(undefined);

  useEffect(() => {
    api.snapshot().then(setSnap).catch(() => {});
    const offSnap = onSnapshot(setSnap);
    const offSend = onSendProgress((p) => setSending(p.done ? null : p));
    return () => {
      offSnap();
      offSend();
    };
  }, []);

  const show = useCallback((t: { text: string; ok?: boolean }) => {
    window.clearTimeout(toastTimer.current);
    setToast(t);
    // Errors stay a little longer: they usually need reading.
    toastTimer.current = window.setTimeout(() => setToast(null), t.ok ? 3500 : 7000);
  }, []);

  // Run an action and show its error (or an optional success note) briefly.
  const act = useCallback(
    async (f: () => Promise<unknown>, ok?: string) => {
      try {
        const r = await f();
        // An action may report its own message by returning a string.
        const msg = typeof r === "string" && r ? r : ok;
        if (msg) show({ text: msg, ok: true });
      } catch (e) {
        show({ text: errText(e) });
      }
    },
    [show],
  );

  if (!snap) return <Frame><p className="muted pad">Loading…</p></Frame>;
  if (!snap.daemon || !snap.status) return <NoService error={snap.error} />;
  const st = snap.status;
  const signedIn = !["no_server", "needs_login", "expired"].includes(st.state);

  return (
    <Frame>
      <Header st={st} signedIn={signedIn} view={view} setView={setView} canControl={snap.can_control} act={act} />
      {!snap.can_control && signedIn && (
        <div className="banner">
          <Icon name="lock" /> View only. Ask an administrator to run <code>gorget operator set YOUR_USER</code> on this computer.
        </div>
      )}
      <div className="content">
        {st.state === "no_server" ? (
          <SetServer act={act} />
        ) : st.state === "needs_login" || st.state === "expired" ? (
          <SignIn st={st} act={act} />
        ) : st.state === "blocked" ? (
          <Blocked st={st} />
        ) : view === "files" ? (
          <Files st={st} act={act} />
        ) : view === "settings" ? (
          <Settings st={st} canControl={snap.can_control} act={act} />
        ) : (
          <Devices st={st} canControl={snap.can_control} act={act} />
        )}
      </div>
      {sending && (
        <div className="toast ok" role="status" aria-live="polite">
          Sending {sending.name}… {sending.total > 0 ? Math.floor((sending.sent / sending.total) * 100) + "%" : ""}
          <div className="bar"><span style={{ width: (sending.total > 0 ? (sending.sent / sending.total) * 100 : 0) + "%" }} /></div>
        </div>
      )}
      {toast && !sending && (
        <div className={"toast" + (toast.ok ? " ok" : "")} role={toast.ok ? "status" : "alert"} onClick={() => setToast(null)}>
          {toast.text}
        </div>
      )}
    </Frame>
  );
}

// errText turns an error from a Go call into a sentence for people.
function errText(e: unknown): string {
  const raw = String((e as Error)?.message ?? e ?? "Something went wrong");
  // Wails wraps Go errors as JSON sometimes; show just the message.
  try {
    const j = JSON.parse(raw);
    if (j && typeof j.message === "string") return j.message;
  } catch {
    /* not JSON */
  }
  return raw;
}

// useBusy runs one action at a time, so double clicks don't send it twice.
function useBusy(): [boolean, (f: () => Promise<unknown>) => Promise<void>] {
  const [busy, setBusy] = useState(false);
  const run = useCallback(async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
    } finally {
      setBusy(false);
    }
  }, []);
  return [busy, run];
}

function Frame({ children }: { children: React.ReactNode }) {
  return <main>{children}</main>;
}

function Logo() {
  return (
    <svg width="22" height="22" viewBox="0 0 32 32" aria-hidden>
      <defs>
        <linearGradient id="g" x1="0" x2="1">
          <stop offset="0" stopColor="#3A55B4" />
          <stop offset=".55" stopColor="#7B4FA8" />
          <stop offset="1" stopColor="#C49A3A" />
        </linearGradient>
      </defs>
      <g fill="none" stroke="url(#g)" strokeWidth="3.2" strokeLinecap="round">
        <path d="M6 11a10 10 0 0 0 20 0" />
        <path d="M8.5 16.5a7.5 7.5 0 0 0 15 0" opacity=".8" />
        <path d="M11.5 21.5a4.5 4.5 0 0 0 9 0" opacity=".6" />
      </g>
    </svg>
  );
}

type Act = (f: () => Promise<unknown>, ok?: string) => Promise<void>;

function Header({ st, signedIn, view, setView, canControl, act }: { st: Status; signedIn: boolean; view: View; setView: (v: View) => void; canControl: boolean; act: Act }) {
  const on = st.state === "running";
  const busy = st.state === "connecting";
  const [pending, run] = useBusy();
  return (
    <header>
      <div className="brand">
        <Logo /> <span>Gorget</span>
      </div>
      {signedIn && st.state !== "blocked" && (
        <>
          <nav aria-label="Sections">
            {(["devices", "files", "settings"] as View[]).map((v) => (
              <button key={v} className={"tab" + (view === v ? " on" : "")} onClick={() => setView(v)}>
                {v === "devices" ? "Devices" : v === "files" ? "Files" : "Settings"}
              </button>
            ))}
          </nav>
          {st.state !== "pending_approval" && (
            <button
              className={"power" + (on ? " on" : busy ? " busy" : "")}
              role="switch"
              aria-checked={on || busy}
              aria-label={on || busy ? "Disconnect" : "Connect"}
              title={!canControl ? "View only on this account" : on || busy ? "Disconnect" : "Connect"}
              disabled={!canControl || pending}
              onClick={() => run(() => act(on || busy ? api.down : api.up))}
            >
              <span className="knob" />
            </button>
          )}
        </>
      )}
    </header>
  );
}

function NoService({ error }: { error?: string }) {
  const [os, setOs] = useState("");
  useEffect(() => {
    api.platform().then(setOs).catch(() => {});
  }, []);
  const how =
    os === "windows" ? (
      <>Open <b>Services</b> and start “Gorget VPN”, or run <code>gorget install-service</code> in a terminal opened as Administrator. Reinstalling Gorget also sets it up.</>
    ) : os === "darwin" ? (
      <>In Terminal, run <code>sudo gorget install-service</code>. Reinstalling Gorget also sets it up.</>
    ) : (
      <>In a terminal, run <code>sudo systemctl start gorget</code>, or <code>sudo gorget install-service</code> if it was never installed.</>
    );
  return (
    <Frame>
      <header>
        <div className="brand">
          <Logo /> <span>Gorget</span>
        </div>
      </header>
      <section className="card center big-gap">
        <Icon name="plug" size={32} />
        <h2>The Gorget service isn't running</h2>
        {error && !error.includes("isn't running") && <p className="error">{error}</p>}
        <p className="muted">The background service does the actual networking; this window only controls it.</p>
        <p className="muted">{how}</p>
        <p className="muted small">This window reconnects by itself as soon as the service is up.</p>
      </section>
    </Frame>
  );
}

function SetServer({ act }: { act: Act }) {
  const [url, setUrl] = useState("");
  const [busy, run] = useBusy();
  const clean = url.trim();
  const looksWrong = clean !== "" && (/\s/.test(clean) || !/[.:]/.test(clean.replace(/^https?:\/\//, "")) && clean !== "localhost");
  return (
    <section className="card big-gap">
      <h2>Connect to your server</h2>
      <p className="muted">
        Step 1 of 2. Enter the address of your Gorget server, for example <code>vpn.example.com</code>. Your administrator can tell you what it is; it is the same address you use for the Gorget web console.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          run(() => act(() => api.setServer(clean)));
        }}
      >
        <input autoFocus value={url} onChange={(e) => setUrl(e.target.value)} placeholder="vpn.example.com" aria-label="Server address" aria-invalid={looksWrong} spellCheck={false} autoCapitalize="off" />
        {looksWrong && <p className="muted small">That doesn't look like a server address. It usually has a dot, like vpn.example.com. Continue anyway if you're sure.</p>}
        <button className="primary" disabled={busy || !clean}>{busy ? "Checking the server…" : "Continue"}</button>
      </form>
    </section>
  );
}

function SignIn({ st, act }: { st: Status; act: Act }) {
  const [key, setKey] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [busy, run] = useBusy();
  const waiting = !!st.login_url;
  return (
    <section className="card big-gap">
      <h2>Sign in to {st.network_name || st.server_url}</h2>
      {st.error && <p className="error">{st.error}</p>}
      {waiting ? (
        <>
          <p>Step 2 of 2. Your browser opened the sign-in page. Sign in there, then check that it shows this code:</p>
          <div className="code" aria-label="Confirmation code">{st.login_code}</div>
          <p className="muted small">This window continues by itself once you approve.</p>
          <button onClick={() => act(() => api.openURL(st.login_url!))}>Open the sign-in page again</button>
        </>
      ) : (
        <>
          <p className="muted">Step 2 of 2. Sign in with your account; your browser opens for it.</p>
          <button className="primary" disabled={busy} onClick={() => run(() => act(api.login))}>{busy ? "Opening your browser…" : "Sign in with your browser"}</button>
        </>
      )}
      <p className="muted small">
        <button className="link" aria-expanded={showKey} onClick={() => setShowKey(!showKey)}>Use a setup key instead</button> (for servers and shared machines; your administrator creates it)
      </p>
      {showKey && (
        <form onSubmit={(e) => { e.preventDefault(); run(() => act(() => api.loginSetupKey(key.trim()))); }}>
          <input value={key} onChange={(e) => setKey(e.target.value)} placeholder="Setup key" aria-label="Setup key" autoComplete="off" spellCheck={false} />
          <button disabled={busy || !key.trim()}>{busy ? "Registering…" : "Register"}</button>
        </form>
      )}
      <p className="muted small">
        {st.server_url} · <button className="link" onClick={() => act(() => api.logout())}>change server</button>
      </p>
    </section>
  );
}

function Blocked({ st }: { st: Status }) {
  const reasons = (st.error ?? "").split("\n").filter((l) => l.startsWith("- ")).map((l) => l.slice(2));
  return (
    <section className="card big-gap">
      <div className="blocked-head">
        <Icon name="shield" size={28} />
        <div>
          <h2>This device is blocked</h2>
          <p className="muted">Your organisation requires a few things before it can join the network.</p>
        </div>
      </div>
      <ul className="reasons">
        {(reasons.length ? reasons : [st.error ?? "Security rules are not met"]).map((r) => (
          <li key={r}><Icon name="x" size={14} /> {r}</li>
        ))}
      </ul>
      <p className="muted small">Fix these and Gorget connects by itself within a minute. Nothing else is needed.</p>
    </section>
  );
}

function Devices({ st, canControl, act }: { st: Status; canControl: boolean; act: Act }) {
  const on = st.state === "running";
  const busy = st.state === "connecting";
  const [q, setQ] = useState("");
  const exits = st.peers.filter((p) => p.exit_node);
  const list = useMemo(() => st.peers.filter((p) => !q || (p.name + p.ipv4 + p.user).toLowerCase().includes(q.toLowerCase())), [st.peers, q]);
  return (
    <>
      <section className="card hero">
        <div className={"ring " + (on ? "on" : busy ? "busy" : "off")}><Icon name={on ? "check" : busy ? "dots" : "power"} size={20} /></div>
        <div className="grow">
          <div className="big">{stateLabel(st)}</div>
          {st.self && on && <div className="muted mono">{st.self.name} · {st.self.ipv4}</div>}
          {on && st.relay_transport && <div className="muted small">Relay over {st.relay_transport === "udp" ? "UDP" : "WebSocket"}{st.relay_connected ? "" : " (reconnecting)"}</div>}
          {st.notice && <div className="muted small">{st.notice}</div>}
          {st.error && st.state !== "blocked" && <div className="error small">{st.error}</div>}
          {st.exit_warning && <div className="error small">{st.exit_warning}</div>}
          {st.state === "pending_approval" && (
            <div className="muted small">An administrator must approve this device in the Gorget web console. You connect automatically once they do.</div>
          )}
          {st.state === "stopped" && canControl && <div className="muted small">Turn on the switch at the top to connect.</div>}
        </div>
      </section>

      {exits.length > 0 && st.can_choose_exit && (
        <section className="card">
          <label className="row">
            <span><Icon name="globe" size={15} /> Exit node</span>
            <select disabled={!canControl} value={st.exit_node_id} onChange={(e) => act(() => api.setExitNode(e.target.value))}>
              <option value="">None (private traffic only)</option>
              {exits.map((p) => <option key={p.id} value={p.id} disabled={!p.online}>{p.name}{p.online ? "" : " (offline)"}</option>)}
            </select>
          </label>
        </section>
      )}

      {st.peers.length > 6 && <input className="search" placeholder="Search devices" value={q} onChange={(e) => setQ(e.target.value)} aria-label="Search devices" />}
      <section className="list">
        {list.length === 0 && <p className="muted center pad">{on ? "No other devices yet." : "Connect to see your devices."}</p>}
        {list.map((p) => <PeerRow key={p.id} p={p} inUse={p.id === st.exit_node_id} on={on} act={act} />)}
      </section>
    </>
  );
}

function stateLabel(st: Status): string {
  switch (st.state) {
    case "running": return "Connected";
    case "connecting": return "Connecting…";
    case "stopped": return "Disconnected";
    case "pending_approval": return "Waiting for approval";
    case "disabled": return "Disabled by an administrator";
    default: return st.state;
  }
}

function PeerRow({ p, inUse, on, act }: { p: Peer; inUse: boolean; on: boolean; act: Act }) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState("");
  const path = !p.online ? "offline" : p.direct ? `direct · ${p.latency_ms} ms` : p.rx_bytes + p.tx_bytes > 0 ? "relayed" : "idle";
  const copy = (what: string, text: string) => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(what);
      setTimeout(() => setCopied(""), 1200);
    });
    setOpen(false);
  };
  return (
    <div className={"peer" + (open ? " open" : "")}>
      <button className="peer-main" onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className={"dot " + (p.online ? "on" : "off")} />
        <span className="grow">
          <span className="name">{p.name}{inUse && <em> · exit node</em>}{p.post_quantum && <span className="pq" title="Protected against future quantum computers"><Icon name="lock" size={11} /></span>}</span>
          <span className="muted small">{p.user || p.os}{p.routes.length > 0 ? " · routes " + p.routes.join(", ") : ""}</span>
        </span>
        <span className="right">
          <span className="mono">{copied ? "copied" : p.ipv4}</span>
          <span className={"chip " + (p.direct ? "direct" : "")}>{path}</span>
        </span>
      </button>
      {open && (
        <div className="peer-menu">
          <button onClick={() => copy("ip", p.ipv4)}><Icon name="copy" size={14} /> Copy address</button>
          <button onClick={() => copy("name", p.fqdn || p.name)}><Icon name="copy" size={14} /> Copy name</button>
          {on && p.online && !p.gateway && (
            <button onClick={() => { setOpen(false); act(async () => ((await api.sendFile(p.id)) ? "File sent" : undefined)); }}>
              <Icon name="send" size={14} /> Send a file…
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function Files({ st, act }: { st: Status; act: Act }) {
  const [files, setFiles] = useState<Incoming[] | null>(null);
  const on = st.state === "running";
  const load = useCallback(() => api.files().then(setFiles).catch(() => setFiles([])), []);
  useEffect(() => {
    load();
    if (!on) return;
    // Only poll while connected and while the window is visible.
    const t = setInterval(() => {
      if (!document.hidden) load();
    }, 5000);
    return () => clearInterval(t);
  }, [load, on]);
  return (
    <>
      <section className="card">
        <h2><Icon name="inbox" size={16} /> Received files</h2>
        <p className="muted small">Files other devices of yours send arrive here. They travel straight between your devices, never through the server. To send one, open a device in the Devices tab and choose “Send a file”.</p>
      </section>
      <section className="list">
        {!on && <p className="muted center pad">Connect to receive files.</p>}
        {on && files && files.length === 0 && <p className="muted center pad">Nothing received yet.</p>}
        {(files ?? []).map((f) => (
          <div className="peer" key={f.name}>
            <div className="peer-main static">
              <Icon name="file" size={18} />
              <span className="grow">
                <span className="name">{f.name}</span>
                <span className="muted small">{bytes(f.size)} · from {f.from || "another device"}</span>
              </span>
              <span className="actions">
                <button onClick={() => act(async () => { const dst = await api.saveFile(f.name); load(); return dst ? "Saved" : undefined; })}>Save as…</button>
                <button className="icon" aria-label={`Delete ${f.name}`} onClick={() => act(async () => { await api.deleteFile(f.name); load(); })}><Icon name="trash" size={14} /></button>
              </span>
            </div>
          </div>
        ))}
      </section>
    </>
  );
}

function Settings({ st, canControl, act }: { st: Status; canControl: boolean; act: Act }) {
  const p = st.prefs;
  const saved = (p.advertise_routes ?? []).join(", ");
  const [routes, setRoutes] = useState(saved);
  // Follow changes made elsewhere (the CLI, another window) unless the user is typing.
  const [dirty, setDirty] = useState(false);
  useEffect(() => {
    if (!dirty) setRoutes(saved);
  }, [saved, dirty]);
  const badRoute = routes
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .find((r) => !/^[0-9a-fA-F:.]+\/\d{1,3}$/.test(r));
  const toggle = (label: string, hint: string, key: keyof typeof p, invert = false, locked = false) => {
    const raw = !!p[key];
    const value = invert ? !raw : raw;
    return (
      <label className="row switch">
        <span>
          <span>{label}</span>
          <span className="muted small block">{hint}</span>
        </span>
        <input type="checkbox" role="switch" disabled={!canControl || locked} checked={value || locked} onChange={(e) => act(() => api.setPrefs({ [key]: invert ? !e.target.checked : e.target.checked }))} />
      </label>
    );
  };
  return (
    <>
      <section className="card">
        {toggle("Use Gorget DNS", "Reach devices by name, e.g. laptop." + (st.domain || "gorget.internal"), "use_dns")}
        {toggle("Use shared networks", "Reach networks other devices share with you", "accept_routes")}
        {toggle("Keep local network reachable", "While an exit node is on, printers and local devices still work", "allow_lan")}
        {toggle("Block traffic if the tunnel drops", "Kill switch while using an exit node", "kill_switch", false, st.kill_switch_enforced)}
        {toggle("Post-quantum protection", "Between Gorget devices, also protects against future quantum computers", "no_post_quantum", true)}
        {toggle("Offer this device as an exit node", "An administrator must approve it", "advertise_exit_node")}
      </section>
      <section className="card">
        <label className="col">
          <span>Share these networks</span>
          <span className="muted small">Comma-separated, like 192.168.1.0/24. An administrator must approve them.</span>
          <div className="inline">
            <input value={routes} disabled={!canControl} onChange={(e) => { setRoutes(e.target.value); setDirty(true); }} placeholder="192.168.1.0/24" aria-invalid={!!badRoute} spellCheck={false} />
            <button
              disabled={!canControl || !!badRoute || !dirty}
              onClick={() =>
                act(async () => {
                  await api.setPrefs({ advertise_routes: routes.split(",").map((s) => s.trim()).filter(Boolean) });
                  setDirty(false);
                }, "Saved. An administrator must approve new networks.")
              }
            >
              Save
            </button>
          </div>
          {badRoute && <span className="error small">“{badRoute}” isn't a network. Write it like 192.168.1.0/24.</span>}
        </label>
      </section>
      <section className="card">
        <div className="row"><span>Server</span><span className="mono small">{st.server_url}</span></div>
        <div className="row"><span>Signed in as</span><span className="mono small">{st.self?.user ?? "—"}</span></div>
        <div className="row"><span>Version</span><span className="mono small">{st.version}</span></div>
        <button className="danger" disabled={!canControl} onClick={() => { if (confirm("Sign this device out of the network?")) act(() => api.logout()); }}>Sign out</button>
      </section>
    </>
  );
}
