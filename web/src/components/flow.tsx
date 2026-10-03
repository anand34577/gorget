import * as React from "react";
import { cn } from "@/lib/utils";

export type FlowState = "ok" | "wait" | "off" | "warn";

export interface FlowStep {
  icon: React.ReactNode;
  title: string;
  sub?: React.ReactNode;
  state?: FlowState;
}

const ring: Record<FlowState, string> = {
  ok: "border-verdigris/60 bg-verdigris-soft text-verdigris",
  wait: "border-straw/60 bg-straw-soft text-straw",
  warn: "border-oxide/50 bg-oxide-soft text-oxide",
  off: "border-line bg-surface-2 text-ink-3",
};

/**
 * A left-to-right picture of how traffic travels ("your devices, the server, the router, the home
 * network"). Each stop shows its state; the line between two stops is alive when both are.
 */
export function Flow({ steps, className }: { steps: FlowStep[]; className?: string }) {
  return (
    <ol className={cn("flex flex-wrap items-stretch justify-center gap-y-3", className)} aria-label="How traffic travels">
      {steps.map((s, i) => {
        const state = s.state ?? "ok";
        const prev = steps[i - 1]?.state ?? "ok";
        const live = i > 0 && state === "ok" && prev === "ok";
        const pending = i > 0 && !live && state !== "off" && prev !== "off";
        return (
          <React.Fragment key={i}>
            {i > 0 && (
              <li aria-hidden className="flex w-10 items-center sm:w-14">
                <span
                  className={cn("h-0.5 w-full rounded", live ? "bg-verdigris" : "border-t-2 border-dashed border-line-strong", pending && "animate-pulse border-straw")}
                  style={live ? { backgroundImage: "linear-gradient(90deg, var(--verdigris) 50%, transparent 50%)", backgroundSize: "10px 2px", animation: "flowdash 0.9s linear infinite" } : undefined}
                />
              </li>
            )}
            <li className="flex w-32 flex-col items-center text-center sm:w-36">
              <span className={cn("grid size-12 place-items-center rounded-2xl border [&_svg]:size-5", ring[state])}>{s.icon}</span>
              <span className="mt-1.5 text-[12.5px] font-medium leading-tight">{s.title}</span>
              {s.sub && <span className="mt-0.5 text-[11px] leading-tight text-ink-3">{s.sub}</span>}
              <span className="sr-only">{state === "ok" ? "working" : state === "wait" ? "waiting" : state === "warn" ? "problem" : "not connected"}</span>
            </li>
          </React.Fragment>
        );
      })}
    </ol>
  );
}
