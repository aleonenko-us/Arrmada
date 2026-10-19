import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { activityTwoClients, STALLED_QUEUE_HASH } from "./fixtures/media";

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

  // ACQ-30 at 375 px: each stuck download keeps its main action on the row, the rest in ⋯.
  test("Needs you's actions fit a phone", async ({ page, api }) => {
    await page.goto("/activity/needs-you");
    const stalled = page.locator("#stalled").getByTestId("needs-you-item");
    await expect(stalled.getByRole("button", { name: "Try another release" })).toBeVisible();
    await expect(stalled.getByRole("button", { name: "Reannounce" })).toHaveCount(0);
    await stalled.getByRole("button", { name: /^More actions for/ }).click();
    await page.getByRole("menuitem", { name: "Reannounce" }).click();
    await expect(page.getByText("Asked the trackers for peers again.")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/queue/")).not.toHaveLength(0);
    await expect(page.locator("#wrong-category").getByRole("button", { name: "Move to TV" })).toBeVisible();
    await expect(page.locator("#import-failures").getByRole("button", { name: "Retry now" })).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });

  // The Stalled pill and the client chips at 375 px: the pills wrap rather than push the
  // page sideways, and a stalled row keeps its chip with Try another release in the ⋯ menu.
  test("the Stalled pill filters, and a stalled row fits with its actions in the menu", async ({ page, api }) => {
    await page.goto("/activity/queue");
    const views = page.getByRole("group", { name: "Queue views" });
    await expect(views.getByRole("button", { name: /^Stalled/ })).toHaveText(/^Stalled\s*1$/);
    expect(await overflow(page)).toBeLessThanOrEqual(0);
    await views.getByRole("button", { name: /^Stalled/ }).click();
    await expect(page).toHaveURL(/\?show=stalled$/);
    const row = page.getByTestId("transfer-card");
    await expect(row).toHaveCount(1);
    await expect(row.getByTestId("stalled-chip")).toHaveText("Stalled 2h · 0 seeds");
    await expect(row.getByRole("button", { name: "Try another release" })).toHaveCount(0);
    await row.getByRole("button", { name: "More actions for Old Film" }).click();
    await page.getByRole("menuitem", { name: "Try another release" }).click();
    await expect(page.getByText(/Grabbed Old\.Film\.1080p\.WEB-DL-NEW instead/)).toBeVisible();
    expect(api.callsTo("POST", `/api/v1/queue/${STALLED_QUEUE_HASH}/failover`)).toHaveLength(1);
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });

  test("with two download clients the rows name theirs and the filter fits", async ({ page }) => {
    await page.route("**/api/v1/downloads", (route) => route.fulfill({ json: activityTwoClients }));
    await page.goto("/activity/queue");
    await expect(page.getByTestId("transfer-card").filter({ hasText: "Old.Film.2019" }).getByTestId("client-chip")).toHaveText("qBittorrent 2");
    const filter = page.getByRole("combobox", { name: "Download client" });
    await expect(filter).toBeVisible();
    expect(await overflow(page)).toBeLessThanOrEqual(0);
    await filter.selectOption({ label: "qBittorrent 2 (2)" });
    await expect(page.getByTestId("transfer-card")).toHaveCount(1);
    expect(await overflow(page)).toBeLessThanOrEqual(0);
  });

  test("with one download client the rows don't name it", async ({ page }) => {
    await page.goto("/activity/queue");
    await expect(page.getByTestId("transfer-card").first()).toBeVisible();
    await expect(page.getByTestId("client-chip")).toHaveCount(0);
  });
});
