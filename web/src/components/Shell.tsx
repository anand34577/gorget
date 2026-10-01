import { Suspense, useEffect, useState, type ReactNode } from "react";
import { NavLink, Outlet, useNavigate } from "react-router";
import { Command } from "cmdk";
import {
  ChartLine,
  Activity,
  Cable,
  KeyRound,
  LayoutGrid,
  LogOut,
  Map,
  Menu as MenuIcon,
  Monitor,
  Moon,
  Network,
  Search,
  Settings,
  ShieldCheck,
  Sun,
  SunMoon,
  UserRound,
  Users,
  Waypoints,
  Globe,
  Clock,
  BookOpen,
} from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useSession, type Perm } from "@/lib/session";
import { useLiveUpdates } from "@/lib/events";
import { useTheme } from "@/lib/theme";
import { post } from "@/lib/api";
import { cn, roleLabel } from "@/lib/utils";
import { Wordmark } from "./Logo";
import { Button, Menu, MenuItem, MenuSeparator, Spinner } from "./ui";

interface NavItem {
  to: string;
  label: string;
  icon: ReactNode;
  perm?: Perm;
}

const nav: { section?: string; items: NavItem[] }[] = [
  {
    items: [
      { to: "/", label: "Overview", icon: <LayoutGrid /> },
      { to: "/insights", label: "Insights", icon: <ChartLine />, perm: "read" },
      { to: "/devices", label: "Devices", icon: <Monitor /> },
      { to: "/wireguard", label: "WireGuard apps", icon: <Cable /> },
      { to: "/map", label: "Network map", icon: <Map />, perm: "read" },
    ],
  },
  {
    section: "Access",
    items: [
      { to: "/access", label: "Access rules", icon: <ShieldCheck />, perm: "read" },
      { to: "/users", label: "People & groups", icon: <Users />, perm: "read" },
      { to: "/routes", label: "Routes & exit nodes", icon: <Waypoints />, perm: "read" },
      { to: "/dns", label: "DNS", icon: <Globe />, perm: "read" },
      { to: "/requests", label: "Temporary access", icon: <Clock /> },
      { to: "/keys", label: "Setup keys", icon: <KeyRound /> },
    ],
  },
  {
    section: "Administration",
    items: [
      { to: "/activity", label: "Activity log", icon: <Activity /> },
      { to: "/settings", label: "Settings", icon: <Settings />, perm: "read" },
      { to: "/api", label: "API reference", icon: <BookOpen />, perm: "read" },
    ],
  },
];

function NavItems({ onNavigate }: { onNavigate?: () => void }) {
  const { can } = useSession();
  return (
    <nav className="flex-1 space-y-5 overflow-y-auto px-3 py-2" aria-label="Main">
      {nav.map((group, i) => {
        const items = group.items.filter((it) => !it.perm || can(it.perm));
        if (!items.length) return null;
        return (
          <div key={i}>
            {group.section && <div className="mb-1 px-2.5 text-[11px] font-medium uppercase tracking-wider text-ink-3">{group.section}</div>}
            <ul className="space-y-0.5">
              {items.map((it) => (
                <li key={it.to}>
                  <NavLink
                    to={it.to}
                    end={it.to === "/"}
                    onClick={onNavigate}
                    className={({ isActive }) =>
                      cn(
                        "group relative flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] font-medium transition-colors [&_svg]:size-4",
                        isActive ? "bg-surface text-ink shadow-sm" : "text-ink-2 hover:bg-sunken hover:text-ink",
                      )
                    }
                  >
                    {({ isActive }) => (
                      <>
                        {isActive && <span className="temper-line absolute inset-y-1.5 left-0 w-[3px] rounded-full" />}
                        <span className={isActive ? "text-blued" : "text-ink-3 group-hover:text-ink-2"}>{it.icon}</span>
                        {it.label}
                      </>
                    )}
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        );
      })}
    </nav>
  );
}

function UserMenu() {
  const { me } = useSession();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [theme, setTheme] = useTheme();
  const initials = (me.user.name || me.user.email).slice(0, 1).toUpperCase();
  return (
    <Menu
      align="start"
      trigger={
        <button className="flex w-full items-center gap-2.5 rounded-md p-2 text-left hover:bg-sunken">
          <span className="grid size-8 place-items-center rounded-full bg-blued-soft font-display text-sm font-semibold text-blued">{initials}</span>
          <span className="min-w-0 flex-1">
            <span className="block truncate text-[13px] font-medium">{me.user.name || me.user.email}</span>
            <span className="block truncate text-[11px] text-ink-3">{roleLabel[me.user.role]}</span>
          </span>
        </button>
      }
    >
      <MenuItem onSelect={() => navigate("/account")}>
        <UserRound /> Account & security
      </MenuItem>
      <MenuSeparator />
      <MenuItem onSelect={() => setTheme("light")}>
        <Sun /> Light {theme === "light" && "✓"}
      </MenuItem>
      <MenuItem onSelect={() => setTheme("dark")}>
        <Moon /> Dark {theme === "dark" && "✓"}
      </MenuItem>
      <MenuItem onSelect={() => setTheme("system")}>
        <SunMoon /> Match system {theme === "system" && "✓"}
      </MenuItem>
      <MenuSeparator />
      <MenuItem
        onSelect={async () => {
          await post("/auth/logout").catch(() => undefined);
          qc.clear();
          navigate("/login");
        }}
      >
        <LogOut /> Sign out
      </MenuItem>
    </Menu>
  );
}

function CommandPalette({ open, setOpen }: { open: boolean; setOpen: (v: boolean) => void }) {
  const navigate = useNavigate();
  const { can } = useSession();
  const go = (to: string) => {
    setOpen(false);
    navigate(to);
  };
  const items = nav.flatMap((g) => g.items).filter((it) => !it.perm || can(it.perm));
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 bg-[#0b0e12]/50 backdrop-blur-[2px]" onClick={() => setOpen(false)}>
      <Command
        label="Command menu"
        className="mx-auto mt-[14vh] w-[calc(100vw-32px)] max-w-xl overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.key === "Escape" && setOpen(false)}
      >
        <div className="flex items-center gap-2 border-b border-line px-4">
          <Search className="size-4 text-ink-3" />
          <Command.Input autoFocus placeholder="Jump to a page or action…" className="h-12 flex-1 bg-transparent text-sm outline-none placeholder:text-ink-3" />
        </div>
        <Command.List className="max-h-80 overflow-y-auto p-2">
          <Command.Empty className="px-3 py-6 text-center text-[13px] text-ink-3">Nothing matches.</Command.Empty>
          <Command.Group heading="Go to" className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-wider [&_[cmdk-group-heading]]:text-ink-3">
            {items.map((it) => (
              <Command.Item key={it.to} onSelect={() => go(it.to)} className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-[13px] data-[selected=true]:bg-sunken [&_svg]:size-4 [&_svg]:text-ink-3">
                {it.icon}
                {it.label}
              </Command.Item>
            ))}
          </Command.Group>
          <Command.Group heading="Actions" className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:tracking-wider [&_[cmdk-group-heading]]:text-ink-3">
            <Command.Item onSelect={() => go("/wireguard?new=1")} className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-[13px] data-[selected=true]:bg-sunken">
              <Cable className="size-4 text-ink-3" /> Add a WireGuard app
            </Command.Item>
            <Command.Item onSelect={() => go("/keys?new=1")} className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-[13px] data-[selected=true]:bg-sunken">
              <KeyRound className="size-4 text-ink-3" /> Create a setup key
            </Command.Item>
            {can("manage_users") && (
              <Command.Item onSelect={() => go("/users?new=1")} className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-[13px] data-[selected=true]:bg-sunken">
                <Users className="size-4 text-ink-3" /> Invite a person
              </Command.Item>
            )}
            <Command.Item onSelect={() => go("/account")} className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-[13px] data-[selected=true]:bg-sunken">
              <UserRound className="size-4 text-ink-3" /> Account & security
            </Command.Item>
          </Command.Group>
        </Command.List>
      </Command>
    </div>
  );
}

export function Shell() {
  useLiveUpdates();
  const { me } = useSession();
  const [cmdOpen, setCmdOpen] = useState(false);
  const [mobileNav, setMobileNav] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setCmdOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const sidebar = (
    <>
      <div className="flex h-14 items-center justify-between px-5">
        <Wordmark />
      </div>
      <div className="px-3 pb-2">
        <button
          onClick={() => setCmdOpen(true)}
          className="flex h-8 w-full items-center gap-2 rounded-md border border-line bg-surface px-2.5 text-[13px] text-ink-3 hover:border-line-strong"
        >
          <Search className="size-3.5" />
          <span className="flex-1 text-left">Search</span>
          <kbd className="rounded border border-line px-1 font-mono text-[10px]">Ctrl K</kbd>
        </button>
      </div>
      <NavItems onNavigate={() => setMobileNav(false)} />
      <div className="border-t border-line p-2">
        <div className="mb-1 flex items-center gap-1.5 px-2 pt-1 text-[11px] text-ink-3">
          <Network className="size-3" />
          <span className="truncate">{me.network.name}</span>
          <span className="ml-auto font-mono">{me.network.domain}</span>
        </div>
        <UserMenu />
      </div>
    </>
  );

  return (
    <div className="flex h-full">
      <aside className="hidden w-60 shrink-0 flex-col border-r border-line bg-bg lg:flex">{sidebar}</aside>
      {mobileNav && (
        <div className="fixed inset-0 z-40 lg:hidden" onClick={() => setMobileNav(false)}>
          <div className="absolute inset-0 bg-[#0b0e12]/50" />
          <aside className="absolute inset-y-0 left-0 flex w-64 flex-col border-r border-line bg-bg" onClick={(e) => e.stopPropagation()}>
            {sidebar}
          </aside>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 items-center gap-2 border-b border-line px-4 lg:hidden">
          <Button variant="ghost" size="icon" aria-label="Open menu" onClick={() => setMobileNav(true)}>
            <MenuIcon />
          </Button>
          <Wordmark />
        </header>
        <main className="flex-1 overflow-y-auto">
          <div className="mx-auto max-w-[1200px] px-4 py-6 sm:px-8 sm:py-8">
            <Suspense
              fallback={
                <div className="grid min-h-60 place-items-center" aria-busy="true">
                  <Spinner className="size-5" />
                </div>
              }
            >
              <Outlet />
            </Suspense>
          </div>
        </main>
      </div>
      <CommandPalette open={cmdOpen} setOpen={setCmdOpen} />
    </div>
  );
}
