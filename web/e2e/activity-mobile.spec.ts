import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";

// The Activity page on a 375 px phone. ACQ-29: a transfer card keeps its poster and title
// readable, and every action but the main one folds into the ⋯ menu.

// The layout viewport, not innerWidth: a phone zooms out to fit an overflowing page.
const overflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

test.describe("manager on a phone", () => {
  test.use({ persona: "manager" });

  test("a transfer keeps its title readable and its secondary actions in the ⋯ menu", async ({ page }) => {
    await page.goto("/activity/queue");
    const card = page.getByTestId("transfer-card").filter({ hasText: "Driftwood.2025" });
    const title = card.getByRole("link", { name: "Driftwood" });
    await expect(title).toBeVisible();
    await expect(title).toHaveAttribute("href", "/movies/3");
    expect((await title.boundingBox())?.width ?? 0).toBeGreaterThan(50);
    await expect(card.getByTestId("transfer-scope")).toHaveText("4K");
    await expect(card.getByTestId("transfer-requester")).toHaveText("for Mum");
    // Pause stays on the row; Block and Delete moved into the menu.
    await expect(card.getByRole("button", { name: "Pause" })).toBeVisible();
    await expect(card.getByRole("button", { name: "Block" })).toHaveCount(0);
    await card.getByRole("button", { name: "More actions for Driftwood" }).click();
    await expect(page.getByRole("menuitem", { name: "Block" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Delete" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Reannounce" })).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });

  test("seeding cards fit too, and a torrent no grab knows keeps its release name", async ({ page }) => {
    await page.goto("/activity/queue?show=seeding");
    const harbour = page.getByTestId("transfer-card").filter({ hasText: "Harbour.Lights" });
    await expect(harbour.getByRole("link", { name: "Harbour Lights" })).toHaveAttribute("href", "/series/1");
    await expect(harbour.getByTestId("transfer-requester")).toHaveText("for Dad and Gran");
    const iso = page.getByTestId("transfer-card").filter({ hasText: "Some.Linux.Distro.ISO-FIXTURE" });
    await expect(iso.getByRole("link")).toHaveCount(0);
    await expect(iso.getByTestId("transfer-title")).toHaveText("Some.Linux.Distro.ISO-FIXTURE");
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });
});
