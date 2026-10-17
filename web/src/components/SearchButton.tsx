import { openPalette, paletteShortcut } from "../lib/paletteOpen";

// SearchButton opens the command palette from a top bar (the requester shell's, and the
// staff console's on a narrow window). Kept to the button alone: it is in a requester's
// first load, the palette is not.
export function SearchButton() {
  return (
    <button
      type="button"
      onClick={() => openPalette()}
      title={`Search everything (${paletteShortcut()})`}
      aria-label="Search everything"
      aria-keyshortcuts="Meta+K Control+K"
      className="grid h-9 w-9 place-items-center rounded-lg"
      style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink)" }}
    >
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden>
        <circle cx="11" cy="11" r="7" />
        <path d="M20 20l-3.5-3.5" />
      </svg>
    </button>
  );
}
