import { useSyncExternalStore } from "react";

// The colour theme: follow the device, or a fixed Light or Dark. It is remembered per
// browser in localStorage (the inline script in index.html applies it before first
// paint, so a reload doesn't flash the other theme). Storage can be blocked; the choice
// then still holds for this page's lifetime, it just isn't remembered.
export type ThemePref = "system" | "light" | "dark";

export const THEME_KEY = "arrmada.theme";
export const THEME_LABEL: Record<ThemePref, string> = { system: "System", light: "Light", dark: "Dark" };

const ORDER: ThemePref[] = ["system", "light", "dark"];

// nextTheme is what the theme button switches to: System → Light → Dark → System.
export function nextTheme(p: ThemePref): ThemePref {
  return ORDER[(ORDER.indexOf(p) + 1) % ORDER.length];
}

function parse(v: string | null): ThemePref {
  return v === "light" || v === "dark" ? v : "system";
}

export function readTheme(): ThemePref {
  try {
    return parse(localStorage.getItem(THEME_KEY));
  } catch {
    return "system"; // storage blocked
  }
}

let pref: ThemePref = "system";
const subs = new Set<() => void>();

// The browser's own chrome (the address bar, and the status bar of the installed app)
// takes the sidebar/header colour of whichever theme is showing now. The manifest's
// theme_color can't change, so this meta tag is what makes the light theme's bar light.
function syncChrome() {
  const c = getComputedStyle(document.documentElement).getPropertyValue("--sidebar").trim();
  const m = document.querySelector('meta[name="theme-color"]');
  if (c && m) m.setAttribute("content", c);
}

function apply(p: ThemePref) {
  const root = document.documentElement;
  pref = p;
  // System removes the stamp, so index.css's prefers-color-scheme blocks decide.
  if (p === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", p);
  syncChrome();
  subs.forEach((f) => f());
}

export function setTheme(p: ThemePref) {
  try {
    if (p === "system") localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, p);
  } catch { /* blocked: it still applies for this session */ }
  apply(p);
}

// initTheme runs once at boot: apply the remembered choice, and keep the browser chrome
// in step when the device flips light/dark while on System.
export function initTheme() {
  apply(readTheme());
  try {
    matchMedia("(prefers-color-scheme: light)").addEventListener("change", () => {
      if (pref === "system") syncChrome();
    });
  } catch { /* an old browser without it: the bar just keeps its colour until a reload */ }
}

const subscribe = (f: () => void) => {
  subs.add(f);
  return () => { subs.delete(f); };
};

export function useTheme(): [ThemePref, (p: ThemePref) => void] {
  return [useSyncExternalStore(subscribe, () => pref), setTheme];
}
