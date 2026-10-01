import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Fingerprint, KeyRound } from "lucide-react";
import { ApiError, errMessage, get, post, postRaw, setCsrf } from "@/lib/api";
import { getAssertion } from "@/lib/webauthn";
import { Logo } from "@/components/Logo";
import { Button, ErrorNote, Field, Input } from "@/components/ui";

type Step = "password" | "mfa";

export function Login() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const returnTo = params.get("return") || "/";
  const [step, setStep] = useState<Step>("password");
  const [methods, setMethods] = useState<string[]>([]);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [error, setError] = useState(params.get("error") ?? "");
  const [busy, setBusy] = useState(false);

  const setup = useQuery({ queryKey: ["setup-status"], queryFn: () => get<{ completed: boolean }>("/setup/status") });
  const providers = useQuery({ queryKey: ["sso"], queryFn: () => get<{ providers: { id: string; name: string }[]; password_login: boolean; password_reset?: boolean }>("/auth/providers") });

  useEffect(() => {
    if (setup.data && !setup.data.completed) navigate("/setup", { replace: true });
  }, [setup.data, navigate]);

  const done = () => {
    qc.clear();
    navigate(returnTo.startsWith("/") && !returnTo.startsWith("//") ? returnTo : "/", { replace: true });
  };

  const submitPassword = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await post<{ mfa_required: boolean; mfa_methods: string[]; csrf_token: string }>("/auth/login", { email, password });
      setCsrf(r.csrf_token);
      if (r.mfa_required) {
        setMethods(r.mfa_methods);
        setStep("mfa");
      } else done();
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const submitCode = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await post("/auth/mfa/totp", { code });
      done();
    } catch (err) {
      setError(err instanceof ApiError && err.code === "bad_code" ? "That code didn't match. Check the time on your phone and try again." : errMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const usePasskey = async () => {
    setBusy(true);
    setError("");
    try {
      const opts = await post<unknown>("/auth/mfa/passkey/begin");
      const cred = await getAssertion(opts);
      await postRaw("/auth/mfa/passkey/finish", cred);
      done();
    } catch (err) {
      setError(errMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const sso = providers.data?.providers ?? [];
  const passwordLogin = providers.data?.password_login ?? true;

  return (
    <div className="grid min-h-full lg:grid-cols-[1fr_minmax(420px,42%)]">
      <LamellarPanel />
      <div className="flex items-center justify-center px-6 py-12">
        <div className="w-full max-w-sm">
          <div className="mb-8 flex items-center gap-3 lg:hidden">
            <Logo size={32} />
            <span className="font-display text-2xl font-semibold">Gorget</span>
          </div>
          {step === "password" ? (
            <>
              <h1 className="font-display text-[26px] font-semibold">Sign in</h1>
              <p className="mt-1 text-[13px] text-ink-2">Manage your private network.</p>
              <div className="mt-6 space-y-4">
                {error && <ErrorNote>{error}</ErrorNote>}
                {sso.map((p) => (
                  <Button key={p.id} className="w-full" size="lg" onClick={() => (window.location.href = `/api/v1/auth/oidc/${p.id}/start?return=${encodeURIComponent(returnTo)}`)}>
                    <KeyRound /> Continue with {p.name}
                  </Button>
                ))}
                {sso.length > 0 && passwordLogin && (
                  <div className="flex items-center gap-3 text-[11px] uppercase tracking-wider text-ink-3">
                    <span className="h-px flex-1 bg-line" /> or <span className="h-px flex-1 bg-line" />
                  </div>
                )}
                {(passwordLogin || sso.length === 0) && (
                  <form onSubmit={submitPassword} className="space-y-4">
                    <Field label="Email" htmlFor="email">
                      <Input id="email" type="email" autoComplete="username" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
                    </Field>
                    <Field label="Password" htmlFor="password">
                      <Input id="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
                    </Field>
                    {providers.data?.password_reset && (
                      <p className="-mt-2 text-right text-xs">
                        <Link to={`/reset-password${email ? `?email=${encodeURIComponent(email)}` : ""}`} className="text-ink-2 underline-offset-2 hover:text-blued hover:underline">
                          Forgot your password?
                        </Link>
                      </p>
                    )}
                    <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>
                      Sign in
                    </Button>
                  </form>
                )}
              </div>
            </>
          ) : (
            <>
              <h1 className="font-display text-[26px] font-semibold">Confirm it's you</h1>
              <p className="mt-1 text-[13px] text-ink-2">Your account is protected with a second factor.</p>
              <div className="mt-6 space-y-4">
                {error && <ErrorNote>{error}</ErrorNote>}
                {methods.includes("passkey") && (
                  <Button className="w-full" size="lg" variant={methods.includes("totp") ? "secondary" : "primary"} onClick={usePasskey} loading={busy}>
                    <Fingerprint /> Use a passkey or security key
                  </Button>
                )}
                {methods.includes("totp") && (
                  <form onSubmit={submitCode} className="space-y-4">
                    <Field label="Authenticator code" hint="Enter the 6-digit code from your app, or a recovery code." htmlFor="code">
                      <Input
                        id="code"
                        inputMode="numeric"
                        autoComplete="one-time-code"
                        autoFocus
                        className="h-11 text-center font-mono text-lg tracking-[0.3em]"
                        value={code}
                        onChange={(e) => setCode(e.target.value)}
                      />
                    </Field>
                    <Button type="submit" variant="primary" size="lg" className="w-full" loading={busy}>
                      Verify
                    </Button>
                  </form>
                )}
                <Button variant="ghost" className="w-full" onClick={() => setStep("password")}>
                  Use a different account
                </Button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

/** Decorative brand panel: articulated lames echoing the gorget's plates. */
export function LamellarPanel() {
  const plates = Array.from({ length: 9 });
  return (
    <div className="relative hidden overflow-hidden bg-[#10151c] lg:sticky lg:top-0 lg:block lg:h-screen">
      <div className="absolute left-12 top-10 flex items-center gap-3">
        <Logo size={32} />
        <span className="font-display text-2xl font-semibold text-[#e6ebf0]">Gorget</span>
      </div>
      <svg className="absolute inset-0 h-full w-full" viewBox="0 0 600 800" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
        <defs>
          <linearGradient id="lame" x1="0" x2="1">
            <stop offset="0" stopColor="#3A55B4" />
            <stop offset=".55" stopColor="#7B4FA8" />
            <stop offset="1" stopColor="#C49A3A" />
          </linearGradient>
          <linearGradient id="steel" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="#2a3340" />
            <stop offset="1" stopColor="#161c24" />
          </linearGradient>
        </defs>
        {plates.map((_, i) => {
          const y = 230 + i * 46;
          const r = 380 - i * 22;
          return (
            <g key={i} opacity={1 - i * 0.07}>
              <path d={`M ${300 - r} ${y} A ${r} ${r * 0.55} 0 0 0 ${300 + r} ${y} L ${300 + r - 14} ${y + 30} A ${r - 14} ${(r - 14) * 0.55} 0 0 1 ${300 - r + 14} ${y + 30} Z`} fill="url(#steel)" stroke="#3a4555" strokeWidth="1" />
              <path d={`M ${300 - r} ${y} A ${r} ${r * 0.55} 0 0 0 ${300 + r} ${y}`} fill="none" stroke="url(#lame)" strokeWidth="2" opacity={0.85 - i * 0.08} />
              <circle cx={300 - r + 22} cy={y + 12} r="3" fill="#56637a" />
              <circle cx={300 + r - 22} cy={y + 12} r="3" fill="#56637a" />
            </g>
          );
        })}
      </svg>
      <div className="absolute bottom-12 left-12 right-12 text-[#a3aeba]">
        <p className="font-display text-[32px] font-semibold leading-tight text-[#e6ebf0]">Your devices, joined plate by plate.</p>
        <p className="mt-3 max-w-md text-[13px]">A private WireGuard network you run yourself. Nothing leaves your server.</p>
      </div>
    </div>
  );
}
