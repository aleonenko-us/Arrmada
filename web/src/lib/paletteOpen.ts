import { openDialogCount } from "./useDialogA11y";

// The command palette's front door, in everyone's first load: the shortcut, the event that
// opens it and how the shortcut is written. The palette host (components/PaletteHost.tsx)
// is a lazy chunk of its own, so an open asked for before it has loaded waits here for it.

export const PALETTE_EVENT = "arrmada:palette";

let hostReady = false;
let pending: string | null = null;
let shown = false;

// The event's detail is the query to open with, or null to close.
const send = (detail: string | null) => window.dispatchEvent(new CustomEvent(PALETTE_EVENT, { detail }));

/** Opens the palette from anywhere (a button, the Settings search), optionally with a query. */
export function openPalette(query = ""): void {
  if (!hostReady) pending = query;
  send(query);
}

/** The host is listening: hands over an open that arrived before it did. */
export function paletteHostReady(): string | null {
  hostReady = true;
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
  return /Mac|iP/.test(navigator.platform) ? "⌘K" : "Ctrl K";
}

// ⌘K / Ctrl K anywhere: opens the palette, or closes it when it's up. Over another dialog
// (a confirm, a form) it does nothing, so that dialog keeps the keyboard.
if (typeof document !== "undefined") {
  document.addEventListener("keydown", (e) => {
    if (e.key.toLowerCase() !== "k" || !(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return;
    e.preventDefault();
    if (shown) send(null);
    else if (openDialogCount() === 0) openPalette();
  });
}
