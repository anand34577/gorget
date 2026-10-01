import { StrictMode, Suspense, lazy, type ComponentType } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Toaster } from "sonner";
import "./index.css";
import { initTheme } from "./lib/theme";
import { SessionProvider } from "./lib/session";
import { ApiError } from "./lib/api";
import { Shell } from "./components/Shell";
import { ConfirmHost, Spinner, TipProvider } from "./components/ui";
import { Login } from "./pages/Login";
import { Setup } from "./pages/Setup";
import { ResetPassword } from "./pages/ResetPassword";
import { Overview } from "./pages/Overview";
import { Account, ReauthHost } from "./pages/Account";

// lazyPage loads a page on demand. After a server upgrade an open tab may ask for
// files that no longer exist; reload once to pick up the new version instead of
// showing a broken page.
function lazyPage<T extends ComponentType<object>>(
  load: () => Promise<{ default: T }>,
) {
  return lazy(() =>
    load()
      .then((m) => {
        try {
          sessionStorage.removeItem("gorget.reloadedForUpdate"); // loaded fine: allow a future reload
        } catch {
          /* storage unavailable */
        }
        return m;
      })
      .catch((err) => {
        const key = "gorget.reloadedForUpdate";
        let reloaded = false;
        try {
          reloaded = sessionStorage.getItem(key) === "1";
          sessionStorage.setItem(key, "1");
        } catch {
          /* storage unavailable */
        }
        if (!reloaded) {
          window.location.reload();
          return new Promise<{ default: T }>(() => {});
        }
        throw err;
      }),
  );
}

// Pages load on first visit; the sign-in page and overview stay in the main bundle.
const Devices = lazyPage(() =>
  import("./pages/Devices").then((m) => ({ default: m.Devices })),
);
const WireGuardApps = lazyPage(() =>
  import("./pages/WireGuard").then((m) => ({ default: m.WireGuardApps })),
);
const DeviceLogin = lazyPage(() =>
  import("./pages/DeviceLogin").then((m) => ({ default: m.DeviceLogin })),
);
const People = lazyPage(() =>
  import("./pages/People").then((m) => ({ default: m.People })),
);
const RoutesPage = lazyPage(() =>
  import("./pages/Routes").then((m) => ({ default: m.RoutesPage })),
);
const DnsPage = lazyPage(() =>
  import("./pages/Dns").then((m) => ({ default: m.DnsPage })),
);
const SetupKeys = lazyPage(() =>
  import("./pages/Keys").then((m) => ({ default: m.SetupKeys })),
);
const ActivityLog = lazyPage(() =>
  import("./pages/Activity").then((m) => ({ default: m.ActivityLog })),
);
const SettingsPage = lazyPage(() =>
  import("./pages/Settings").then((m) => ({ default: m.SettingsPage })),
);
const Requests = lazyPage(() =>
  import("./pages/Requests").then((m) => ({ default: m.Requests })),
);
const ApiDocs = lazyPage(() =>
  import("./pages/ApiDocs").then((m) => ({ default: m.ApiDocs })),
);
const Insights = lazyPage(() => import("./pages/Insights").then((m) => ({ default: m.Insights })));
const NotFound = lazyPage(() =>
  import("./pages/NotFound").then((m) => ({ default: m.NotFound })),
);
const AccessRules = lazyPage(() => import("./pages/Access"));
const NetworkMap = lazyPage(() => import("./pages/NetworkMap"));

initTheme();

const qc = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (n, err) =>
        !(err instanceof ApiError && err.status < 500) && n < 2,
      refetchOnWindowFocus: true,
      staleTime: 10_000,
    },
  },
});

const loading = (
  <div className="grid h-full place-items-center">
    <Spinner className="size-6" />
  </div>
);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <TipProvider>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/setup" element={<Setup />} />
            <Route path="/reset-password" element={<ResetPassword />} />
            <Route
              element={
                <SessionProvider fallback={loading}>
                  <Suspense fallback={loading}>
                    <Shell />
                  </Suspense>
                </SessionProvider>
              }
            >
              <Route index element={<Overview />} />
              <Route path="insights" element={<Insights />} />
              <Route path="devices" element={<Devices />} />
              <Route path="devices/:id" element={<Devices />} />
              <Route path="wireguard" element={<WireGuardApps />} />
              <Route path="map" element={<NetworkMap />} />
              <Route path="access" element={<AccessRules />} />
              <Route path="users" element={<People />} />
              <Route path="routes" element={<RoutesPage />} />
              <Route path="dns" element={<DnsPage />} />
              <Route path="keys" element={<SetupKeys />} />
              <Route path="requests" element={<Requests />} />
              <Route path="api" element={<ApiDocs />} />
              <Route path="activity" element={<ActivityLog />} />
              <Route path="settings" element={<SettingsPage />} />
              <Route path="account" element={<Account />} />
              <Route path="device" element={<DeviceLogin />} />
              <Route path="*" element={<NotFound />} />
            </Route>
          </Routes>
        </BrowserRouter>
        <ConfirmHost />
        <ReauthHost />
        <Toaster
          position="bottom-right"
          toastOptions={{
            className:
              "!bg-surface !text-ink !border-line !font-sans !text-[13px]",
          }}
        />
      </TipProvider>
    </QueryClientProvider>
  </StrictMode>,
);
