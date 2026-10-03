import { useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { errMessage, get, post } from "@/lib/api";
import { Button, ErrorNote, Field, Input, Note } from "@/components/ui";
import { LamellarPanel } from "./Login";
import { cn } from "@/lib/utils";

interface Status {
  completed: boolean;
  defaults: { ipv4: string; ipv6: string; domain: string };
  public_url: string;
  tls_mode: string;
}

/** First-run wizard: protected by the one-time setup token printed in the server log. */
export function Setup() {
  const navigate = useNavigate();
  const { data } = useQuery({ queryKey: ["setup-status"], queryFn: () => get<Status>("/setup/status") });
  const [form, setForm] = useState({
    setup_token: "",
    network_name: "Home",
    owner_name: "",
    owner_email: "",
    password: "",
    confirm: "",
    ipv4: "",
    domain: "",
    policy: "allow-all",
    approval_required: false,
  });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // The server prints a link with the token in the URL fragment (#token=…); fill it in
  // and remove it from the address bar so it doesn't linger in history.
  useEffect(() => {
    const m = /(?:^#|&)token=([^&]+)/.exec(window.location.hash);
    if (m) {
      setForm((f) => ({ ...f, setup_token: decodeURIComponent(m[1]) }));
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
    }
  }, []);

  useEffect(() => {
    if (data?.completed) navigate("/login", { replace: true });
    if (data && !form.ipv4) setForm((f) => ({ ...f, ipv4: data.defaults.ipv4, domain: data.defaults.domain }));
  }, [data, navigate, form.ipv4]);

  const set = (k: keyof typeof form) => (v: string | boolean) => setForm((f) => ({ ...f, [k]: v }));

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    if (form.password !== form.confirm) return setError("The passwords don't match.");
    if (form.password.length < 10) return setError("Use at least 10 characters for the password.");
    setBusy(true);
    try {
      const { confirm: _c, ...body } = form;
      void _c;
      await post("/setup", body);
      navigate("/", { replace: true });
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid min-h-full lg:grid-cols-[1fr_minmax(480px,48%)]">
      <LamellarPanel />
      <div className="flex justify-center overflow-y-auto px-6 py-12">
        <form onSubmit={submit} className="w-full max-w-md space-y-8">
          <div>
            <h1 className="font-display text-[26px] font-semibold">Set up your network</h1>
            <p className="mt-1 text-[13px] text-ink-2">This runs once. You'll be the owner of this Gorget server.</p>
          </div>
          {error && <ErrorNote>{error}</ErrorNote>}

          <section className="space-y-4">
            <h2 className="text-[11px] font-medium uppercase tracking-wider text-ink-3">Server</h2>
            <Field
              label="Setup token"
              hint={
                form.setup_token
                  ? "Filled in from the link the server printed."
                  : "Open the setup link the server printed, or run gorget-server setup-link on the server (Docker: docker compose exec gorget gorget-server setup-link). The token is also saved as setup-token in the data directory."
              }
            >
              <Input required autoComplete="off" spellCheck={false} className="font-mono" value={form.setup_token} onChange={(e) => set("setup_token")(e.target.value.trim())} />
            </Field>
            {data && (
              <p className="text-xs text-ink-3">
                Public address <span className="font-mono text-ink-2">{data.public_url}</span> · TLS <span className="font-mono text-ink-2">{data.tls_mode}</span>
              </p>
            )}
          </section>

          <section className="space-y-4">
            <h2 className="text-[11px] font-medium uppercase tracking-wider text-ink-3">Owner account</h2>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Name">
                <Input value={form.owner_name} onChange={(e) => set("owner_name")(e.target.value)} autoComplete="name" />
              </Field>
              <Field label="Email">
                <Input type="email" required value={form.owner_email} onChange={(e) => set("owner_email")(e.target.value)} autoComplete="email" />
              </Field>
              <Field label="Password" hint="At least 10 characters.">
                <Input type="password" required value={form.password} onChange={(e) => set("password")(e.target.value)} autoComplete="new-password" />
              </Field>
              <Field label="Confirm password">
                <Input type="password" required value={form.confirm} onChange={(e) => set("confirm")(e.target.value)} autoComplete="new-password" />
              </Field>
            </div>
          </section>

          <section className="space-y-4">
            <h2 className="text-[11px] font-medium uppercase tracking-wider text-ink-3">Network</h2>
            <Field label="Network name">
              <Input required value={form.network_name} onChange={(e) => set("network_name")(e.target.value)} />
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Address range" hint="Private or 100.64.0.0/10 range, /8 – /28.">
                <Input required className="font-mono" value={form.ipv4} onChange={(e) => set("ipv4")(e.target.value)} />
              </Field>
              <Field label="Internal domain" hint="Devices get names like laptop.domain">
                <Input required className="font-mono" value={form.domain} onChange={(e) => set("domain")(e.target.value)} />
              </Field>
            </div>
            <Note>Pick a range that doesn't overlap the networks your devices sit on. The default 100.80.0.0/16 rarely clashes with home or office networks.</Note>
          </section>

          <section className="space-y-3">
            <h2 className="text-[11px] font-medium uppercase tracking-wider text-ink-3">Starting access rules</h2>
            {[
              { v: "allow-all", t: "Everything can reach everything", d: "Good for a personal network. Tighten rules later." },
              { v: "deny", t: "People reach only their own devices", d: "Zero-trust start. Add rules for anything shared." },
            ].map((o) => (
              <label key={o.v} className={cn("flex cursor-pointer gap-3 rounded-lg border p-3", form.policy === o.v ? "border-blued bg-blued-soft" : "border-line bg-surface hover:border-line-strong")}>
                <input type="radio" name="policy" className="mt-0.5 accent-[var(--blued)]" checked={form.policy === o.v} onChange={() => set("policy")(o.v)} />
                <span>
                  <span className="block text-[13px] font-medium">{o.t}</span>
                  <span className="block text-xs text-ink-3">{o.d}</span>
                </span>
              </label>
            ))}
            <label className="flex items-center gap-2 pt-1 text-[13px]">
              <input type="checkbox" className="size-4 accent-[var(--blued)]" checked={form.approval_required} onChange={(e) => set("approval_required")(e.target.checked)} />
              New devices need approval from an admin before they can connect
            </label>
          </section>

          <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>
            Create network
          </Button>
        </form>
      </div>
    </div>
  );
}
