import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { BellRing, Send, ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { errMessage, get, post, put } from "@/lib/api";
import type { EmailNotify, NotificationSettings, PushStatus } from "@/lib/types";
import { useSession } from "@/lib/session";
import { relTime } from "@/lib/utils";
import { Badge, Button, Checkbox, ErrorNote, Field, Input, Note, Panel, PanelHeader, Select, Skeleton, ToggleRow } from "@/components/ui";
import { notifyRows } from "./SettingsEmail";

const loginModes: [NotificationSettings["login"]["mode"], string, string][] = [
  ["new", "New browsers and countries", "Only when someone signs in from a browser or country their account hasn't used before. Quiet, and catches the sign-ins that matter."],
  ["all", "Every sign-in", "Every successful sign-in to the console."],
  ["off", "Off", "No sign-in notifications."],
];

const gotifyPriorities: [number, string][] = [
  [1, "Low (1)"],
  [3, "Normal (3)"],
  [5, "Default (5)"],
  [8, "High (8)"],
  [10, "Maximum (10)"],
];

function randomTopic() {
  const b = new Uint8Array(12);
  crypto.getRandomValues(b);
  return "gorget-" + [...b].map((x) => x.toString(36).padStart(2, "0")).join("").slice(0, 18);
}

/** Push notifications (Gotify, ntfy) and sign-in alerts. Email has its own tab. */
export function NotificationsTab() {
  const { can } = useSession();
  const qc = useQueryClient();
  const canEdit = can("manage_sys");
  const q = useQuery({ queryKey: ["notifications"], queryFn: () => get<{ settings: NotificationSettings; status: PushStatus }>("/settings/notifications"), refetchInterval: 30_000 });
  const mail = useQuery({ queryKey: ["email"], queryFn: () => get<{ settings: { enabled: boolean; host: string } }>("/settings/email") });
  const [draft, setDraft] = useState<NotificationSettings | null>(null);
  const [gotifyToken, setGotifyToken] = useState<string | null>(null);
  const [ntfyToken, setNtfyToken] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [testing, setTesting] = useState("");

  useEffect(() => {
    if (q.data && !draft) setDraft(structuredClone(q.data.settings));
  }, [q.data, draft]);

  if (!draft || !q.data) return <Skeleton className="h-96" />;
  const st = q.data.status;
  const emailOn = !!mail.data?.settings.enabled && !!mail.data.settings.host;
  const dirty = gotifyToken !== null || ntfyToken !== null || JSON.stringify(draft) !== JSON.stringify(q.data.settings);
  const set = (patch: Partial<NotificationSettings>) => setDraft({ ...draft, ...patch });
  const setEvent = (k: keyof EmailNotify, v: boolean) => set({ events: { ...draft.events, [k]: v } });

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      const body: Record<string, unknown> = { ...draft };
      const g = { ...draft.gotify } as Record<string, unknown>;
      const n = { ...draft.ntfy } as Record<string, unknown>;
      delete g.token_set;
      delete n.token_set;
      body.gotify = g;
      body.ntfy = n;
      if (gotifyToken !== null) body.gotify_token = gotifyToken;
      if (ntfyToken !== null) body.ntfy_token = ntfyToken;
      const saved = await put<NotificationSettings>("/settings/notifications", body);
      setDraft(structuredClone(saved));
      setGotifyToken(null);
      setNtfyToken(null);
      toast.success("Notification settings saved");
      qc.invalidateQueries({ queryKey: ["notifications"] });
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const test = async (channel: "gotify" | "ntfy") => {
    setTesting(channel);
    try {
      await post("/settings/notifications/test", { channel });
      toast.success(channel === "gotify" ? "Test sent to Gotify" : "Test sent to ntfy");
    } catch (e) {
      toast.error(errMessage(e), { duration: 10_000 });
    } finally {
      setTesting("");
      qc.invalidateQueries({ queryKey: ["notifications"] });
    }
  };

  const SaveRow = canEdit ? (
    <div className="space-y-3 border-t border-line px-5 py-3">
      {err && <ErrorNote>{err}</ErrorNote>}
      <div className="flex items-center gap-3">
        <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
          Save changes
        </Button>
        {dirty && <span className="text-xs text-ink-3">Unsaved changes</span>}
      </div>
    </div>
  ) : null;

  return (
    <div className="space-y-6">
      <Panel>
        <PanelHeader
          title={
            <span className="flex items-center gap-2">
              <ShieldAlert className="size-4 text-ink-3" /> Sign-in alerts
            </span>
          }
          description="Be told when someone signs in to the console: who, when, from where and with which browser."
        />
        <fieldset disabled={!canEdit} className="space-y-4 px-5 py-4">
          <div className="space-y-2" role="radiogroup" aria-label="When to send sign-in alerts">
            {loginModes.map(([m, title, hint]) => (
              <label key={m} className={"flex cursor-pointer gap-3 rounded-lg border p-3 " + (draft.login.mode === m ? "border-blued bg-blued-soft" : "border-line hover:border-line-strong")}>
                <input type="radio" name="login-mode" className="mt-0.5 accent-[var(--blued)]" checked={draft.login.mode === m} onChange={() => set({ login: { ...draft.login, mode: m } })} />
                <span>
                  <span className="block text-[13px] font-medium">{title}</span>
                  <span className="block text-xs text-ink-3">{hint}</span>
                </span>
              </label>
            ))}
          </div>
          {draft.login.mode !== "off" && (
            <div className="divide-y divide-line rounded-lg border border-line px-4">
              <ToggleRow title="Email the person whose account was used" description="So they can spot a sign-in that wasn't them. Needs email to be set up." checked={draft.login.to_user} onChange={(v) => set({ login: { ...draft.login, to_user: v } })} />
              <ToggleRow title="Email the administrators too" description="Goes to the notification recipients in Settings > Email (every owner and admin if none are set)." checked={draft.login.to_admins} onChange={(v) => set({ login: { ...draft.login, to_admins: v } })} />
              <ToggleRow title="Send to Gotify and ntfy" description="A push message on your phone, when a channel below is switched on." checked={draft.login.to_push} onChange={(v) => set({ login: { ...draft.login, to_push: v } })} />
            </div>
          )}
          {draft.login.mode !== "off" && (draft.login.to_user || draft.login.to_admins) && !emailOn && (
            <Note tone="warn">
              Email isn't switched on, so no email will be sent. Set it up under <Link to="/settings?tab=email" className="underline">Email</Link>.
            </Note>
          )}
          <p className="text-xs text-ink-3">The country comes from the local country database (Settings &gt; Device health); nothing is looked up online.</p>
        </fieldset>
        {SaveRow}
      </Panel>

      <Panel>
        <PanelHeader
          title={
            <span className="flex items-center gap-2">
              <BellRing className="size-4 text-ink-3" /> Gotify
              {draft.gotify.enabled && draft.gotify.url ? <Badge tone="ok">On</Badge> : <Badge>Off</Badge>}
            </span>
          }
          description="Push notifications to your own Gotify server (https://gotify.net). Create an application in Gotify and paste its token."
        />
        <fieldset disabled={!canEdit} className="space-y-4 px-5 py-4">
          <ToggleRow title="Send notifications to Gotify" checked={draft.gotify.enabled} onChange={(v) => set({ gotify: { ...draft.gotify, enabled: v } })} />
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Server address" hint="Where Gotify runs, for example https://gotify.example.com">
              <Input value={draft.gotify.url} placeholder="https://gotify.example.com" className="font-mono" spellCheck={false} onChange={(e) => set({ gotify: { ...draft.gotify, url: e.target.value.trim() } })} />
            </Field>
            <Field label="Application token" hint={draft.gotify.token_set && gotifyToken === null ? "Saved. Type to replace it." : "Stored encrypted with the server's master key; never shown again."}>
              <Input type="password" autoComplete="new-password" value={gotifyToken ?? ""} placeholder={draft.gotify.token_set ? "••••••••" : ""} onChange={(e) => setGotifyToken(e.target.value)} />
            </Field>
            <Field label="Priority" hint="Important events (an unrecognised sign-in, a blocked device) are raised to at least 8.">
              <Select value={String(draft.gotify.priority)} onChange={(e) => set({ gotify: { ...draft.gotify, priority: Number(e.target.value) } })}>
                {gotifyPriorities.map(([n, l]) => (
                  <option key={n} value={n}>
                    {l}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <Checkbox checked={draft.gotify.skip_verify} onChange={(v) => set({ gotify: { ...draft.gotify, skip_verify: v } })} label="Accept a self-signed certificate (only for your own server)" />
        </fieldset>
        <div className="flex flex-wrap items-center gap-3 border-t border-line px-5 py-3">
          {canEdit && (
            <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
              Save changes
            </Button>
          )}
          <Button loading={testing === "gotify"} disabled={!canEdit || dirty || !q.data.settings.gotify.url || !q.data.settings.gotify.token_set} onClick={() => test("gotify")}>
            <Send /> Send test
          </Button>
          {dirty && <span className="text-xs text-ink-3">Save first: the test uses the saved settings.</span>}
        </div>
      </Panel>

      <Panel>
        <PanelHeader
          title={
            <span className="flex items-center gap-2">
              <BellRing className="size-4 text-ink-3" /> ntfy
              {draft.ntfy.enabled && draft.ntfy.topic ? <Badge tone="ok">On</Badge> : <Badge>Off</Badge>}
            </span>
          }
          description="Push notifications through ntfy (https://ntfy.sh), hosted or self-hosted. Subscribe to the topic in the ntfy app."
        />
        <fieldset disabled={!canEdit} className="space-y-4 px-5 py-4">
          <ToggleRow title="Send notifications to ntfy" checked={draft.ntfy.enabled} onChange={(v) => set({ ntfy: { ...draft.ntfy, enabled: v } })} />
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Server address" hint="https://ntfy.sh, or your own server.">
              <Input value={draft.ntfy.url} placeholder="https://ntfy.sh" className="font-mono" spellCheck={false} onChange={(e) => set({ ntfy: { ...draft.ntfy, url: e.target.value.trim() } })} />
            </Field>
            <Field label="Topic" hint="Anyone who knows the name can read the messages on a public server, so make it hard to guess.">
              <div className="flex gap-2">
                <Input value={draft.ntfy.topic} placeholder="gorget-alerts" className="font-mono" spellCheck={false} onChange={(e) => set({ ntfy: { ...draft.ntfy, topic: e.target.value.trim() } })} />
                <Button type="button" onClick={() => set({ ntfy: { ...draft.ntfy, topic: randomTopic() } })}>
                  Generate
                </Button>
              </div>
            </Field>
            <Field label="Access token (optional)" hint={draft.ntfy.token_set && ntfyToken === null ? "Saved. Type to replace it." : "For a topic that needs a login. Stored encrypted."}>
              <Input type="password" autoComplete="new-password" value={ntfyToken ?? ""} placeholder={draft.ntfy.token_set ? "••••••••" : ""} onChange={(e) => setNtfyToken(e.target.value)} />
            </Field>
            <Field label="Priority">
              <Select value={String(draft.ntfy.priority)} onChange={(e) => set({ ntfy: { ...draft.ntfy, priority: Number(e.target.value) } })}>
                {[
                  [1, "Minimum"],
                  [2, "Low"],
                  [3, "Default"],
                  [4, "High"],
                  [5, "Urgent"],
                ].map(([n, l]) => (
                  <option key={n} value={n}>
                    {l}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <Checkbox checked={draft.ntfy.skip_verify} onChange={(v) => set({ ntfy: { ...draft.ntfy, skip_verify: v } })} label="Accept a self-signed certificate (only for your own server)" />
        </fieldset>
        <div className="flex flex-wrap items-center gap-3 border-t border-line px-5 py-3">
          {canEdit && (
            <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
              Save changes
            </Button>
          )}
          <Button loading={testing === "ntfy"} disabled={!canEdit || dirty || !q.data.settings.ntfy.url || !q.data.settings.ntfy.topic} onClick={() => test("ntfy")}>
            <Send /> Send test
          </Button>
          {dirty && <span className="text-xs text-ink-3">Save first: the test uses the saved settings.</span>}
        </div>
      </Panel>

      <Panel>
        <PanelHeader title="What to push" description="Events sent to Gotify and ntfy. Email has its own list under Settings > Email." />
        <fieldset disabled={!canEdit} className="px-5 py-4">
          <div className="divide-y divide-line rounded-lg border border-line px-4">
            {notifyRows.map(([k, title, hint]) => (
              <ToggleRow key={k} title={title} description={hint || undefined} checked={draft.events[k]} onChange={(v) => setEvent(k, v)} />
            ))}
          </div>
        </fieldset>
        {SaveRow}
        <div className="flex flex-wrap gap-x-6 gap-y-1 border-t border-line px-5 py-3 text-xs text-ink-3">
          <span>Sent since start: {st.sent}</span>
          <span>Failed: {st.failed}</span>
          {st.queued > 0 && <span>Waiting: {st.queued}</span>}
          {st.last_sent > 0 && <span>Last sent {relTime(st.last_sent)}</span>}
        </div>
        {st.last_error && (
          <div className="px-5 pb-4">
            <ErrorNote>
              Last failure {relTime(st.last_error_at)}: {st.last_error}
            </ErrorNote>
          </div>
        )}
      </Panel>
    </div>
  );
}
