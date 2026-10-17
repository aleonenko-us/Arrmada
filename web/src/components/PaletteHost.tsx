import { lazy, Suspense, useEffect, useState, type ComponentType } from "react";
import { PALETTE_EVENT, openPalette, paletteHostMounted, paletteShortcut, paletteShown } from "../lib/paletteOpen";

// The command palette (⌘K / Ctrl K): search titles, pages, settings and a few actions from
// anywhere. The shortcut lives in lib/paletteOpen.ts; this host mounts the palette when
// asked, and the palette itself is its own chunk, fetched the first time it opens. The
// requester shell loads this host lazily too, so a requester's first load carries only the
// search button. A chunk that can't load (a deploy replaced it) leaves the palette closed
// rather than taking the page down with it.
type PaletteProps = { initialQuery?: string; onClose: () => void };
const CommandPalette = lazy<ComponentType<PaletteProps>>(() => import("./palette/CommandPalette").catch(() => ({ default: () => null })));

// PaletteHost mounts the palette when asked. Each shell renders exactly one.
export function PaletteHost() {
  const [query, setQuery] = useState<string | null>(null);
  useEffect(() => {
    const onEvent = (e: Event) => setQuery((e as CustomEvent<string | null>).detail);
    window.addEventListener(PALETTE_EVENT, onEvent);
    // A search button pressed before this chunk had loaded. (The shortcut only works
    // from here on.)
    const early = paletteHostMounted(true);
    if (early !== null) setQuery(early);
    return () => {
      window.removeEventListener(PALETTE_EVENT, onEvent);
      paletteHostMounted(false);
    };
  }, []);
  useEffect(() => paletteShown(query !== null), [query]);
  if (query === null) return null;
  return (
    <Suspense fallback={null}>
      <CommandPalette initialQuery={query} onClose={() => setQuery(null)} />
    </Suspense>
  );
}

// SearchBar is the staff sidebar's way in: a search field look-alike showing the shortcut.
export function SearchBar({ onOpen }: { onOpen?: () => void }) {
  const keys = paletteShortcut();
  return (
    <button
      type="button"
      onClick={() => { onOpen?.(); openPalette(); }}
      title={`Search titles, pages and settings (${keys})`}
      aria-keyshortcuts="Meta+K Control+K"
      className="flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 text-left text-base text-ink-dim"
      style={{ background: "var(--panel-2)", border: "1px solid var(--line)" }}
    >
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden>
        <circle cx="11" cy="11" r="7" />
        <path d="M20 20l-3.5-3.5" />
      </svg>
      <span className="flex-1">Search</span>
      <kbd className="rounded px-1.5 font-mono text-xs" style={{ border: "1px solid var(--line)" }}>{keys}</kbd>
    </button>
  );
}
