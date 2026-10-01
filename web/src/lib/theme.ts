import { useEffect, useState } from "react";

export type ThemePref = "system" | "light" | "dark";

function read(): ThemePref {
  try {
    const v = localStorage.getItem("gorget-theme");
    if (v === "light" || v === "dark") return v;
  } catch {
    /* storage unavailable */
  }
  return "system";
}

function apply(pref: ThemePref) {
  const dark = pref === "dark" || (pref === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
}

export function initTheme() {
  apply(read());
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => apply(read()));
}

export function useTheme() {
  const [pref, setPref] = useState<ThemePref>(read);
  useEffect(() => {
    apply(pref);
    try {
      if (pref === "system") localStorage.removeItem("gorget-theme");
      else localStorage.setItem("gorget-theme", pref);
    } catch {
      /* ignore */
    }
  }, [pref]);
  return [pref, setPref] as const;
}
