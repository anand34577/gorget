import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, LogOut, Mail, MoreHorizontal, Plus, ShieldOff, Trash2, UserCog, UserRound, Users } from "lucide-react";
import { toast } from "sonner";
import { ApiError, del, errMessage, get, patch, post } from "@/lib/api";
import type { Group, Role, UserView } from "@/lib/types";
import { useSession } from "@/lib/session";
import { relTime, roleLabel } from "@/lib/utils";
import {
  Badge,
  Button,
  Checkbox,
  confirmAction,
  Dialog,
  EmptyState,
  ErrorNote,
  Field,
  Input,
  Menu,
  MenuItem,
  MenuSeparator,
  PageHeader,
  Panel,
  PanelHeader,
  SecretBox,
  Select,
  Table,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Td,
  Th,
} from "@/components/ui";
import { useReauth } from "./Account";

const roleHelp: Record<Role, string> = {
  owner: "Full control, including other owners",
  admin: "Manage everything except owners",
  network_admin: "Devices, access rules, routes, DNS and keys",
  auditor: "Read-only view of everything",
  user: "Their own devices only",
};

export function People() {
  const [params] = useSearchParams();
  return (
    <>
      <PageHeader title="People & groups" description="Who can sign in, what they can manage, and the groups you use in access rules." />
      <Tabs defaultValue={params.get("tab") ?? "people"}>
        <TabsList>
          <TabsTrigger value="people">People</TabsTrigger>
          <TabsTrigger value="groups">Groups</TabsTrigger>
        </TabsList>
        <TabsContent value="people">
          <PeopleTab />
        </TabsContent>
        <TabsContent value="groups">
          <GroupsTab />
        </TabsContent>
      </Tabs>
    </>
  );
}

function useEmailOn() {
  const q = useQuery({ queryKey: ["sso"], queryFn: () => get<{ password_reset?: boolean }>("/auth/providers") });
  return !!q.data?.password_reset;
}

function PeopleTab() {
  const { me, can } = useSession();
  const emailOn = useEmailOn();
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const reauth = useReauth();
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<UserView[]>("/users") });
  const [invite, setInvite] = useState(params.get("new") === "1");
  const [editing, setEditing] = useState<UserView | null>(null);
  const [secret, setSecret] = useState<{ email: string; password: string } | null>(null);
  const manage = can("manage_users");
  const refresh = () => qc.invalidateQueries({ queryKey: ["users"] });

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn();
      toast.success(ok);
      refresh();
    } catch (e) {
      if (e instanceof ApiError && e.code === "reauth_required" && (await reauth())) return run(fn, ok);
      toast.error(errMessage(e));
    }
  };

  return (
    <Panel>
      <PanelHeader
        title={`${users.data?.length ?? "…"} people`}
        actions={
          manage && (
            <Button variant="primary" size="sm" onClick={() => setInvite(true)}>
              <Plus /> Add person
            </Button>
          )
        }
      />
      <Table>
        <thead>
          <tr>
            <Th>Person</Th>
            <Th>Role</Th>
            <Th className="hidden md:table-cell">Groups</Th>
            <Th className="hidden md:table-cell">Devices</Th>
            <Th className="hidden lg:table-cell">Last sign-in</Th>
            <Th className="w-10" />
          </tr>
        </thead>
        <tbody>
          {users.data?.map((u) => (
            <tr key={u.id} className="hover:bg-surface-2">
              <Td>
                <div className="flex items-center gap-2.5">
                  <span className="grid size-8 shrink-0 place-items-center rounded-full bg-sunken font-display text-sm font-semibold text-ink-2">{(u.name || u.email)[0].toUpperCase()}</span>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-medium">{u.name || u.email}</span>
                      {u.id === me.user.id && <Badge>You</Badge>}
                      {u.disabled && <Badge tone="danger">Disabled</Badge>}
                      {u.locked_until > Date.now() / 1000 && <Badge tone="warn">Locked</Badge>}
                      {(u.totp_enabled || u.passkeys > 0) && <Badge tone="ok">2FA</Badge>}
                      {u.provider !== "local" && <Badge tone="blued">SSO</Badge>}
                    </div>
                    {u.name && <div className="truncate text-xs text-ink-3">{u.email}</div>}
                  </div>
                </div>
              </Td>
              <Td>{roleLabel[u.role]}</Td>
              <Td className="hidden md:table-cell">
                <div className="flex flex-wrap gap-1">
                  {u.groups.map((g) => (
                    <Badge key={g}>{g}</Badge>
                  ))}
                </div>
              </Td>
              <Td className="hidden md:table-cell">{u.device_count}</Td>
              <Td className="hidden lg:table-cell text-ink-2">{relTime(u.last_login_at)}</Td>
              <Td>
                {manage && u.id !== me.user.id && (u.role !== "owner" || can("owner")) && (
                  <Menu trigger={<Button variant="ghost" size="icon" aria-label={`Actions for ${u.email}`}><MoreHorizontal /></Button>}>
                    <MenuItem onSelect={() => setEditing(u)}>
                      <UserCog /> Change role
                    </MenuItem>
                    <MenuItem onSelect={() => run(() => post(`/users/${u.id}/revoke-sessions`), "Signed out everywhere")}>
                      <LogOut /> Sign out everywhere
                    </MenuItem>
                    {u.provider === "local" && emailOn && (
                      <MenuItem onSelect={() => run(() => post(`/users/${u.id}/invite`), `Password link emailed to ${u.email}`)}>
                        <Mail /> Email a password link
                      </MenuItem>
                    )}
                    {u.provider === "local" && (
                      <MenuItem
                        onSelect={() =>
                          run(async () => {
                            const r = await post<{ temporary_password: string }>(`/users/${u.id}/reset-password`);
                            setSecret({ email: u.email, password: r.temporary_password });
                          }, "Password reset")
                        }
                      >
                        <KeyRound /> Set a temporary password
                      </MenuItem>
                    )}
                    {(u.totp_enabled || u.passkeys > 0) && (
                      <MenuItem
                        onSelect={async () =>
                          (await confirmAction({ title: `Remove two-factor sign-in for ${u.email}?`, description: "Use this when someone lost their phone or security key. They'll sign in with only a password until they set it up again.", confirm: "Remove 2FA", danger: true })) &&
                          run(() => post(`/users/${u.id}/reset-mfa`), "Two-factor sign-in removed")
                        }
                      >
                        <ShieldOff /> Remove two-factor sign-in
                      </MenuItem>
                    )}
                    <MenuItem onSelect={() => run(() => patch(`/users/${u.id}`, { disabled: !u.disabled }), u.disabled ? "Account enabled" : "Account disabled")}>
                      <UserRound /> {u.disabled ? "Enable account" : "Disable account"}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem
                      danger
                      onSelect={async () =>
                        (await confirmAction({ title: `Remove ${u.email}?`, description: `Their ${u.device_count} device(s) are removed from the network too. This cannot be undone.`, confirm: "Remove person", danger: true })) &&
                        run(() => del(`/users/${u.id}`), "Person removed")
                      }
                    >
                      <Trash2 /> Remove
                    </MenuItem>
                  </Menu>
                )}
              </Td>
            </tr>
          ))}
        </tbody>
      </Table>
      <InviteDialog
        open={invite}
        onOpenChange={(v) => {
          setInvite(v);
          if (!v && params.get("new")) setParams({});
        }}
        onCreated={(email, password, invited) => {
          refresh();
          if (invited) toast.success(`Invitation emailed to ${email}`);
          else if (password) setSecret({ email, password });
        }}
      />
      {editing && (
        <RoleDialog
          user={editing}
          onClose={() => setEditing(null)}
          onSave={(role, name) => run(() => patch(`/users/${editing.id}`, { role, name }), "Person updated").then(() => setEditing(null))}
        />
      )}
      <Dialog open={!!secret} onOpenChange={(o) => !o && setSecret(null)} title="Temporary password" description={`Share this with ${secret?.email} over a secure channel. They must choose a new password when they sign in.`}>
        {secret && <SecretBox value={secret.password} caption="It won't be shown again." />}
      </Dialog>
    </Panel>
  );
}

function InviteDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (v: boolean) => void; onCreated: (email: string, pw: string, invited: boolean) => void }) {
  const { can } = useSession();
  const emailOn = useEmailOn();
  const [sendInvite, setSendInvite] = useState(true);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("user");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setEmail("");
      setName("");
      setRole("user");
      setErr("");
    }
  }, [open]);
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Add a person"
      description={
        emailOn
          ? "They get an email with a link to choose their password, or can use single sign-on if it's set up with the same email."
          : "They get a temporary password to sign in with, or can use single sign-on if it's set up with the same email. Set up email in Settings to send invitations instead."
      }
      footer={
        <>
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!email.includes("@")}
            onClick={async () => {
              setBusy(true);
              setErr("");
              try {
                const r = await post<{ temporary_password: string; invited: boolean }>("/users", { email: email.trim(), name, role, invite: emailOn && sendInvite });
                onOpenChange(false);
                onCreated(email, r.temporary_password, r.invited);
              } catch (e) {
                setErr(errMessage(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            Add person
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {err && <ErrorNote>{err}</ErrorNote>}
        <Field label="Email">
          <Input type="email" autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        {emailOn && <Checkbox checked={sendInvite} onChange={setSendInvite} label="Email them an invitation to choose a password" />}
        <Field label="Role" hint={roleHelp[role]}>
          <Select value={role} onChange={(e) => setRole(e.target.value as Role)}>
            {(["user", "auditor", "network_admin", "admin", ...(can("owner") ? ["owner"] : [])] as Role[]).map((r) => (
              <option key={r} value={r}>
                {roleLabel[r]}
              </option>
            ))}
          </Select>
        </Field>
      </div>
    </Dialog>
  );
}

function RoleDialog({ user, onClose, onSave }: { user: UserView; onClose: () => void; onSave: (role: Role, name: string) => void }) {
  const { can } = useSession();
  const [role, setRole] = useState<Role>(user.role);
  const [name, setName] = useState(user.name);
  return (
    <Dialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={user.email}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" onClick={() => onSave(role, name)}>
            Save changes
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <fieldset className="space-y-2">
          <legend className="mb-1.5 text-[13px] font-medium">Role</legend>
          {(["user", "auditor", "network_admin", "admin", ...(can("owner") ? ["owner"] : [])] as Role[]).map((r) => (
            <label key={r} className="flex cursor-pointer items-start gap-2.5 text-[13px]">
              <input type="radio" className="mt-1 accent-[var(--blued)]" checked={role === r} onChange={() => setRole(r)} />
              <span>
                <span className="font-medium">{roleLabel[r]}</span>
                <span className="block text-xs text-ink-3">{roleHelp[r]}</span>
              </span>
            </label>
          ))}
        </fieldset>
      </div>
    </Dialog>
  );
}

function GroupsTab() {
  const { can } = useSession();
  const qc = useQueryClient();
  const groups = useQuery({ queryKey: ["groups"], queryFn: () => get<Group[]>("/groups") });
  const users = useQuery({ queryKey: ["users"], queryFn: () => get<UserView[]>("/users") });
  const [edit, setEdit] = useState<Partial<Group> | null>(null);
  const manage = can("manage_users") || can("manage_net");
  const emailOf = (id: string) => users.data?.find((u) => u.id === id)?.email ?? id.slice(0, 8);

  const save = async () => {
    if (!edit) return;
    try {
      const body = { name: edit.name, description: edit.description ?? "", members: edit.members ?? [] };
      if (edit.id) await patch(`/groups/${edit.id}`, body);
      else await post("/groups", body);
      toast.success(edit.id ? "Group updated" : "Group created");
      setEdit(null);
      qc.invalidateQueries({ queryKey: ["groups"] });
      qc.invalidateQueries({ queryKey: ["users"] });
    } catch (e) {
      toast.error(errMessage(e));
    }
  };

  return (
    <Panel>
      <PanelHeader
        title="Groups"
        description="Refer to groups in access rules as group:<name>. Groups from single sign-on update automatically."
        actions={
          manage && (
            <Button size="sm" variant="primary" onClick={() => setEdit({ members: [] })}>
              <Plus /> New group
            </Button>
          )
        }
      />
      {!groups.data?.length ? (
        <EmptyState icon={<Users />} title="No groups yet">
          Groups let one rule cover a whole team, like letting group:engineering reach tag:server.
        </EmptyState>
      ) : (
        <ul className="divide-y divide-line">
          {groups.data.map((g) => (
            <li key={g.id} className="flex flex-wrap items-center gap-3 px-5 py-3">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-[13px] font-medium">group:{g.name}</span>
                  {g.source !== "local" && <Badge tone="blued">Synced</Badge>}
                </div>
                {g.description && <div className="text-xs text-ink-3">{g.description}</div>}
                <div className="mt-1 flex flex-wrap gap-1">
                  {g.members.map((m) => (
                    <Badge key={m}>{emailOf(m)}</Badge>
                  ))}
                  {!g.members.length && <span className="text-xs text-ink-3">No members</span>}
                </div>
              </div>
              {manage && (
                <div className="flex gap-1">
                  <Button size="sm" onClick={() => setEdit(g)}>
                    Edit
                  </Button>
                  <Button
                    size="sm"
                    variant="danger-ghost"
                    aria-label={`Delete ${g.name}`}
                    onClick={async () => {
                      if (!(await confirmAction({ title: `Delete group:${g.name}?`, description: "Access rules that mention this group stop matching anyone.", confirm: "Delete group", danger: true }))) return;
                      try {
                        await del(`/groups/${g.id}`);
                        qc.invalidateQueries({ queryKey: ["groups"] });
                        toast.success("Group deleted");
                      } catch (e) {
                        toast.error(errMessage(e));
                      }
                    }}
                  >
                    <Trash2 />
                  </Button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      <Dialog
        open={!!edit}
        onOpenChange={(o) => !o && setEdit(null)}
        title={edit?.id ? `Edit group:${edit.name}` : "New group"}
        footer={
          <>
            <Button onClick={() => setEdit(null)}>Cancel</Button>
            <Button variant="primary" onClick={save} disabled={!edit?.name}>
              {edit?.id ? "Save changes" : "Create group"}
            </Button>
          </>
        }
      >
        {edit && (
          <div className="space-y-4">
            <Field label="Name" hint="Lowercase letters, digits and dashes.">
              <Input className="font-mono" value={edit.name ?? ""} onChange={(e) => setEdit({ ...edit, name: e.target.value.toLowerCase() })} />
            </Field>
            <Field label="Description">
              <Input value={edit.description ?? ""} onChange={(e) => setEdit({ ...edit, description: e.target.value })} />
            </Field>
            <fieldset>
              <legend className="mb-2 text-[13px] font-medium">Members</legend>
              <div className="max-h-60 space-y-1.5 overflow-y-auto rounded-md border border-line p-2">
                {users.data?.map((u) => (
                  <label key={u.id} className="flex cursor-pointer items-center gap-2 rounded px-1 py-0.5 text-[13px] hover:bg-surface-2">
                    <input
                      type="checkbox"
                      className="size-4 accent-[var(--blued)]"
                      checked={edit.members?.includes(u.id) ?? false}
                      onChange={(e) => setEdit({ ...edit, members: e.target.checked ? [...(edit.members ?? []), u.id] : (edit.members ?? []).filter((x) => x !== u.id) })}
                    />
                    {u.name ? `${u.name} · ` : ""}
                    <span className="text-ink-2">{u.email}</span>
                  </label>
                ))}
              </div>
            </fieldset>
          </div>
        )}
      </Dialog>
    </Panel>
  );
}
