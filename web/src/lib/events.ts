import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

const keyMap: Record<string, string[][]> = {
  device: [["devices"], ["overview"], ["network-map"], ["routes"]],
  route: [["routes"], ["overview"], ["network-map"]],
  policy: [["policy"], ["overview"], ["network-map"], ["device-access"]],
  user: [["users"], ["overview"]],
  settings: [["settings"], ["overview"]],
  auth: [["audit"]],
};

/** Subscribes to server-sent change events and refreshes affected queries. */
export function useLiveUpdates() {
  const qc = useQueryClient();
  useEffect(() => {
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout>;
    let pending = new Set<string>();
    let flush: ReturnType<typeof setTimeout> | undefined;
    const connect = () => {
      es = new EventSource("/api/v1/events");
      es.onmessage = (m) => {
        try {
          const ev = JSON.parse(m.data) as { type: string };
          const prefix = ev.type.split(".")[0];
          for (const k of keyMap[prefix] ?? []) pending.add(JSON.stringify(k));
          pending.add(JSON.stringify(["audit"]));
          clearTimeout(flush);
          flush = setTimeout(() => {
            for (const k of pending) qc.invalidateQueries({ queryKey: JSON.parse(k) });
            pending = new Set();
          }, 250);
        } catch {
          /* ignore malformed */
        }
      };
      es.onerror = () => {
        es?.close();
        retry = setTimeout(connect, 5000);
      };
    };
    connect();
    return () => {
      es?.close();
      clearTimeout(retry);
      clearTimeout(flush);
    };
  }, [qc]);
}
