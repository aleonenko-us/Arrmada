import { openDialogCount } from "./useDialogA11y";

// The command palette's front door, in everyone's first load: the shortcut, the event that
// opens it, how the shortcut is written and where each account's recent picks are kept.
// The palette host (components/PaletteHost.tsx) is a lazy chunk of its own; a search
// button pressed before it has mounted waits here for it.

export const PALETTE_EVENT = "arrmada:palette";

// Apple keyboards use ⌘K; Ctrl K there stays the text fields' "delete to end of line".
const MAC = typeof navigator !== "undefined" && /Mac|iP/.test(navigator.platform);

let hostMounted = false;
let pending: string | null = null;
let shown = false;

// The event's detail is the query to open with, or null to close.
const send = (detail: string | null) => window.dispatchEvent(new CustomEvent(PALETTE_EVENT, { detail }));

/** Opens the palette from a button or the Settings search, optionally with a query. */
export function openPalette(query = ""): void {
  if (!hostMounted) pending = query;
  send(query);
}

/**
 * The host mounted (true) or went away (false). Mounting hands over a button press that
 * arrived while its chunk was still loading.
 */
export function paletteHostMounted(on: boolean): string | null {
  hostMounted = on;
  if (!on) {
    shown = false;
    return null;
  }
  const q = pending;
  pending = null;
  return q;
}

/** The host says whether the palette is up, so the shortcut knows to close it. */
export function paletteShown(v: boolean): void {
  shown = v;
}

/** The shortcut as this keyboard writes it: ⌘K on Apple devices, Ctrl K elsewhere. */
export function paletteShortcut(): string {
  return MAC ? "⌘K" : "Ctrl K";
}

/** Where an account's recent picks are kept (lib/palette.ts reads and writes them). */
export const paletteRecentsKey = (userId: number) => `arrmada.palette.recent.${userId}`;

// ⌘K / Ctrl K: opens the palette, or closes it when it's up. Only while a shell's host is
// mounted (never on the sign-in or setup screens, and a press is never saved for later),
// and never over another dialog (a confirm, a form), which keeps the keyboard. The key's
// position (KeyK) counts too, so a Cyrillic or Greek layout works. Autofill fires keydown
// events without a key, hence the type check.
if (typeof document !== "undefined") {
  document.addEventListener("keydown", (e) => {
    if (!hostMounted || typeof e.key !== "string") return;
    if ((e.key.toLowerCase() !== "k" && e.code !== "KeyK") || !(MAC ? e.metaKey : e.ctrlKey) || e.altKey || e.shiftKey) return;
    if (shown) {
      e.preventDefault();
      send(null);
    } else if (openDialogCount() === 0) {
      e.preventDefault();
      send("");
    }
  });
}
