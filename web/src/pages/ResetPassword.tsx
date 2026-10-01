import { useEffect, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { CheckCircle2, MailCheck } from "lucide-react";
import { errMessage, post } from "@/lib/api";
import { Logo } from "@/components/Logo";
import { Button, ErrorNote, Field, Input } from "@/components/ui";
import { LamellarPanel } from "./Login";

/**
 * ResetPassword serves two cases: without a token it asks for an email address and
 * sends a reset link; with a token (from an invitation or reset email, in the URL
 * fragment so it never reaches server logs) it lets the person choose a password.
 */
export function ResetPassword() {
  const [params] = useSearchParams();
  const [token, setToken] = useState("");

  useEffect(() => {
    const m = /(?:^#|&)token=([^&]+)/.exec(window.location.hash);
    if (m) {
      setToken(decodeURIComponent(m[1]));
      window.history.replaceState(null, "", window.location.pathname);
    }
  }, []);

  return (
    <div className="grid min-h-full lg:grid-cols-[1fr_minmax(420px,42%)]">
      <LamellarPanel />
      <div className="flex items-center justify-center px-6 py-12">
        <div className="w-full max-w-sm">
          <div className="mb-8 flex items-center gap-3 lg:hidden">
            <Logo size={32} />
            <span className="font-display text-2xl font-semibold">Gorget</span>
          </div>
          {token ? <ChoosePassword token={token} /> : <RequestLink initialEmail={params.get("email") ?? ""} />}
        </div>
      </div>
    </div>
  );
}

function RequestLink({ initialEmail }: { initialEmail: string }) {
  const [email, setEmail] = useState(initialEmail);
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState("");
  const [error, setError] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await post<{ message: string }>("/auth/forgot-password", { email: email.trim() });
      setSent(r.message);
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setBusy(false);
    }
  };
  if (sent) {
    return (
      <div className="space-y-4">
        <MailCheck className="size-8 text-verdigris" />
        <h1 className="font-display text-[26px] font-semibold">Check your email</h1>
        <p className="text-[13px] text-ink-2">{sent} The link works once and expires in 30 minutes. Nothing arrived? Look in spam, or ask an administrator to reset your password.</p>
        <Link to="/login" className="inline-block text-[13px] text-blued underline-offset-2 hover:underline">
          Back to sign-in
        </Link>
      </div>
    );
  }
  return (
    <>
      <h1 className="font-display text-[26px] font-semibold">Reset your password</h1>
      <p className="mt-1 text-[13px] text-ink-2">Enter the email you sign in with and we'll send you a link to choose a new password.</p>
      <form onSubmit={submit} className="mt-6 space-y-4">
        {error && <ErrorNote>{error}</ErrorNote>}
        <Field label="Email" htmlFor="email">
          <Input id="email" type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>
          Send reset link
        </Button>
        <p className="text-center text-xs">
          <Link to="/login" className="text-ink-2 underline-offset-2 hover:text-blued hover:underline">
            Back to sign-in
          </Link>
        </p>
      </form>
    </>
  );
}

function ChoosePassword({ token }: { token: string }) {
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [doneFor, setDoneFor] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    if (password !== confirm) return setError("The passwords don't match.");
    if (password.length < 10) return setError("Use at least 10 characters.");
    setBusy(true);
    try {
      const r = await post<{ email: string }>("/auth/reset-password", { token, password });
      setDoneFor(r.email);
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setBusy(false);
    }
  };
  if (doneFor) {
    return (
      <div className="space-y-4">
        <CheckCircle2 className="size-8 text-verdigris" />
        <h1 className="font-display text-[26px] font-semibold">Password saved</h1>
        <p className="text-[13px] text-ink-2">You can now sign in as {doneFor}. Any other signed-in sessions were ended.</p>
        <Link to="/login">
          <Button variant="primary" size="lg" className="w-full">
            Sign in
          </Button>
        </Link>
      </div>
    );
  }
  return (
    <>
      <h1 className="font-display text-[26px] font-semibold">Choose a password</h1>
      <p className="mt-1 text-[13px] text-ink-2">At least 10 characters. A few unrelated words make a strong password that's easy to remember.</p>
      <form onSubmit={submit} className="mt-6 space-y-4">
        {error && <ErrorNote>{error}</ErrorNote>}
        <Field label="New password" htmlFor="pw">
          <Input id="pw" type="password" autoComplete="new-password" required minLength={10} autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        <Field label="Repeat it" htmlFor="pw2">
          <Input id="pw2" type="password" autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Field>
        <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>
          Save password
        </Button>
      </form>
    </>
  );
}
