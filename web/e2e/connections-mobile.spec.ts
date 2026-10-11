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

    await page.getByRole("button", { name: "+ Add indexer" }).click();
    await expect(page.getByRole("form", { name: "Add an indexer" })).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });
});
