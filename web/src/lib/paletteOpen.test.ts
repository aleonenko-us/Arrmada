// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { PALETTE_EVENT, openPalette, paletteHostMounted, paletteShown } from "./paletteOpen";

// The shortcut only acts while a shell's palette host is mounted, never over another
// dialog, survives keydown events without a key (autofill) and works by key position on
// non-Latin layouts. (jsdom's platform isn't a Mac, so the modifier is Ctrl.)

function listen() {
  const got: (string | null)[] = [];
  const on = (e: Event) => got.push((e as CustomEvent<string | null>).detail);
  window.addEventListener(PALETTE_EVENT, on);
  return { got, stop: () => window.removeEventListener(PALETTE_EVENT, on) };
}

function press(init: KeyboardEventInit) {
  const e = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
  document.dispatchEvent(e);
  return e;
}

afterEach(() => {
  paletteHostMounted(false);
});

describe("the palette shortcut", () => {
  it("does nothing, and leaves the key alone, until a host is mounted", () => {
    const l = listen();
    const e = press({ key: "k", ctrlKey: true });
    expect(e.defaultPrevented).toBe(false);
    expect(l.got).toEqual([]);
    // Nor is the press saved for later.
    expect(paletteHostMounted(true)).toBeNull();
    l.stop();
  });

  it("opens with Ctrl K, closes with it when up, and needs the modifier", () => {
    paletteHostMounted(true);
    const l = listen();
    expect(press({ key: "k" }).defaultPrevented).toBe(false);
    expect(press({ key: "k", ctrlKey: true, shiftKey: true }).defaultPrevented).toBe(false);
    expect(press({ key: "K", ctrlKey: true }).defaultPrevented).toBe(true);
    paletteShown(true);
    press({ key: "k", ctrlKey: true });
    expect(l.got).toEqual(["", null]);
    l.stop();
  });

  it("works by key position on another layout and ignores events with no key", () => {
    paletteHostMounted(true);
    const l = listen();
    press({ key: "л", code: "KeyK", ctrlKey: true });
    expect(() => press({ ctrlKey: true } as KeyboardEventInit)).not.toThrow();
    const autofill = new Event("keydown", { bubbles: true, cancelable: true });
    expect(() => document.dispatchEvent(autofill)).not.toThrow();
    expect(l.got).toEqual([""]);
    l.stop();
  });

  it("leaves the keyboard to another open dialog", async () => {
    paletteHostMounted(true);
    const { useDialogA11y } = await import("./useDialogA11y");
    const { renderHook } = await import("@testing-library/react");
    const el = document.createElement("div");
    document.body.appendChild(el);
    const { unmount } = renderHook(() => useDialogA11y({ current: el }, { open: true, onClose: () => {} }));
    const l = listen();
    expect(press({ key: "k", ctrlKey: true }).defaultPrevented).toBe(false);
    expect(l.got).toEqual([]);
    unmount();
    l.stop();
    // The first import of @testing-library/react can take over 5 s on a loaded machine.
  }, 30_000);

  it("hands a button press from before the host mounted to the host", () => {
    openPalette("dune");
    expect(paletteHostMounted(true)).toBe("dune");
    expect(paletteHostMounted(true)).toBeNull();
  });
});
