import { useEffect, useState, type ReactNode } from "react";
import { useLocation } from "react-router-dom";

// Rendering a whole library at once is what made Movies slow to open: 2,500 cards are
// ~50,000 DOM nodes and most of a second of main-thread work on a fast desktop (several
// seconds on a laptop or phone) before anything can be clicked. useProgressive renders the
// first few screens and adds more as the list scrolls towards its end, well before the
// last card is reached, so scrolling never meets a gap.
//
// FIRST and STEP are a few screens of cards and divide evenly by the usual column counts
// (2, 3, 4, 6, 7, 12), so a batch ends on a full row.
export const FIRST = 84;
const STEP = 84;
// How far below the visible part of the list the next batch is added.
const AHEAD = "1600px";
const STORE = "arrmada.listCount";
const CAP = 50;

type Saved = Record<string, number>;
function load(): Saved {
  try { return JSON.parse(sessionStorage.getItem(STORE) ?? "{}") as Saved; } catch { return {}; }
}
function save(key: string, n: number) {
  const next: Saved = { ...load() };
  delete next[key];
  next[key] = n;
  const keys = Object.keys(next);
  for (const k of keys.slice(0, Math.max(0, keys.length - CAP))) delete next[k];
  try { sessionStorage.setItem(STORE, JSON.stringify(next)); } catch { /* storage blocked */ }
}

// scrollParent is the element the list scrolls in: the shell's <main>, not the window.
// An observer watching the window would never see the end of a list inside it.
function scrollParent(el: HTMLElement): HTMLElement | null {
  for (let p = el.parentElement; p; p = p.parentElement) {
    const oy = getComputedStyle(p).overflowY;
    if (oy === "auto" || oy === "scroll" || oy === "overlay") return p;
  }
  return null;
}

/**
 * useProgressive returns the part of `items` to render and a marker to place after the
 * list. `id` tells apart two lists on one page; `resetOn` (the filter, search and sort)
 * starts a changed list from the top again.
 *
 * Each history entry remembers how far its list had grown, so Back to a page scrolled
 * deep renders that much straight away and the scroll position can be put back.
 */
export function useProgressive<T>(items: T[], id: string, resetOn: string): { shown: T[]; sentinel: ReactNode } {
  const key = `${useLocation().key}:${id}`;
  const [count, setCount] = useState(() => Math.max(FIRST, load()[key] ?? 0));
  // A new filter, search or sort shows its own first screens (adjusted during render, so
  // the long list never paints once more first).
  const [seen, setSeen] = useState(resetOn);
  if (seen !== resetOn) {
    setSeen(resetOn);
    setCount(FIRST);
  }
  const total = items.length;
  const more = count < total;

  useEffect(() => {
    if (count > FIRST || load()[key] !== undefined) save(key, count);
  }, [key, count]);

  // The marker's element in state, not a ref: it comes and goes (grid ↔ table, the
  // list's loading and empty states) and each new one needs watching.
  const [el, setEl] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!more || !el) return;
    // No IntersectionObserver (an old browser, the test DOM): everything, as before.
    if (typeof IntersectionObserver === "undefined") {
      setCount(total);
      return;
    }
    // A fresh observer per batch reports straight away whether the marker is still near:
    // a tall screen keeps adding batches until it's filled.
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) setCount((c) => Math.min(Math.max(c, FIRST) + STEP, total));
    }, { root: scrollParent(el), rootMargin: `0px 0px ${AHEAD} 0px` });
    io.observe(el);
    return () => io.disconnect();
  }, [el, more, count, total]);

  return {
    shown: more ? items.slice(0, count) : items,
    sentinel: more ? <div ref={setEl} aria-hidden="true" className="h-px" /> : null,
  };
}
