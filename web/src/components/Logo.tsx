import { useId } from "react";

/** Gorget mark: three articulated lames, heat-tinted like tempered steel. */
export function Logo({ size = 28 }: { size?: number }) {
  const id = useId();
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <defs>
        <linearGradient id={id} x1="0" x2="1">
          <stop offset="0" stopColor="#3A55B4" />
          <stop offset=".55" stopColor="#7B4FA8" />
          <stop offset="1" stopColor="#C49A3A" />
        </linearGradient>
      </defs>
      <rect width="32" height="32" rx="7" fill="#151A21" />
      <g fill="none" stroke={`url(#${id})`} strokeWidth="3.2" strokeLinecap="round">
        <path d="M6 11a10 10 0 0 0 20 0" />
        <path d="M8.5 16.5a7.5 7.5 0 0 0 15 0" opacity=".8" />
        <path d="M11.5 21.5a4.5 4.5 0 0 0 9 0" opacity=".6" />
      </g>
    </svg>
  );
}

export function Wordmark() {
  return (
    <div className="flex items-center gap-2.5">
      <Logo />
      <span className="font-display text-[19px] font-semibold tracking-tight">Gorget</span>
    </div>
  );
}
