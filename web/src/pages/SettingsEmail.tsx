import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Mail, Send } from "lucide-react";
import { toast } from "sonner";
import { errMessage, get, post, put } from "@/lib/api";
import type { EmailNotify, EmailSettings, MailStatus } from "@/lib/types";
import { useSession } from "@/lib/session";
import { relTime } from "@/lib/utils";
import { Badge, Button, Checkbox, ErrorNote, Field, Input, ListEditor, Note, Panel, PanelHeader, Select, Skeleton, ToggleRow } from "@/components/ui";

const notifyRows: [keyof EmailNotify, string, string][] = [
  ["device_pending", "A device is waiting for approval", "So new devices don't sit unapproved."],
  ["device_added", "A device joins", "Every new device, including approved ones."],
  ["new_country", "A device connects from a new country", "Needs the country database (Settings > Device health)."],
  ["device_blocked", "A device is blocked by health rules", "For example after its firewall was switched off."],
  ["key_expiring", "A device's sign-in is about to expire", "A week before, so it doesn't drop off unexpectedly."],
  ["access_requests", "Someone requests temporary access", ""],
  ["route_advertised", "A device offers a network or exit node", "Networks are only used after approval."],
  ["login_lockout", "An account is locked after wrong passwords", "Can mean someone is guessing passwords."],
];

// Common providers, to save people looking up ports.
const presets: { name: string; host: string; port: number; security: EmailSettings["security"]; hint: string }[] = [
  { name: "Gmail / Google Workspace", host: "smtp.gmail.com", port: 587, security: "starttls", hint: "Use an app password (Google account > Security > App passwords)." },
  { name: "Microsoft 365 / Outlook", host: "smtp.office365.com", port: 587, security: "starttls", hint: "SMTP AUTH must be allowed for the mailbox." },
  { name: "Amazon SES", host: "email-smtp.us-east-1.amazonaws.com", port: 587, security: "starttls", hint: "Use SES SMTP credentials and your region's host." },
  { name: "Brevo", host: "smtp-relay.brevo.com", port: 587, security: "starttls", hint: "Use the SMTP key as the password." },
  { name: "Mailgun", host: "smtp.mailgun.org", port: 587, security: "starttls", hint: "" },
  { name: "Zoho Mail", host: "smtp.zoho.com", port: 465, security: "tls", hint: "Use an app-specific password if 2FA is on." },
];

export function EmailTab() {
  const { can, me } = useSession();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["email"], queryFn: () => get<{ settings: EmailSettings; status: MailStatus }>("/settings/email"), refetchInterval: 30_000 });
  const [draft, setDraft] = useState<EmailSettings | null>(null);
  const [password, setPassword] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [testTo, setTestTo] = useState("");
  const [testing, setTesting] = useState(false);
  const canEdit = can("manage_sys");

  useEffect(() => {
    if (q.data && !draft) setDraft(structuredClone(q.data.settings));
  }, [q.data, draft]);

  if (!draft || !q.data) return <Skeleton className="h-96" />;
  const st = q.data.status;
  const set = (patch: Partial<EmailSettings>) => setDraft({ ...draft, ...patch });
  const setNotify = (k: keyof EmailNotify, v: boolean) => setDraft({ ...draft, notify: { ...draft.notify, [k]: v } });
  const dirty = password !== null || JSON.stringify(draft) !== JSON.stringify(q.data.settings);

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      const { password_set: _ps, ...body } = draft;
      void _ps;
      const saved = await put<EmailSettings>("/settings/email", password === null ? body : { ...body, password });
      setDraft(structuredClone(saved));
      setPassword(null);
      toast.success("Email settings saved");
      qc.invalidateQueries({ queryKey: ["email"] });
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const test = async () => {
    setTesting(true);
    try {
      const r = await post<{ sent_to: string }>("/settings/email/test", { to: testTo });
      toast.success(`Test message sent to ${r.sent_to}`);
    } catch (e) {
      toast.error(errMessage(e), { duration: 10_000 });
    } finally {
      setTesting(false);
      qc.invalidateQueries({ queryKey: ["email"] });
    }
  };

  return (
    <div className="space-y-6">
      <Panel>
        <PanelHeader
          title={
            <span className="flex items-center gap-2">
              <Mail className="size-4 text-ink-3" /> Email (SMTP)
              {draft.enabled && draft.host ? <Badge tone="ok">On</Badge> : <Badge>Off</Badge>}
            </span>
          }
          description="Gorget sends notifications, invitations and password-reset links through your mail provider. Nothing is sent until you turn email on."
        />
        <fieldset disabled={!canEdit} className="space-y-5 px-5 py-4">
          <ToggleRow title="Send email" description="Turn on after filling in the server details and sending a test message." checked={draft.enabled} onChange={(v) => set({ enabled: v })} />
          <Field label="Provider" hint={presets.find((p) => p.host === draft.host)?.hint || "Pick one to fill in the server details, or enter them yourself."}>
            <Select
              value={presets.find((p) => p.host === draft.host)?.name ?? ""}
              onChange={(e) => {
                const p = presets.find((x) => x.name === e.target.value);
                if (p) set({ host: p.host, port: p.port, security: p.security });
              }}
            >
              <option value="">Other / custom</option>
              {presets.map((p) => (
                <option key={p.name}>{p.name}</option>
              ))}
            </Select>
          </Field>
          <div className="grid gap-4 md:grid-cols-[1fr_120px_200px]">
            <Field label="SMTP server">
              <Input value={draft.host} placeholder="smtp.example.com" className="font-mono" spellCheck={false} onChange={(e) => set({ host: e.target.value.trim() })} />
            </Field>
            <Field label="Port">
              <Input type="number" min={1} max={65535} value={draft.port || ""} onChange={(e) => set({ port: Number(e.target.value) })} />
            </Field>
            <Field label="Encryption">
              <Select value={draft.security} onChange={(e) => set({ security: e.target.value as EmailSettings["security"] })}>
                <option value="starttls">STARTTLS (port 587)</option>
                <option value="tls">TLS (port 465)</option>
                <option value="none">None (local relay only)</option>
              </Select>
            </Field>
          </div>
          {draft.security === "none" && <Note tone="warn">Without encryption the password can't be sent. Use this only for a mail relay on this server or a trusted private network.</Note>}
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Username" hint="Usually your full email address.">
              <Input value={draft.username} autoComplete="off" spellCheck={false} onChange={(e) => set({ username: e.target.value })} />
            </Field>
            <Field label="Password" hint={draft.password_set && password === null ? "Saved. Type to replace it." : "Stored encrypted with the server's master key; never shown again."}>
              <Input
                type="password"
                autoComplete="new-password"
                value={password ?? ""}
                placeholder={draft.password_set ? "••••••••" : ""}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="From address" hint="Must be an address your provider lets you send from.">
              <Input value={draft.from} placeholder="vpn@example.com" spellCheck={false} onChange={(e) => set({ from: e.target.value.trim() })} />
            </Field>
            <Field label="From name">
              <Input value={draft.from_name} placeholder="Gorget" onChange={(e) => set({ from_name: e.target.value })} />
            </Field>
          </div>
          <Checkbox checked={draft.skip_verify} onChange={(v) => set({ skip_verify: v })} label="Accept a self-signed certificate (only for your own mail relay)" />
        </fieldset>
        {canEdit && (
          <div className="space-y-3 border-t border-line px-5 py-3">
            {err && <ErrorNote>{err}</ErrorNote>}
            <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
              Save changes
            </Button>
          </div>
        )}
      </Panel>

      <Panel>
        <PanelHeader title="Send a test message" description="Check the settings before relying on them. Save first: the test uses the saved settings." />
        <div className="flex flex-wrap items-end gap-3 px-5 py-4">
          <Field label="Send to" className="min-w-64 flex-1">
            <Input value={testTo} placeholder={me.user.email} onChange={(e) => setTestTo(e.target.value)} />
          </Field>
          <Button onClick={test} loading={testing} disabled={!canEdit || !q.data.settings.host || dirty}>
            <Send /> Send test
          </Button>
        </div>
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

      <Panel>
        <PanelHeader title="Notifications" description="Who gets email, and about what." />
        <fieldset disabled={!canEdit} className="space-y-5 px-5 py-4">
          <Field label="Send administrator notifications to" hint="Leave empty to notify every owner and admin.">
            <ListEditor values={draft.recipients} onChange={(v) => set({ recipients: v })} placeholder="it@example.com" />
          </Field>
          <div className="divide-y divide-line rounded-lg border border-line px-4">
            {notifyRows.map(([k, title, hint]) => (
              <ToggleRow key={k} title={title} description={hint || undefined} checked={draft.notify[k]} onChange={(v) => setNotify(k, v)} />
            ))}
          </div>
          <div className="divide-y divide-line rounded-lg border border-line px-4">
            <ToggleRow title="Also tell people about their own devices" description="Owners hear about expiring sign-ins, blocks and new-country connections of their devices." checked={draft.notify.owners_too} onChange={(v) => setNotify("owners_too", v)} />
            <ToggleRow title="Email invitations to new people" description="New people get a link to choose their own password instead of a temporary one you pass on." checked={draft.notify.invitations} onChange={(v) => setNotify("invitations", v)} />
          </div>
        </fieldset>
        {canEdit && (
          <div className="border-t border-line px-5 py-3">
            <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
              Save changes
            </Button>
          </div>
        )}
      </Panel>
    </div>
  );
}
