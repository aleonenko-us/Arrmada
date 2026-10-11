import { useEffect, useLayoutEffect, useRef, type RefObject } from "react";
import { useLocation, useNavigationType } from "react-router-dom";

const STORE = "arrmada.scroll";
const CAP = 100;
const GIVE_UP_MS = 2000;

type Saved = Record<string, number>;

function load(): Saved {
  try { return JSON.parse(sessionStorage.getItem(STORE) ?? "{}") as Saved; } catch { return {}; }
}

// remember stores one history entry's scroll position, keeping the newest CAP entries
// (an object keeps insertion order, so the oldest go first).
export function remember(saved: Saved, key: string, top: number): Saved {
  const next: Saved = { ...saved };
  delete next[key];
  next[key] = Math.round(top);
  const keys = Object.keys(next);
  for (const k of keys.slice(0, Math.max(0, keys.length - CAP))) delete next[k];
  return next;
}

// isOverlay: the entry is a sheet or overlay over the page that's still under it — a
// useBackToClose sheet ({sheets}) or a Discover title/person/collection ({depth, cold}).
// The page behind must not jump to the top when one opens.
export function isOverlay(state: unknown): boolean {
  if (!state || typeof state !== "object") return false;
  return "sheets" in state || "depth" in state || "cold" in state;
}

// What a navigation does to the scroll position: Back/Forward restore where you were, a
// new page starts at the top, and the same page with a new query (typing in a filter
// box) or an overlay opening over it leaves it where it is.
export function scrollAction(type: "POP" | "PUSH" | "REPLACE", pathChanged: boolean, overlay = false): "restore" | "top" | "keep" {
  if (type === "POP") return "restore";
  if (overlay || !pathChanged) return "keep";
  return "top";
}

// useScrollMemory gives the shell's scrolling <main> what the browser does for a window:
// each history entry remembers its scroll position (in sessionStorage, so a reload keeps
// it), Back and Forward put it back, and following a link opens the page at the top.
// React Router's ScrollRestoration only handles window scroll, and here the window never
// scrolls. A restore that lands before the content has rendered tall enough is retried as
// the content grows, until it fits, the user scrolls, or two seconds pass.
export function useScrollMemory(mainRef: RefObject<HTMLElement>): void {
  const location = useLocation();
  const navType = useNavigationType();
  const prev = useRef<{ key: string; pathname: string } | null>(null);
  const top = useRef(0); // the scroll position of the entry on screen, kept current
  const pending = useRef<(() => void) | null>(null); // stops a restore still waiting for content
  useEffect(() => () => pending.current?.(), []);

  useEffect(() => {
    const el = mainRef.current;
    if (!el) return;
    const onScroll = () => { top.current = el.scrollTop; };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, [mainRef]);

  useLayoutEffect(() => {
    const el = mainRef.current;
    const before = prev.current;
    prev.current = { key: location.key, pathname: location.pathname };
    if (!el || !before || before.key === location.key) return;
    // A new navigation cancels a restore still waiting for the last page's content.
    pending.current?.();
    pending.current = null;
    try { sessionStorage.setItem(STORE, JSON.stringify(remember(load(), before.key, top.current))); } catch { /* storage blocked */ }

    const action = scrollAction(navType, before.pathname !== location.pathname, isOverlay(location.state));
    if (action === "keep") return;
    if (action === "top") { el.scrollTop = 0; top.current = 0; return; }

    // An entry with nothing saved (from before this tab's session) is left where it is.
    const want = load()[location.key];
    if (want === undefined) return;
    el.scrollTop = want;
    top.current = el.scrollTop;
    if (Math.abs(el.scrollTop - want) <= 1) return;
    // Too short yet (the list is still loading): try again as it grows.
    let done = false;
    const stop = () => {
      if (done) return;
      done = true;
      ro?.disconnect();
      clearTimeout(timer);
      for (const ev of ["wheel", "touchstart", "keydown"] as const) el.removeEventListener(ev, stop);
    };
    const retry = () => {
      el.scrollTop = want;
      top.current = el.scrollTop;
      if (Math.abs(el.scrollTop - want) <= 1) stop();
    };
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(retry);
    if (el.firstElementChild) ro?.observe(el.firstElementChild);
    const timer = setTimeout(stop, GIVE_UP_MS);
    for (const ev of ["wheel", "touchstart", "keydown"] as const) el.addEventListener(ev, stop, { passive: true });
    pending.current = stop;
  }, [location.key, location.pathname, navType, mainRef]);
}
