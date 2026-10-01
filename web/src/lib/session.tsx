import { createContext, useContext, useEffect, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useLocation } from "react-router";
import { get, onAuthError, setCsrf } from "./api";
import type { Me } from "./types";

export type Perm = "read" | "manage_net" | "manage_users" | "manage_sys" | "owner";

interface SessionCtx {
  me: Me;
  can: (perm: Perm) => boolean;
  isAdmin: boolean;
}

const Ctx = createContext<SessionCtx | null>(null);

export function useSession() {
  const v = useContext(Ctx);
  if (!v) throw new Error("useSession outside provider");
  return v;
}

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const me = await get<Me>("/me");
      setCsrf(me.csrf_token);
      return me;
    },
    retry: false,
    staleTime: 60_000,
  });
}

/** Loads the session and redirects to /login when signed out. */
export function SessionProvider({ children, fallback }: { children: ReactNode; fallback: ReactNode }) {
  const { data, error, isLoading } = useMe();
  const navigate = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();

  useEffect(() => {
    return onAuthError((err) => {
      if (err.status === 401) {
        qc.clear();
        navigate(`/login?return=${encodeURIComponent(location.pathname + location.search)}`, { replace: true });
      } else {
        qc.invalidateQueries({ queryKey: ["me"] });
        navigate("/account", { replace: true });
      }
    });
  }, [navigate, location, qc]);

  useEffect(() => {
    if (error) navigate(`/login?return=${encodeURIComponent(location.pathname + location.search)}`, { replace: true });
  }, [error, navigate, location]);

  if (isLoading || !data) return <>{fallback}</>;
  const perms = new Set(data.permissions);
  const value: SessionCtx = {
    me: data,
    can: (p) => perms.has(p),
    isAdmin: perms.has("read"),
  };
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
