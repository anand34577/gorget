import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Download, Globe, Laptop, Loader2, Monitor, Network, Router, Server } from "lucide-react";
import { toast } from "sonner";
import { errMessage, get, post } from "@/lib/api";
import type { Device } from "@/lib/types";
import { generateKeyPair, withPrivateKey } from "@/lib/wgkeys";
import { toRouterScript } from "@/lib/routerScript";
import { cn, download } from "@/lib/utils";
import { Button, Checkbox, CopyButton, Dialog, ErrorNote, Field, Input, ListEditor, Mono, Note } from "@/components/ui";
import { Flow, type FlowState } from "@/components/flow";

type Kind = "router" | "linux";

const cidrOk = (v: string) => (/^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/.test(v) ? null : `${v} isn't a network like 192.168.1.0/24`);
const lanSuggestions = [
  { value: "192.168.1.0/24", label: "Typical home network", hint: "192.168.1.0/24" },
  { value: "192.168.0.0/24", label: "Typical home network (alternative)", hint: "192.168.0.0/24" },
  { value: "10.0.0.0/24", label: "Typical office network", hint: "10.0.0.0/24" },
  { value: "192.168.20.0/24", label: "A VLAN, for example", hint: "192.168.20.0/24" },
];

const stepNames = ["What", "Networks", "Connect"];

/**
 * Guided setup for reaching a home or office network with the one Gorget app: a router that only
 * speaks WireGuard (MikroTik, pfSense, GL.iNet and others), or a Linux computer that runs Gorget itself.
 */
export function RouterWizard({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [step, setStep] = useState(0);
  const [kind, setKind] = useState<Kind>("router");
  const [name, setName] = useState("home-router");
  const [nets, setNets] = useState<string[]>([]);
  const [manage, setManage] = useState(true);
  const [masq, setMasq] = useState(false);
  const [how, setHow] = useState<"conf" | "script">("conf");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [created, setCreated] = useState<{ id: string; name: string; config: string } | null>(null);
  const [added, setAdded] = useState<string[]>([]);

  useEffect(() => {
    if (open) {
      setStep(0);
      setKind("router");
      setName("home-router");
      setNets([]);
      setManage(true);
      setMasq(false);
      setHow("conf");
      setErr("");
      setCreated(null);
      setAdded([]);
    }
  }, [open]);

  // Once the configuration exists, watch for the router to connect.
  const live = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices"), refetchInterval: created ? 3000 : false, enabled: open && !!created });
  const device = created ? live.data?.find((d) => d.id === created.id) : undefined;
  const online = !!device?.online;

  const script = useMemo(() => (created ? toRouterScript(created.name, created.config, { manage, masquerade: masq }) : ""), [created, manage, masq]);
  const confFile = created ? `${created.name}.conf` : "";
  const file = created ? `${created.name}-router.sh` : "";

  const addNetworks = async (id: string, todo: string[]) => {
    const done: string[] = [];
    for (const c of todo) {
      await post("/routes", { device_id: id, cidr: c });
      done.push(c);
      setAdded((a) => [...a, c]);
    }
    qc.invalidateQueries({ queryKey: ["routes"] });
    return done;
  };

  const create = async () => {
    setBusy(true);
    setErr("");
    try {
      let c = created;
      if (!c) {
        const keys = generateKeyPair();
        const r = await post<{ device: Device; config: string }>("/wireguard-configs", {
          name: name.trim(),
          public_key: keys.publicKey,
          tunnel_mode: "split",
          preshared_key: true,
          dns: false,
        });
        c = { id: r.device.id, name: r.device.name, config: withPrivateKey(r.config, keys.privateKey) };
        setCreated(c);
        qc.invalidateQueries({ queryKey: ["devices"] });
      }
      await addNetworks(c.id, nets.filter((n) => !added.includes(n)));
      setStep(2);
    } catch (e) {
      setErr(errMessage(e));
      if (created || added.length) setStep(2); // the router exists; show what's left to do
    } finally {
      setBusy(false);
    }
  };

  const remaining = nets.filter((n) => !added.includes(n));
  const states: { router: FlowState; nets: FlowState } = created ? { router: online ? "ok" : "wait", nets: online && added.length ? "ok" : added.length ? "wait" : "off" } : { router: "off", nets: "off" };

  const footer =
    step === 0 ? (
      <>
        <Button onClick={() => onOpenChange(false)}>Cancel</Button>
        <Button variant="primary" disabled={kind === "router" && !/^[a-z0-9][a-z0-9-]*$/.test(name)} onClick={() => setStep(1)}>
          Next
        </Button>
      </>
    ) : step === 1 ? (
      kind === "linux" ? (
        <>
          <Button onClick={() => setStep(0)}>Back</Button>
          <Button variant="primary" onClick={() => setStep(2)}>
            Show the commands
          </Button>
        </>
      ) : (
        <>
          <Button onClick={() => setStep(0)} disabled={busy || !!created}>
            Back
          </Button>
          <Button variant="primary" loading={busy} onClick={create}>
            {created ? "Add the remaining networks" : nets.length ? "Create and continue" : "Continue without a network"}
          </Button>
        </>
      )
    ) : (
      <>
        {remaining.length > 0 && created && (
          <Button loading={busy} onClick={create}>
            Retry adding networks
          </Button>
        )}
        {online && (
          <Button onClick={() => (onOpenChange(false), navigate("/map"))}>
            <Network /> Open the network map
          </Button>
        )}
        <Button variant="primary" onClick={() => onOpenChange(false)}>
          {online || kind === "linux" ? "Done" : "Close and finish later"}
        </Button>
      </>
    );

  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Reach your home network with one app" description="Connect a router or a machine at home, so every Gorget device reaches its devices by their usual addresses." wide footer={footer}>
      <ol className="mb-5 flex items-center gap-2" aria-label="Steps">
        {stepNames.map((s, i) => (
          <li key={s} className="flex flex-1 items-center gap-2" aria-current={i === step ? "step" : undefined}>
            <span className={cn("grid size-6 shrink-0 place-items-center rounded-full border text-[11px] font-semibold", i < step ? "border-verdigris bg-verdigris text-white" : i === step ? "border-blued bg-blued text-blued-ink" : "border-line text-ink-3")}>
              {i < step ? <Check className="size-3.5" /> : i + 1}
            </span>
            <span className={cn("text-[13px]", i === step ? "font-medium" : "text-ink-3")}>{s}</span>
            {i < stepNames.length - 1 && <span className={cn("h-px flex-1", i < step ? "bg-verdigris" : "bg-line")} />}
          </li>
        ))}
      </ol>

      {err && (
        <div className="mb-4">
          <ErrorNote>{err}</ErrorNote>
        </div>
      )}

      {step === 0 && (
        <div className="space-y-5">
          <div className="grid gap-3 sm:grid-cols-2" role="radiogroup" aria-label="What are you connecting?">
            {(
              [
                ["router", <Router key="r" />, "A router with WireGuard", "MikroTik, pfSense, GL.iNet, a NAS… anything that runs standard WireGuard but not the Gorget app. You keep just one VPN."],
                ["linux", <Laptop key="l" />, "A Linux computer at home", "A Raspberry Pi, a server or a Proxmox host that is always on. It runs the Gorget app and shares your network."],
              ] as const
            ).map(([k, icon, title, desc]) => (
              <label key={k} className={cn("flex cursor-pointer gap-3 rounded-xl border p-4 [&_svg]:size-5", kind === k ? "border-blued bg-blued-soft" : "border-line hover:border-line-strong")}>
                <input type="radio" name="kind" className="mt-1 accent-[var(--blued)]" checked={kind === k} onChange={() => setKind(k)} />
                <span className="text-blued">{icon}</span>
                <span>
                  <span className="block text-[13px] font-semibold">{title}</span>
                  <span className="mt-0.5 block text-xs text-ink-3">{desc}</span>
                </span>
              </label>
            ))}
          </div>
          {kind === "router" && (
            <Field label="Name" hint="Lowercase letters, digits and dashes. It becomes the router's name on your network, for example home-router.gorget.internal.">
              <Input value={name} onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-"))} className="font-mono" autoFocus />
            </Field>
          )}
          <Flow
            steps={[
              { icon: <Laptop />, title: "Your laptop and phone", sub: "Gorget app" },
              { icon: <Server />, title: "Gorget server", sub: "meets in the middle" },
              { icon: <Router />, title: kind === "router" ? "Home router" : "Home computer", sub: kind === "router" ? "WireGuard" : "Gorget app", state: "off" },
              { icon: <Monitor />, title: "Everything at home", sub: "by its usual address", state: "off" },
            ]}
          />
        </div>
      )}

      {step === 1 && kind === "router" && (
        <div className="space-y-4">
          <Field label="Which home networks should be reachable?" hint="The LAN, and any VLANs, as address ranges. Check them in the router: the interface list shows each one, for example 192.168.1.1/24 is the network 192.168.1.0/24.">
            <ListEditor values={nets} onChange={setNets} placeholder="192.168.1.0/24" suggestions={lanSuggestions} validate={cidrOk} disabled={!!created} />
          </Field>
          <div className="space-y-2 rounded-lg border border-line bg-surface-2 p-3">
            <div className="text-[13px] font-medium">Options for the router script (only used if you choose the script)</div>
            <Checkbox checked={manage} onChange={setManage} label="Let Gorget devices open the router's SSH and web interface (LuCI)" />
            <Checkbox checked={masq} onChange={setMasq} label="Some home devices don't use this router as their gateway (a VLAN behind another router): translate addresses" />
            <p className="text-xs text-ink-3">The access rules in Gorget still decide who may reach what; you can change both options later.</p>
          </div>
          {nets.length === 0 && <Note>Without a network, Gorget devices can reach the router itself but not the devices behind it. You can add networks later under Routes &amp; exit nodes.</Note>}
          <Note>If you are at home on the same network, your computer keeps using the local connection for these addresses; away from home it goes through the VPN. A network that is also used where you travel (many cafés use 192.168.1.0/24) may be shadowed: a less common range at home avoids that.</Note>
        </div>
      )}

      {step === 1 && kind === "linux" && (
        <div className="space-y-4">
          <Field label="Which home networks will it share?" hint="Ranges as seen from that computer. It needs an address in each.">
            <ListEditor values={nets} onChange={setNets} placeholder="192.168.1.0/24" suggestions={lanSuggestions} validate={cidrOk} />
          </Field>
        </div>
      )}

      {step === 2 && kind === "linux" && (
        <div className="space-y-4">
          <Flow
            steps={[
              { icon: <Laptop />, title: "Your devices" },
              { icon: <Server />, title: "Gorget server" },
              { icon: <Laptop />, title: "Home computer", state: "wait", sub: "run the commands" },
              { icon: <Monitor />, title: nets.join(", ") || "Your networks", state: "off" },
            ]}
          />
          <ol className="space-y-3 text-[13px]">
            <Step n={1} title="Create a setup key" body={<Button size="sm" onClick={() => (onOpenChange(false), navigate("/keys?new=1"))}>Create a setup key</Button>} />
            <Step n={2} title="On the home computer, install and join" body={<Cmd value="curl -fsSL https://YOUR-SERVER/install.sh | GORGET_SETUP_KEY=gsk_… sh" hint="Use your server's address and the key from step 1." />} />
            <Step n={3} title="Share the networks" body={<Cmd value={`gorget set -advertise-routes=${nets.join(",") || "192.168.1.0/24"}`} />} />
            <Step n={4} title="Approve them" body={<span className="text-ink-2">Open <b>Routes &amp; exit nodes</b> and switch on <b>Approved</b>.</span>} />
          </ol>
        </div>
      )}

      {step === 2 && kind === "router" && created && (
        <div className="space-y-5">
          <Flow
            steps={[
              { icon: <Laptop />, title: "Your devices", sub: "Gorget app" },
              { icon: <Server />, title: "Gorget server", sub: "gateway ready" },
              { icon: <Router />, title: created.name, sub: online ? "connected" : "waiting for it…", state: states.router },
              { icon: <Monitor />, title: nets.length ? nets.join(", ") : "No networks yet", sub: online && added.length ? "reachable" : undefined, state: states.nets },
            ]}
          />
          <div className={cn("flex items-center gap-3 rounded-lg border px-4 py-3 text-[13px]", online ? "border-verdigris/40 bg-verdigris-soft" : "border-straw/40 bg-straw-soft")} role="status" aria-live="polite">
            {online ? <Check className="size-5 text-verdigris" /> : <Loader2 className="size-5 animate-spin text-straw" />}
            <div className="flex-1">
              <div className="font-medium">{online ? `${created.name} is connected.` : `Waiting for ${created.name} to connect…`}</div>
              <div className="text-xs text-ink-2">
                {online ? (
                  <>
                    Try it from any device: <Mono>ping {device?.ipv4}</Mono>
                    {nets[0] && (
                      <>
                        {" "}
                        or open an address in <Mono>{nets[0]}</Mono>.
                      </>
                    )}
                  </>
                ) : (
                  "This page updates by itself as soon as the router connects (usually within a few seconds of running the script)."
                )}
              </div>
            </div>
          </div>
          {remaining.length > 0 && <Note tone="warn">These networks weren't added yet: {remaining.join(", ")}. Use “Retry adding networks”.</Note>}

          {!online && (
            <div className="space-y-3 text-[13px]">
              <div className="inline-flex rounded-md border border-line bg-surface-2 p-0.5" role="radiogroup" aria-label="How to configure the router">
                {(
                  [
                    ["conf", "Config file (any router)"],
                    ["script", "Shell script (uci routers)"],
                  ] as const
                ).map(([id, label]) => (
                  <button key={id} type="button" role="radio" aria-checked={how === id} onClick={() => setHow(id)} className={cn("rounded px-2.5 py-1 text-xs font-medium", how === id ? "bg-surface text-ink shadow-sm" : "text-ink-3 hover:text-ink")}>
                    {label}
                  </button>
                ))}
              </div>
              {how === "conf" ? (
                <ol className="space-y-3">
                  <Step
                    n={1}
                    title="Download the WireGuard configuration"
                    body={
                      <Button size="sm" onClick={() => download(confFile, created.config)}>
                        <Download /> Download {confFile}
                      </Button>
                    }
                  />
                  <Step
                    n={2}
                    title="Import it on the router"
                    body={
                      <div className="space-y-1.5 text-ink-2">
                        <p>Every router that supports WireGuard can import a standard configuration file. Look for “WireGuard” in its VPN settings:</p>
                        <ul className="list-disc space-y-1 pl-5 text-xs">
                          <li><b>MikroTik:</b> WinBox &gt; WireGuard &gt; add the interface and peer from the file, then an address and a firewall forward rule.</li>
                          <li><b>pfSense / OPNsense:</b> VPN &gt; WireGuard &gt; add a tunnel and a peer from the file, assign the interface, allow forwarding to your LAN.</li>
                          <li><b>GL.iNet, Asus, Synology, UniFi and similar:</b> VPN client &gt; WireGuard &gt; import the file.</li>
                          <li><b>Any Linux box:</b> <Mono>sudo wg-quick up ./{confFile}</Mono> (and turn on IP forwarding).</li>
                        </ul>
                      </div>
                    }
                  />
                  <Step n={3} title="That's it" body={<span className="text-ink-2">The router connects, and this page turns green. Make sure the router forwards traffic between the tunnel and your home networks.</span>} />
                </ol>
              ) : (
                <ol className="space-y-3">
                  <Step n={1} title="Make sure WireGuard is installed on the router" body={<span className="text-ink-2">Many routers include it. If not, install the <Mono>wireguard-tools</Mono> package with the router's package manager.</span>} />
                  <Step
                    n={2}
                    title="Run the setup script on the router"
                    body={
                      <div className="space-y-2">
                        <Cmd value={`ssh root@192.168.1.1 'sh -s' < ${file}`} hint="Use your router's address. Download the script first:" />
                        <div className="flex flex-wrap gap-2">
                          <Button size="sm" onClick={() => download(file, script)}>
                            <Download /> Download {file}
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => toast.info("Paste it into the router's SSH session, or into its startup scripts page, then run it.")}>
                            Other ways to run it
                          </Button>
                        </div>
                      </div>
                    }
                  />
                  <Step n={3} title="That's it" body={<span className="text-ink-2">The router connects, and this page turns green.</span>} />
                </ol>
              )}
            </div>
          )}
          <details className="rounded-lg border border-line">
            <summary className="cursor-pointer px-3 py-2 text-[13px] font-medium">{how === "conf" ? "Show the configuration" : "Show the script"}</summary>
            <div className="border-t border-line p-3">
              <div className="mb-1 flex justify-end">
                <CopyButton value={how === "conf" ? created.config : script} />
              </div>
              <pre className="max-h-64 overflow-auto rounded bg-sunken p-3 font-mono text-[11.5px] leading-relaxed">{how === "conf" ? created.config : script}</pre>
              <p className="mt-2 text-xs text-ink-3">It contains the router's private key. Don't share it; it is not stored on the server and can't be shown again.</p>
            </div>
          </details>
          {online && (
            <div className="flex flex-wrap items-center gap-2 text-xs text-ink-3">
              <Globe className="size-3.5" /> Everyone allowed by your access rules can now reach the router and these networks. Review who under <b>Access rules</b>.
            </div>
          )}
        </div>
      )}
    </Dialog>
  );
}

function Step({ n, title, body }: { n: number; title: string; body: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="mt-0.5 grid size-5 shrink-0 place-items-center rounded-full bg-sunken text-[11px] font-semibold text-ink-2">{n}</span>
      <div className="min-w-0 flex-1">
        <div className="font-medium">{title}</div>
        <div className="mt-1">{body}</div>
      </div>
    </li>
  );
}

function Cmd({ value, hint }: { value: string; hint?: string }) {
  return (
    <div>
      <div className="flex items-center gap-1 rounded border border-line bg-surface px-2 py-1.5">
        <Mono className="flex-1 overflow-x-auto whitespace-nowrap text-[12px]">{value}</Mono>
        <CopyButton value={value} />
      </div>
      {hint && <p className="mt-1 text-xs text-ink-3">{hint}</p>}
    </div>
  );
}
