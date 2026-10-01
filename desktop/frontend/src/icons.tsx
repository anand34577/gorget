// A few outline icons (Lucide paths, ISC license) inlined so the app has no icon dependency.
const paths: Record<string, string> = {
  lock: "M7 11V7a5 5 0 0 1 10 0v4M5 11h14v10H5z",
  plug: "M12 22v-5M9 8V2M15 8V2M18 8v5a6 6 0 0 1-12 0V8z",
  shield: "M20 13c0 5-3.5 7.5-8 9-4.5-1.5-8-4-8-9V5l8-3 8 3z",
  x: "M18 6 6 18M6 6l12 12",
  check: "M20 6 9 17l-5-5",
  dots: "M5 12h.01M12 12h.01M19 12h.01",
  power: "M12 2v10M18.4 6.6a9 9 0 1 1-12.8 0",
  globe: "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM2 12h20M12 2a15 15 0 0 1 0 20 15 15 0 0 1 0-20",
  copy: "M8 8h12v12H8zM4 16V4h12",
  send: "M22 2 11 13M22 2l-7 20-4-9-9-4z",
  inbox: "M22 12h-6l-2 3h-4l-2-3H2M5.5 5h13L22 12v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6z",
  file: "M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6",
  trash: "M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v6M14 11v6",
};

export function Icon({ name, size = 16 }: { name: keyof typeof paths | string; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden className="icon">
      <path d={paths[name] ?? ""} />
    </svg>
  );
}
