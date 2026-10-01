import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import QRCode from "qrcode";
import { Fingerprint, KeyRound, Laptop, Plus, ShieldCheck, Smartphone, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { ApiError, del, errMessage, get, patch, post, postRaw } from "@/lib/api";
import { createCredential } from "@/lib/webauthn";
import type { APIToken } from "@/lib/types";
import { useSession } from "@/lib/session";
import { fmtDate, relTime, roleLabel } from "@/lib/utils";
import { Badge, Button, Checkbox, confirmAction, Dialog, ErrorNote, Field, Input, Note, PageHeader, Panel, PanelHeader, SecretBox, Select } from "@/components/ui";

/** Prompts for the password when the server asks for recent authentication. */
let reauthImpl: (() => Promise<boolean>) | null = null;
export function useReauth() {
  return () => (reauthImpl ? reauthImpl() : Promise.resolve(false));
}

export function ReauthHost() {
  const [state, setState] = useState<{ resolve: (v: boolean) => void } | null>(null);
  const [pw, setPw] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    reauthImpl = () => new Promise((resolve) => setState({ resolve }));
    return () => {
      reauthImpl = null;
    };
  }, []);
  const close = (v: boolean) => {
    state?.resolve(v);
    setState(null);
    setPw("");
    setErr("");
  };
  return (
    <Dialog open={!!state} onOpenChange={(o) => !o && close(false)} title="Confirm your password" description="This action needs a recent sign-in.">
      <form
        className="space-y-4"
        onSubmit={async (e: FormEvent) => {
          e.preventDefault();
          try {
            await post("/me/reauth", { password: pw });
            close(true);
          } catch (e2) {
            setErr(errMessage(e2));
          }
        }}
      >
        {err && <ErrorNote>{err}</ErrorNote>}
        <Input type="password" autoFocus autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} />
        <div className="flex justify-end">
          <Button type="submit" variant="primary">
            Confirm
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export function Account() {
  const { me } = useSession();
  return (
    <>
      <PageHeader title="Account & security" description={`${me.user.email} · ${roleLabel[me.user.role]}${me.user.provider !== "local" ? " · single sign-on" : ""}`} />
      <div className="space-y-6">
        {me.user.must_change_password && <Note tone="warn">Choose a new password to finish setting up your account.</Note>}
        {me.mfa_enrollment_required && <Note tone="warn">Your organisation requires two-factor sign-in. Set up an authenticator app or a passkey to continue.</Note>}
        <Profile />
        {me.user.provider === "local" && <Password />}
        <TwoFactor />
        <Sessions />
        <Tokens />
      </div>
    </>
  );
}

function Profile() {
  const { me } = useSession();
  const qc = useQueryClient();
  const [name, setName] = useState(me.user.name);
  return (
    <Panel>
      <PanelHeader title="Profile" />
      <div className="flex flex-wrap items-end gap-3 px-5 py-4">
        <Field label="Display name" className="w-72">
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Button
          disabled={name === me.user.name}
          onClick={async () => {
            try {
              await patch("/me", { name });
              toast.success("Name saved");
              qc.invalidateQueries({ queryKey: ["me"] });
            } catch (e) {
              toast.error(errMessage(e));
            }
          }}
        >
          Save
        </Button>
      </div>
    </Panel>
  );
}

function Password() {
  const qc = useQueryClient();
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Panel>
      <PanelHeader title="Password" description="Changing it signs you out on every other browser." />
      <form
        className="grid gap-4 px-5 py-4 md:grid-cols-3"
        onSubmit={async (e) => {
          e.preventDefault();
          setErr("");
          if (next !== confirm) return setErr("The new passwords don't match.");
          setBusy(true);
          try {
            await post("/me/password", { current: cur, new: next });
            toast.success("Password changed");
            setCur("");
            setNext("");
            setConfirm("");
            qc.invalidateQueries({ queryKey: ["me"] });
          } catch (e2) {
            setErr(errMessage(e2));
          } finally {
            setBusy(false);
          }
        }}
      >
        {err && <div className="md:col-span-3"><ErrorNote>{err}</ErrorNote></div>}
        <Field label="Current password">
          <Input type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} />
        </Field>
        <Field label="New password" hint="At least 10 characters.">
          <Input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
        <Field label="Confirm new password">
          <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Field>
        <div className="md:col-span-3">
          <Button type="submit" variant="primary" loading={busy} disabled={!cur || !next}>
            Change password
          </Button>
        </div>
      </form>
    </Panel>
  );
}

function TwoFactor() {
  const { me } = useSession();
  const qc = useQueryClient();
  const reauth = useReauth();
  const passkeys = useQuery({ queryKey: ["passkeys"], queryFn: () => get<{ id: string; name: string; created_at: number; last_used_at: number }[]>("/me/passkeys") });
  const [totp, setTotp] = useState<{ secret: string; url: string; qr: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [err, setErr] = useState("");
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["me"] });
    qc.invalidateQueries({ queryKey: ["passkeys"] });
  };
  const withReauth = async (fn: () => Promise<unknown>) => {
    try {
      await fn();
      return true;
    } catch (e) {
      if (e instanceof ApiError && e.code === "reauth_required" && (await reauth())) {
        await fn();
        return true;
      }
      throw e;
    }
  };

  return (
    <Panel>
      <PanelHeader title="Two-factor sign-in" description="A second step after your password, so a stolen password alone isn't enough." />
      <div className="divide-y divide-line">
        <div className="flex flex-wrap items-center gap-4 px-5 py-4">
          <Smartphone className="size-5 text-ink-3" />
          <div className="flex-1">
            <div className="text-[13px] font-medium">Authenticator app</div>
            <div className="text-xs text-ink-3">6-digit codes from an app such as Aegis, 2FAS or any TOTP app.</div>
          </div>
          {me.user.totp_enabled ? (
            <>
              <Badge tone="ok">
                <ShieldCheck className="size-3" /> On
              </Badge>
              <Button
                size="sm"
                variant="danger-ghost"
                onClick={async () => {
                  if (!(await confirmAction({ title: "Turn off the authenticator app?", confirm: "Turn off", danger: true }))) return;
                  try {
                    await withReauth(() => del("/me/totp"));
                    toast.success("Authenticator app turned off");
                    refresh();
                  } catch (e) {
                    toast.error(errMessage(e));
                  }
                }}
              >
                Turn off
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              variant="primary"
              onClick={async () => {
                try {
                  const r = await post<{ secret: string; url: string }>("/me/totp/begin");
                  setTotp({ ...r, qr: await QRCode.toDataURL(r.url, { margin: 1, width: 200 }) });
                  setErr("");
                  setCode("");
                } catch (e) {
                  toast.error(errMessage(e));
                }
              }}
            >
              Set up
            </Button>
          )}
        </div>
        <div className="px-5 py-4">
          <div className="flex flex-wrap items-center gap-4">
            <Fingerprint className="size-5 text-ink-3" />
            <div className="flex-1">
              <div className="text-[13px] font-medium">Passkeys & security keys</div>
              <div className="text-xs text-ink-3">Touch ID, Windows Hello, Android or a hardware key like a YubiKey.</div>
            </div>
            <Button
              size="sm"
              onClick={async () => {
                try {
                  const opts = await post<unknown>("/me/passkeys/begin");
                  const cred = await createCredential(opts);
                  const name = navigator.platform ? `${navigator.platform} passkey` : "Passkey";
                  await postRaw(`/me/passkeys/finish?name=${encodeURIComponent(name)}`, cred);
                  toast.success("Passkey added");
                  refresh();
                } catch (e) {
                  toast.error(errMessage(e));
                }
              }}
            >
              <Plus /> Add passkey
            </Button>
          </div>
          {!!passkeys.data?.length && (
            <ul className="mt-3 space-y-1.5 pl-9">
              {passkeys.data.map((p) => (
                <li key={p.id} className="flex items-center gap-3 text-[13px]">
                  <KeyRound className="size-3.5 text-ink-3" />
                  <span className="flex-1">{p.name}</span>
                  <span className="text-xs text-ink-3">{p.last_used_at ? `used ${relTime(p.last_used_at)}` : `added ${relTime(p.created_at)}`}</span>
                  <Button
                    size="icon"
                    variant="ghost"
                    aria-label={`Remove ${p.name}`}
                    onClick={async () => {
                      try {
                        await withReauth(() => del(`/me/passkeys/${p.id}`));
                        refresh();
                      } catch (e) {
                        toast.error(errMessage(e));
                      }
                    }}
                  >
                    <Trash2 />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
      <Dialog open={!!totp} onOpenChange={(o) => !o && setTotp(null)} title="Set up an authenticator app" description="Scan the code, then enter the 6 digits it shows.">
        {totp && (
          <form
            className="space-y-4"
            onSubmit={async (e) => {
              e.preventDefault();
              try {
                const r = await post<{ recovery_codes: string[] }>("/me/totp/confirm", { code });
                setTotp(null);
                setCodes(r.recovery_codes);
                refresh();
              } catch (e2) {
                setErr(errMessage(e2));
              }
            }}
          >
            <div className="flex justify-center">
              <img src={totp.qr} alt="Authenticator QR code" className="rounded-lg border border-line bg-white p-2" width={200} height={200} />
            </div>
            <SecretBox value={totp.secret} caption="Can't scan? Enter this key manually." />
            {err && <ErrorNote>{err}</ErrorNote>}
            <Field label="Code from the app">
              <Input inputMode="numeric" autoComplete="one-time-code" className="text-center font-mono text-lg tracking-[0.3em]" value={code} onChange={(e) => setCode(e.target.value)} />
            </Field>
            <Button type="submit" variant="primary" className="w-full" disabled={code.length < 6}>
              Turn on
            </Button>
          </form>
        )}
      </Dialog>
      <Dialog open={!!codes} onOpenChange={(o) => !o && setCodes(null)} title="Save your recovery codes" description="Each code works once if you lose your phone. They won't be shown again.">
        {codes && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-2 rounded-lg bg-sunken p-4 font-mono text-[13px]">
              {codes.map((c) => (
                <span key={c}>{c}</span>
              ))}
            </div>
            <SecretBox value={codes.join("\n")} caption="Copy all codes" />
          </div>
        )}
      </Dialog>
    </Panel>
  );
}

function Sessions() {
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["sessions"], queryFn: () => get<{ id: string; created_at: number; last_seen_at: number; ip: string; user_agent: string; current: boolean }[]>("/me/sessions") });
  return (
    <Panel>
      <PanelHeader title="Where you're signed in" />
      <ul className="divide-y divide-line">
        {data?.map((s) => (
          <li key={s.id} className="flex items-center gap-3 px-5 py-3 text-[13px]">
            <Laptop className="size-4 text-ink-3" />
            <div className="min-w-0 flex-1">
              <div className="truncate">{browserName(s.user_agent)}</div>
              <div className="text-xs text-ink-3">
                {s.ip} · signed in {relTime(s.created_at)} · active {relTime(s.last_seen_at)}
              </div>
            </div>
            {s.current ? (
              <Badge tone="ok">This browser</Badge>
            ) : (
              <Button
                size="sm"
                onClick={async () => {
                  await del(`/me/sessions/${s.id}`);
                  qc.invalidateQueries({ queryKey: ["sessions"] });
                  toast.success("Signed out");
                }}
              >
                Sign out
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Panel>
  );
}

function browserName(ua: string) {
  const b = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /Windows/.test(ua) ? "Windows" : /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Mac OS/.test(ua) ? "macOS" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${b} on ${os}` : b;
}

function Tokens() {
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["tokens"], queryFn: () => get<APIToken[]>("/me/tokens") });
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [write, setWrite] = useState(false);
  const [days, setDays] = useState("90");
  const [created, setCreated] = useState("");
  return (
    <Panel>
      <PanelHeader
        title="API tokens"
        description="For scripts and automation. A token acts as you, limited to read-only unless you allow changes."
        actions={
          <Button size="sm" onClick={() => setOpen(true)}>
            <Plus /> New token
          </Button>
        }
      />
      {!data?.length ? (
        <p className="px-5 py-4 text-[13px] text-ink-3">No tokens.</p>
      ) : (
        <ul className="divide-y divide-line">
          {data.map((t) => (
            <li key={t.id} className="flex items-center gap-3 px-5 py-3 text-[13px]">
              <div className="min-w-0 flex-1">
                <div className="font-medium">{t.name}</div>
                <div className="text-xs text-ink-3">
                  <span className="font-mono">{t.token_prefix}…</span> · {t.scopes.includes("write") ? "read & write" : "read-only"} · {t.expires_at ? `expires ${fmtDate(t.expires_at)}` : "no expiry"} ·{" "}
                  {t.last_used_at ? `used ${relTime(t.last_used_at)}` : "never used"}
                </div>
              </div>
              <Button
                size="sm"
                variant="danger-ghost"
                onClick={async () => {
                  if (!(await confirmAction({ title: `Delete ${t.name}?`, description: "Scripts using it stop working immediately.", confirm: "Delete token", danger: true }))) return;
                  await del(`/me/tokens/${t.id}`);
                  qc.invalidateQueries({ queryKey: ["tokens"] });
                }}
              >
                Delete
              </Button>
            </li>
          ))}
        </ul>
      )}
      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (!o) setCreated("");
        }}
        title={created ? "Your API token" : "New API token"}
        description={created ? "Copy it now; it won't be shown again." : undefined}
        footer={
          !created && (
            <Button
              variant="primary"
              disabled={!name}
              onClick={async () => {
                try {
                  const r = await post<{ token: string }>("/me/tokens", { name, scopes: write ? ["read", "write"] : ["read"], expires_in_days: Number(days) });
                  setCreated(r.token);
                  setName("");
                  qc.invalidateQueries({ queryKey: ["tokens"] });
                } catch (e) {
                  toast.error(errMessage(e));
                }
              }}
            >
              Create token
            </Button>
          )
        }
      >
        {created ? (
          <div className="space-y-3">
            <SecretBox value={created} />
            <pre className="overflow-x-auto rounded-md bg-sunken p-3 font-mono text-[12px]">curl -H "Authorization: Bearer {created.slice(0, 12)}…" {location.origin}/api/v1/devices</pre>
          </div>
        ) : (
          <div className="space-y-4">
            <Field label="Name">
              <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="backup script" />
            </Field>
            <Field label="Expires after">
              <Select value={days} onChange={(e) => setDays(e.target.value)}>
                <option value="30">30 days</option>
                <option value="90">90 days</option>
                <option value="365">1 year</option>
                <option value="0">Never</option>
              </Select>
            </Field>
            <Checkbox checked={write} onChange={setWrite} label="Allow changes (not just reading)" />
          </div>
        )}
      </Dialog>
    </Panel>
  );
}
