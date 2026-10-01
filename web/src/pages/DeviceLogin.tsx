import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, MonitorSmartphone, XCircle } from "lucide-react";
import { errMessage, get, post } from "@/lib/api";
import { useSession } from "@/lib/session";
import { osLabel, relTime } from "@/lib/utils";
import { Button, ErrorNote, Field, Input, Note, Panel } from "@/components/ui";

interface LoginInfo {
  code: string;
  hostname: string;
  os: string;
  created_at: number;
  existing_device: string;
  approval_required: boolean;
}

/** Page opened from a client's "sign in" link to approve that device. */
export function DeviceLogin() {
  const [params, setParams] = useSearchParams();
  const { me } = useSession();
  const code = params.get("code") ?? "";
  const [draft, setDraft] = useState(code);
  const [result, setResult] = useState<"approved" | "denied" | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const info = useQuery({ queryKey: ["device-login", code], queryFn: () => get<LoginInfo>(`/device-logins/${encodeURIComponent(code)}`), enabled: !!code && !result, retry: false });

  const act = async (what: "approve" | "deny") => {
    setBusy(true);
    setErr("");
    try {
      await post(`/device-logins/${encodeURIComponent(code)}/${what}`);
      setResult(what === "approve" ? "approved" : "denied");
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-md pt-6">
      <Panel className="p-6">
        <div className="mb-5 flex items-center gap-3">
          <div className="rounded-lg bg-blued-soft p-2.5 text-blued">
            <MonitorSmartphone className="size-5" />
          </div>
          <div>
            <h1 className="font-display text-xl font-semibold">Sign in a device</h1>
            <p className="text-xs text-ink-3">as {me.user.email}</p>
          </div>
        </div>
        {result === "approved" ? (
          <div className="flex items-start gap-3 text-[13px]">
            <CheckCircle2 className="size-5 shrink-0 text-verdigris" />
            <p>
              Done. <b>{info.data?.hostname || "The device"}</b> is joining your network. You can close this tab.
              {info.data?.approval_required && " An admin still needs to approve it before it connects."}
            </p>
          </div>
        ) : result === "denied" ? (
          <div className="flex items-start gap-3 text-[13px]">
            <XCircle className="size-5 shrink-0 text-oxide" />
            <p>The sign-in was refused. Nothing was added to your network.</p>
          </div>
        ) : !code ? (
          <form
            onSubmit={(e: FormEvent) => {
              e.preventDefault();
              setParams({ code: draft.trim().toUpperCase() });
            }}
            className="space-y-4"
          >
            <Field label="Code shown on your device">
              <Input autoFocus className="h-11 text-center font-mono text-lg tracking-[0.2em]" placeholder="XXXX-XXXX" value={draft} onChange={(e) => setDraft(e.target.value)} />
            </Field>
            <Button type="submit" variant="primary" className="w-full">
              Continue
            </Button>
          </form>
        ) : info.isError ? (
          <ErrorNote>{errMessage(info.error)}</ErrorNote>
        ) : info.data ? (
          <div className="space-y-4">
            <Note>
              Only continue if you started this sign-in yourself. Check that the code matches the one on your device: <b className="font-mono">{info.data.code}</b>
            </Note>
            <dl className="space-y-2 text-[13px]">
              <div className="flex justify-between">
                <dt className="text-ink-3">Device</dt>
                <dd className="font-medium">{info.data.hostname || "Unknown"}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-ink-3">System</dt>
                <dd>{osLabel[info.data.os] ?? info.data.os}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-ink-3">Requested</dt>
                <dd>{relTime(info.data.created_at)}</dd>
              </div>
              {info.data.existing_device && (
                <div className="flex justify-between">
                  <dt className="text-ink-3">Signs back in as</dt>
                  <dd className="font-mono">{info.data.existing_device}</dd>
                </div>
              )}
            </dl>
            {err && <ErrorNote>{err}</ErrorNote>}
            <div className="flex gap-2">
              <Button className="flex-1" onClick={() => act("deny")} disabled={busy}>
                Refuse
              </Button>
              <Button className="flex-1" variant="primary" onClick={() => act("approve")} loading={busy}>
                Sign in device
              </Button>
            </div>
          </div>
        ) : (
          <p className="text-[13px] text-ink-3">Checking the code…</p>
        )}
      </Panel>
    </div>
  );
}
