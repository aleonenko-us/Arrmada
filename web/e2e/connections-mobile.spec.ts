import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";

// Settings → Connections on a 375 px phone (INT-16/17): one column of cards, nothing makes
// the page scroll sideways, with or without an editor open.

const overflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

test.describe("admin on a phone", () => {
  test.use({ persona: "admin" });

  test("cards and editors fit a 375 px screen", async ({ page, api }) => {
    await page.goto("/settings/connections");
    const qb = page.locator("#download_client-1");
    await expect(qb.getByText(/connection refused/)).toBeVisible();
    await api.quiet();
    expect(await overflow(page)).toBeLessThanOrEqual(0);

    // One column: the cards stack.
    const a = await page.locator("#flaresolverr").boundingBox();
    const b = await page.locator("#indexer-1").boundingBox();
    expect(a && b && b.y > a.y + a.height - 1).toBe(true);

    await qb.getByRole("button", { name: "Fix" }).click();
    await expect(qb.getByLabel(/Order \(1 = first\)/)).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);

    await page.locator("#apikey-tmdb").getByRole("button", { name: "Edit" }).click();
    await expect(page.locator("#apikey-tmdb").getByRole("button", { name: "Save" })).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);

    // The Plex connection opens in its card too (CFG-16).
    await page.locator("#plex").getByRole("button", { name: "Connect Plex" }).click();
    await expect(page.locator("#plex").getByRole("button", { name: "Sign in with Plex" })).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);

    // INT-18: the add-indexer catalogue and a preset's form fit the phone too.
    await page.getByRole("button", { name: "+ Add indexer" }).click();
    const dialog = page.getByRole("dialog", { name: "Add an indexer" });
    await expect(dialog.getByRole("button", { name: /MyAnonaMouse/ })).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
    await dialog.getByRole("button", { name: /TorrentLeech/ }).click();
    const form = page.getByRole("form", { name: "Add an indexer" });
    await expect(form.getByLabel("Username")).toBeVisible();
    const box = await page.getByRole("dialog").boundingBox();
    expect(box && box.x >= 0 && box.x + box.width <= 375).toBe(true);
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });

  // J5a: the media server cards stack on a phone, and the Jellyfin editor fits.
  test("the media server cards stack on a 375 px screen", async ({ page, api }) => {
    await page.goto("/settings/connections");
    const plex = page.locator("#media-servers #plex");
    const jf = page.locator("#media-servers #jellyfin");
    await expect(jf.getByRole("button", { name: "Connect Jellyfin" })).toBeVisible();
    await api.quiet();
    const a = await plex.boundingBox();
    const b = await jf.boundingBox();
    expect(a && b && b.y >= a.y + a.height - 1 && Math.abs(a.x - b.x) < 2).toBe(true);
    expect(await overflow(page)).toBeLessThanOrEqual(0);

    await jf.getByRole("button", { name: "Connect Jellyfin" }).click();
    await jf.getByLabel("Server URL").fill("http://192.168.1.20:8096");
    await expect(jf.getByText(/only works on your home network/)).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });

  // PLEX-17 / J5b: Settings → Media servers, both groups and every card, fits a phone too,
  // and its jump links reach the Jellyfin group.
  test("Settings → Media servers fits a 375 px screen", async ({ page, api }) => {
    await page.goto("/settings/media-servers");
    await expect(page.locator("#plex-accounts").getByText("Plex · grandadplex")).toBeVisible();
    await expect(page.locator("#plex-tautulli")).toBeVisible();
    await expect(page.locator("#jellyfin-library-updates").getByRole("switch", { name: /Tell Jellyfin to scan after changes/ })).toBeVisible();
    await api.quiet();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
    await page.getByRole("navigation", { name: "Media servers on this page" }).getByRole("link", { name: "Jellyfin" }).click();
    await expect(page.getByRole("heading", { name: "Jellyfin", exact: true })).toBeInViewport();
  });
});
