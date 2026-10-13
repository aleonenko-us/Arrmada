// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// FE-05: the theme choice survives a reload, still works when storage is blocked, and
// the browser chrome colour follows the theme showing (including the device flipping
// while on System).

const SIDEBAR = { light: "#fbf8f1", dark: "#1f1812" };
let osLight = false;
let mediaListeners: (() => void)[] = [];

function meta() {
  return document.querySelector('meta[name="theme-color"]')!.getAttribute("content");
}

// load imports a fresh copy of the module (its choice lives in module state).
async function load() {
  vi.resetModules();
  return import("./theme");
}

beforeEach(() => {
  osLight = false;
  mediaListeners = [];
  localStorage.clear();
  document.documentElement.removeAttribute("data-theme");
  document.head.innerHTML = '<meta name="theme-color" content="#000000">';
  // jsdom has no matchMedia and doesn't resolve index.css: stand in for both. --sidebar
  // resolves the way index.css does — data-theme wins, else the device preference.
  vi.stubGlobal("matchMedia", (q: string) => ({
    matches: q.includes("light") ? osLight : !osLight,
    addEventListener: (_: string, f: () => void) => mediaListeners.push(f),
  }));
  vi.spyOn(window, "getComputedStyle").mockImplementation(() => ({
    getPropertyValue: (name: string) => {
      if (name !== "--sidebar") return "";
      const stamped = document.documentElement.getAttribute("data-theme");
      return stamped === "light" || stamped === "dark" ? SIDEBAR[stamped] : osLight ? SIDEBAR.light : SIDEBAR.dark;
    },
  }) as unknown as CSSStyleDeclaration);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("theme", () => {
  it("cycles System → Light → Dark → System", async () => {
    const { nextTheme } = await load();
    expect(nextTheme("system")).toBe("light");
    expect(nextTheme("light")).toBe("dark");
    expect(nextTheme("dark")).toBe("system");
  });

  it("remembers the choice across a reload and colours the browser chrome", async () => {
    let t = await load();
    t.initTheme();
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
    t.setTheme("light");
    expect(localStorage.getItem(t.THEME_KEY)).toBe("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(meta()).toBe(SIDEBAR.light);

    // A reload: a fresh module reads the stored choice.
    document.documentElement.removeAttribute("data-theme");
    t = await load();
    t.initTheme();
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(t.readTheme()).toBe("light");

    // Back to System forgets the stamp and the stored value.
    t.setTheme("system");
    expect(localStorage.getItem(t.THEME_KEY)).toBeNull();
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
    expect(meta()).toBe(SIDEBAR.dark);
  });

  it("ignores a junk stored value", async () => {
    localStorage.setItem("arrmada.theme", "neon");
    const t = await load();
    expect(t.readTheme()).toBe("system");
  });

  it("still switches for the session when storage is blocked", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("blocked"); });
    const t = await load();
    expect(() => t.initTheme()).not.toThrow();
    expect(() => t.setTheme("dark")).not.toThrow();
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(meta()).toBe(SIDEBAR.dark);
    expect(() => t.setTheme("system")).not.toThrow();
  });

  it("on System, follows the device flipping light/dark", async () => {
    const t = await load();
    t.initTheme();
    expect(meta()).toBe(SIDEBAR.dark);
    osLight = true;
    mediaListeners.forEach((f) => f());
    expect(meta()).toBe(SIDEBAR.light);

    // A fixed choice doesn't move when the device does.
    t.setTheme("dark");
    osLight = false;
    mediaListeners.forEach((f) => f());
    osLight = true;
    mediaListeners.forEach((f) => f());
    expect(meta()).toBe(SIDEBAR.dark);
  });
});
