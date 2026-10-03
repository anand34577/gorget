import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { errMessage, get, put } from "@/lib/api";
import type { Device, Settings, SettingsResponse } from "@/lib/types";
import { useUnsavedGuard } from "@/lib/utils";
import { useSession } from "@/lib/session";
import { Button, ErrorNote, Field, Input, ListEditor, Mono, PageHeader, Panel, PanelHeader, Select, Skeleton, ToggleRow } from "@/components/ui";
import { SuggestInput, type Suggestion } from "@/components/suggest";
import { SearchInput, matchesQuery } from "@/components/data";

type DNS = Settings["dns"];

const presets: { label: string; servers: string[] }[] = [
  { label: "Cloudflare (DNS over HTTPS)", servers: ["https://cloudflare-dns.com/dns-query"] },
  { label: "Quad9 (DNS over TLS)", servers: ["tls://dns.quad9.net"] },
  { label: "Mullvad (DNS over HTTPS)", servers: ["https://dns.mullvad.net/dns-query"] },
  { label: "Google (DNS over HTTPS)", servers: ["https://dns.google/dns-query"] },
];

export function DnsPage() {
  const { can } = useSession();
  const qc = useQueryClient();
  const manage = can("manage_net");
  const { data } = useQuery({ queryKey: ["settings"], queryFn: () => get<SettingsResponse>("/settings") });
  const [dns, setDns] = useState<DNS | null>(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [recQ, setRecQ] = useState("");
  const devices = useQuery({ queryKey: ["devices"], queryFn: () => get<Device[]>("/devices") });

  useEffect(() => {
    if (data) setDns(normalize(data.settings.dns));
  }, [data]);

  // Values to suggest while filling in records: device names, their addresses and DNS names.
  const hints = useMemo(() => {
    const ds = (devices.data ?? []).filter((d) => d.kind !== "gateway");
    return {
      names: ds.map<Suggestion>((d) => ({ value: d.name, label: d.name, hint: d.ipv4 })),
      v4: ds.map<Suggestion>((d) => ({ value: d.ipv4, label: d.name, hint: d.ipv4 })),
      v6: ds.filter((d) => d.ipv6).map<Suggestion>((d) => ({ value: d.ipv6, label: d.name, hint: d.ipv6 })),
      fqdn: ds.map<Suggestion>((d) => ({ value: d.fqdn, label: d.name, hint: d.fqdn })),
    };
  }, [devices.data]);

  const dirtyNow = !!data && !!dns && JSON.stringify(dns) !== JSON.stringify(normalize(data.settings.dns));
  useUnsavedGuard(dirtyNow);
  if (!data || !dns) return <Skeleton className="h-96" />;
  const domain = data.settings.network.domain;
  const dirty = dirtyNow;

  const save = async () => {
    setBusy(true);
    setErr("");
    try {
      await put("/settings/dns", dns);
      toast.success("DNS settings saved");
      qc.invalidateQueries({ queryKey: ["settings"] });
    } catch (e) {
      setErr(errMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader
        title="DNS"
        description={
          <>
            How devices resolve names. Every device is reachable as <Mono>name.{domain}</Mono>; everything else goes to the resolvers you choose, encrypted when you use DoH or DoT.
          </>
        }
        actions={
          manage && (
            <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
              Save changes
            </Button>
          )
        }
      />
      {err && <div className="mb-4"><ErrorNote>{err}</ErrorNote></div>}
      <div className="space-y-6 pb-20">
        <Panel>
          <PanelHeader title="Device names" />
          <div className="divide-y divide-line px-5">
            <ToggleRow title={`Resolve device names (*.${domain})`} description="Devices look each other up by name instead of IP address." checked={dns.magic_dns} onChange={(v) => setDns({ ...dns, magic_dns: v })} disabled={!manage} />
            <ToggleRow title="Override each device's DNS" description="Send all lookups through the resolvers below, even when the device's network offers its own. Prevents DNS leaks." checked={dns.override_local} onChange={(v) => setDns({ ...dns, override_local: v })} disabled={!manage} />
            <ToggleRow title="Fall back to the device's DNS if resolvers fail" description="Off means lookups fail instead of leaking to the local network." checked={dns.fail_open} onChange={(v) => setDns({ ...dns, fail_open: v })} disabled={!manage} />
          </div>
        </Panel>

        <Panel>
          <PanelHeader title="Resolvers" description="Tried in order. Accepts an IP, IP:port, https://… (DNS over HTTPS) or tls://… (DNS over TLS)." />
          <div className="space-y-4 px-5 py-4">
            <ListEditor
              values={dns.nameservers ?? []}
              onChange={(v) => setDns({ ...dns, nameservers: v })}
              placeholder="https://dns.example/dns-query"
              suggestions={presets.flatMap((p) => p.servers.map((sv) => ({ value: sv, label: p.label, hint: sv })))}
              disabled={!manage}
            />
            {manage && (
              <div className="flex flex-wrap items-center gap-2 text-xs text-ink-3">
                Presets:
                {presets.map((p) => (
                  <Button key={p.label} size="sm" onClick={() => setDns({ ...dns, nameservers: p.servers })}>
                    {p.label}
                  </Button>
                ))}
              </div>
            )}
            <Field label="Search domains" hint="Short names like 'nas' are tried with these suffixes.">
              <ListEditor values={dns.search_domains ?? []} onChange={(v) => setDns({ ...dns, search_domains: v })} placeholder="corp.example.com" />
            </Field>
          </div>
        </Panel>

        <Panel>
          <PanelHeader
            title="Split DNS"
            description="Send lookups for specific domains to specific resolvers, for example your home or office DNS server reached through a shared network."
            actions={
              manage && (
                <Button size="sm" onClick={() => setDns({ ...dns, split: [...(dns.split ?? []), { domain: "", nameservers: [] }] })}>
                  <Plus /> Add domain
                </Button>
              )
            }
          />
          <div className="divide-y divide-line">
            {!(dns.split ?? []).length && <p className="px-5 py-4 text-[13px] text-ink-3">No split domains.</p>}
            {(dns.split ?? []).map((s, i) => (
              <div key={i} className="grid gap-3 px-5 py-4 md:grid-cols-[220px_1fr_auto]">
                <Input
                  className="font-mono"
                  placeholder="corp.local"
                  value={s.domain}
                  onChange={(e) => setDns({ ...dns, split: dns.split!.map((x, j) => (j === i ? { ...x, domain: e.target.value } : x)) })}
                />
                <ListEditor
                  values={s.nameservers}
                  onChange={(v) => setDns({ ...dns, split: dns.split!.map((x, j) => (j === i ? { ...x, nameservers: v } : x)) })}
                  placeholder="10.0.0.53"
                  suggestions={[...new Set((dns.nameservers ?? []).concat((dns.split ?? []).flatMap((x) => x.nameservers)))]}
                />
                <Button variant="ghost" size="icon" aria-label="Remove split domain" onClick={() => setDns({ ...dns, split: dns.split!.filter((_, j) => j !== i) })}>
                  <Trash2 />
                </Button>
              </div>
            ))}
          </div>
        </Panel>

        <Panel>
          <PanelHeader
            title="Custom records"
            description={`A single word like nas becomes nas.${domain} (*.apps becomes a wildcard under it). Names with a dot, like jellyfin.home.lan or *.apps.home.lan, are used as written and work on every connected device.`}
            actions={
              manage && (
                <Button size="sm" onClick={() => setDns({ ...dns, records: [...(dns.records ?? []), { name: "", type: "A", value: "" }] })}>
                  <Plus /> Add record
                </Button>
              )
            }
          />
          <div className="divide-y divide-line">
            {!(dns.records ?? []).length && <p className="px-5 py-4 text-[13px] text-ink-3">No custom records.</p>}
            {(dns.records ?? []).length > 6 && (
              <div className="px-5 py-3">
                <SearchInput value={recQ} onChange={setRecQ} placeholder="Find a record" className="sm:w-64" />
              </div>
            )}
            {(dns.records ?? []).map((r, i) => {
              if (recQ && !matchesQuery(recQ, r.name, r.value, r.type)) return null;
              const upd = (patch: Partial<typeof r>) => setDns({ ...dns, records: dns.records!.map((x, j) => (j === i ? { ...x, ...patch } : x)) });
              const bad = recordProblem(r);
              return (
                <div key={i} className="grid items-start gap-3 px-5 py-3 md:grid-cols-[1fr_110px_1fr_auto]">
                  <SuggestInput mono placeholder="nas, *.apps or jellyfin.home.lan" value={r.name} suggestions={hints.names} onChange={(v) => upd({ name: v })} disabled={!manage} aria-label="Record name" />
                  <Select value={r.type} onChange={(e) => upd({ type: e.target.value })} disabled={!manage} aria-label="Record type">
                    <option>A</option>
                    <option>AAAA</option>
                    <option>CNAME</option>
                  </Select>
                  <div>
                    <SuggestInput
                      mono
                      placeholder={r.type === "CNAME" ? "nas.example.com" : r.type === "AAAA" ? "fd00::10" : "192.168.1.10"}
                      value={r.value}
                      suggestions={r.type === "CNAME" ? hints.fqdn : r.type === "AAAA" ? hints.v6 : hints.v4}
                      onChange={(v) => upd({ value: v })}
                      disabled={!manage}
                      aria-label="Record value"
                      aria-invalid={!!bad}
                    />
                    {bad && <p className="mt-1 text-xs text-oxide">{bad}</p>}
                  </div>
                  {manage && (
                    <Button variant="ghost" size="icon" aria-label="Remove record" onClick={() => setDns({ ...dns, records: dns.records!.filter((_, j) => j !== i) })}>
                      <Trash2 />
                    </Button>
                  )}
                </div>
              );
            })}
          </div>
        </Panel>
      </div>
      {manage && dirty && (
        <div className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-surface/95 px-4 py-3 shadow-[0_-4px_16px_-8px_rgb(0_0_0/0.25)] backdrop-blur lg:left-60">
          <div className="mx-auto flex max-w-[1200px] items-center justify-end gap-3 sm:px-4">
            <span className="mr-auto text-[13px] text-ink-2">You have unsaved DNS changes.</span>
            <Button onClick={() => setDns(normalize(data.settings.dns))}>Discard</Button>
            <Button variant="primary" loading={busy} onClick={save}>
              Save changes
            </Button>
          </div>
        </div>
      )}
    </>
  );
}

function recordProblem(r: { name: string; type: string; value: string }): string | null {
  if (!r.value) return null;
  if (r.type === "A" && !/^(\d{1,3}\.){3}\d{1,3}$/.test(r.value)) return "An A record needs an IPv4 address like 192.168.1.10";
  if (r.type === "AAAA" && !r.value.includes(":")) return "An AAAA record needs an IPv6 address like fd00::10";
  if (r.type === "CNAME" && !/^[a-z0-9.-]+$/i.test(r.value)) return "A CNAME points at a name like nas.example.com";
  return null;
}

function normalize(d: DNS): DNS {
  return { ...d, nameservers: d.nameservers ?? [], search_domains: d.search_domains ?? [], split: d.split ?? [], records: d.records ?? [] };
}
