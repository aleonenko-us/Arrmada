// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { signOut } from "./session";
import { paletteRecentsKey } from "./paletteOpen";

// Signing out forgets that account's command palette recents, so the next person on this
// browser starts with none; another account's are its own business.
describe("signOut", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("clears the signed-out account's palette recents and nobody else's", async () => {
    vi.spyOn(api, "logout").mockResolvedValue(undefined as never);
    // jsdom can't navigate; the reload to "/" is not what's being tested.
    const loc = window.location;
    Object.defineProperty(window, "location", { configurable: true, value: { ...loc, href: loc.href } });
    localStorage.setItem(paletteRecentsKey(3), "[]");
    localStorage.setItem(paletteRecentsKey(4), "[]");
    await signOut(3);
    expect(localStorage.getItem(paletteRecentsKey(3))).toBeNull();
    expect(localStorage.getItem(paletteRecentsKey(4))).toBe("[]");
    expect(window.location.href).toBe("/");
    Object.defineProperty(window, "location", { configurable: true, value: loc });
  });
});
