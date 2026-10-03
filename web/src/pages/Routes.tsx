import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Globe, Plus, Router, Trash2, Waypoints } from "lucide-react";
import { toast } from "sonner";
import { del, errMessage, get, patch, post } from "@/lib/api";
import type { Device, Route } from "@/lib/types";
import { useSession } from "@/lib/session";
import { Badge, Button, confirmAction, Dialog, EmptyState, ErrorNote, Field, Input, ListEditor, Mono, Note, PageHeader, Panel, PanelHeader, Select, StatusDot, Switch, Table, Td, Th, Tip } from "@/components/ui";
import { useDeviceActions } from "./Devices";

interface RouteRow extends Route {
  device_name: string;
  primary: boolean;
  online: boolean;
}

interface ExitRow {
  device_id: string;
  device_name: string;
  approved: boolean;
  online: boolean;
  kind: string;
}

export function RoutesPage() {
  const { can } = useSession();
  const qc = useQueryClient();
  const manage = can("manage_net");
  const actions = useDeviceActions();
  const { data } = useQuery({ queryKey: ["routes"], queryFn: () => get<{ routes: RouteRow[]; exit_nodes: ExitRow[] }>("/routes") });
  const [adding, setAdding] = useState(false);

  const update = async (r: RouteRow, body: Partial<Route>) => {
    try {
      await patch(`/routes/${r.id}`, body);
      qc.invalidateQueries({ queryKey: ["routes"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  return (
    <>
      <PageHeader
        title="Routes & exit nodes"
        description="Devices can share their local network (a subnet route) or offer themselves as an exit node for internet traffic. Both need approval unless an auto-approver in the access rules covers them."
        actions={
          manage && (
            <Button onClick={() => setAdding(true)}>
              <Router /> Add networks behind a router
            </Button>
          )
        }
      />
      <SiteRouteDialog open={adding} onOpenChange={setAdding} />
      <div className="space-y-6">
        <Panel>
          <PanelHeader
            title="Shared networks"
            description="When two devices share the same network, the one with the lowest priority number carries traffic, and the next takes over if it goes offline."
          />
          {!data?.routes.length ? (
            <EmptyState icon={<Waypoints />} title="No shared networks">
              To reach your home or office network from anywhere, run <span className="font-mono">gorget set -advertise-routes 192.168.1.0/24</span> on one always-on Linux machine there (list VLANs too, separated by commas), then approve it here.
              Using a router with WireGuard (such as OpenWrt) instead? Choose <b>Add networks behind a router</b>.
            </EmptyState>
          ) : (
            <Table>
              <thead>
                <tr>
                  <Th>Network</Th>
                  <Th>Shared by</Th>
                  <Th>Status</Th>
                  <Th className="w-28">Priority</Th>
                  <Th className="w-20">Approved</Th>
                  <Th className="w-20">Enabled</Th>
                  <Th className="w-10" />
                </tr>
              </thead>
              <tbody>
                {data.routes.map((r) => (
                  <tr key={r.id} className="hover:bg-surface-2">
                    <Td>
                      <Mono>{r.cidr}</Mono>
                    </Td>
                    <Td>
                      <span className="inline-flex items-center gap-2">
                        <StatusDot online={r.online} />
                        {r.device_name}
                      </span>
                    </Td>
                    <Td>
                      {!r.advertised ? (
                        <Badge>No longer offered</Badge>
                      ) : !r.approved ? (
                        <Badge tone="warn">Needs approval</Badge>
                      ) : r.primary ? (
                        <Tip content="This device currently carries traffic for the network.">
                          <span>
                            <Badge tone="ok">Active</Badge>
                          </span>
                        </Tip>
                      ) : (
                        <Badge>Standby</Badge>
                      )}
                    </Td>
                    <Td>
                      <Input
                        type="number"
                        min={0}
                        max={10000}
                        disabled={!manage}
                        defaultValue={r.priority}
                        className="h-7 w-20 font-mono"
                        onBlur={(e) => Number(e.target.value) !== r.priority && update(r, { priority: Number(e.target.value) })}
                      />
                    </Td>
                    <Td>
                      <Switch label="Approved" checked={r.approved} disabled={!manage} onCheckedChange={(v) => update(r, { approved: v })} />
                    </Td>
                    <Td>
                      <Switch label="Enabled" checked={r.enabled} disabled={!manage} onCheckedChange={(v) => update(r, { enabled: v })} />
                    </Td>
                    <Td>
                      {manage && (
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Remove ${r.cidr}`}
                          onClick={async () => {
                            if (!(await confirmAction({ title: `Remove ${r.cidr} from ${r.device_name}?`, description: "If the device still offers it, it reappears and needs approval again.", confirm: "Remove route" }))) return;
                            try {
                              await del(`/routes/${r.id}`);
                              qc.invalidateQueries({ queryKey: ["routes"] });
                            } catch (e) {
                              toast.error(errMessage(e));
                            }
                          }}
                        >
                          <Trash2 />
                        </Button>
                      )}
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </Panel>

        <Panel>
          <PanelHeader title="Exit nodes" description="People choose an exit node in their Gorget app to send all internet traffic through it. Access rules decide who may use them (autogroup:internet)." />
          {!data?.exit_nodes.length ? (
            <EmptyState icon={<Globe />} title="No exit nodes">
              Run <span className="font-mono">gorget up --advertise-exit-node</span> on a server, or turn on the gateway exit node in Settings.
            </EmptyState>
          ) : (
            <ul className="divide-y divide-line">
              {data.exit_nodes.map((x) => (
                <li key={x.device_id} className="flex items-center gap-3 px-5 py-3">
                  <StatusDot online={x.online} />
                  <span className="flex-1 font-medium">
                    {x.device_name} {x.kind === "gateway" && <Badge tone="blued">Gateway</Badge>}
                  </span>
                  {x.kind === "gateway" ? (
                    <span className="text-xs text-ink-3">Managed in Settings → Gateway</span>
                  ) : (
                    <label className="flex items-center gap-2 text-[13px]">
                      Approved
                      <Switch label="Approved" checked={x.approved} disabled={!manage} onCheckedChange={(v) => actions.update(x.device_id, { exit_approved: v }, v ? "Exit node approved" : "Exit node approval removed").then(() => qc.invalidateQueries({ queryKey: ["routes"] }))} />
                    </label>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </div>
    </>
  );
}

/**
 * SiteRouteDialog attaches local networks to a standard WireGuard device, typically a
 * home router (OpenWrt, MikroTik, pfSense). Gorget apps share networks themselves.
 */
function SiteRouteDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const qc = useQueryClient();
  const devs = useQuery({ queryKey: ["devices", "wireguard"], queryFn: () => get<Device[]>("/devices?kind=wireguard"), enabled: open });
  const [deviceId, setDeviceId] = useState("");
  const [cidrs, setCidrs] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => {
    if (open) {
      setCidrs([]);
      setErr("");
    }
  }, [open]);
  useEffect(() => {
    if (!deviceId && devs.data?.length) setDeviceId(devs.data[0].id);
  }, [devs.data, deviceId]);
  const list = devs.data ?? [];
  const save = async () => {
    setBusy(true);
    setErr("");
    const done: string[] = [];
    try {
      for (const c of cidrs) {
        await post("/routes", { device_id: deviceId, cidr: c });
        done.push(c);
      }
      toast.success(`${done.length} network${done.length === 1 ? "" : "s"} added`);
      onOpenChange(false);
    } catch (e) {
      setErr((done.length ? `Added ${done.join(", ")}. ` : "") + errMessage(e));
    } finally {
      setBusy(false);
      qc.invalidateQueries({ queryKey: ["routes"] });
      qc.invalidateQueries({ queryKey: ["devices"] });
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Networks behind a router"
      description="Let a router that connects with a standard WireGuard configuration carry your home or office networks (LAN and VLANs), so every device on Gorget reaches them by their usual IP addresses."
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={!deviceId || cidrs.length === 0} onClick={save}>
            <Plus /> Add networks
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {err && <ErrorNote>{err}</ErrorNote>}
        {list.length === 0 && !devs.isLoading ? (
          <Note tone="warn">
            First create a WireGuard configuration for the router in <Link to="/wireguard" className="underline">WireGuard apps</Link> (choose the OpenWrt format there), then come back.
          </Note>
        ) : (
          <Field label="Router" hint="The WireGuard configuration your router uses.">
            <Select value={deviceId} onChange={(e) => setDeviceId(e.target.value)}>
              {list.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name} ({d.ipv4})
                </option>
              ))}
            </Select>
          </Field>
        )}
        <Field label="Networks behind it" hint="Press Enter after each one, for example 192.168.1.0/24 for the LAN and 192.168.20.0/24 for a VLAN.">
          <ListEditor
            values={cidrs}
            onChange={setCidrs}
            placeholder="192.168.1.0/24"
            suggestions={[{ value: "192.168.1.0/24", label: "Typical home LAN" }, { value: "192.168.0.0/24", label: "Typical home LAN (alternative)" }, { value: "10.0.0.0/24", label: "Typical office network" }]}
            validate={(v) => (/^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/.test(v) ? null : `${v} isn't a network like 192.168.1.0/24`)}
          />
        </Field>
        <Note>
          Who may use these networks is decided by your <Link to="/access" className="underline">access rules</Link>. On the router, allow forwarding from the WireGuard interface to these networks (the OpenWrt configuration download includes this).
        </Note>
      </div>
    </Dialog>
  );
}
