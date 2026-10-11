import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";

// The series page (SER-14): a hero with the next episode, monitored progress and size;
// three primary actions (Monitor, Search missing, Choose release…) plus one overflow menu
// that holds every other action, reachable by keyboard and by touch at 375px.
// SER-15: a season rail (the choice kept in the address) and episode cards with stills,
// overviews and one menu each; a long anime season pages in 50 at a time.

async function fitsScreen(page: Page) {
  const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
  expect(w.doc, "document width").toBeLessThanOrEqual(w.viewport);
}

const OVERFLOW = ["Refresh & rescan", "Rename…", "Manual import…", "Upload torrent…", "Switch to anime numbering", "Delete…"];

test.describe("desktop", () => {
  test.skip(({ isMobile }) => isMobile, "desktop project only");

  test("the hero shows the next episode, progress and size, with three actions and an overflow", async ({ page, api }) => {
    await page.goto("/series/1");
    const facts = page.getByTestId("series-facts");
    await expect(facts).toContainText("Next: S03E03");
    await expect(facts).toContainText("17/21");
    await expect(facts).toContainText("monitored episodes");
    await expect(facts).toContainText("GB");

    await expect(page.getByRole("button", { name: "Search missing" })).toBeVisible();
    await expect(page.getByRole("group", { name: "Show actions" }).getByRole("button", { name: "Choose release…" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Monitoring", exact: true })).toBeVisible();
    // The old nine-button toolbar is gone.
    for (const old of ["Auto-grab missing", "Upload torrent", "Manual import", "Refresh & rescan", "Search indexers"]) {
      await expect(page.getByRole("button", { name: old, exact: true })).toHaveCount(0);
    }

    // The keyboard opens the overflow, walks it, and Escape closes it back onto the trigger.
    const more = page.getByRole("button", { name: "More actions" });
    await more.focus();
    await page.keyboard.press("Enter");
    const menu = page.getByRole("menu", { name: "More actions" });
    await expect(menu).toBeVisible();
    for (const label of OVERFLOW) await expect(menu.getByRole("menuitem", { name: label })).toBeVisible();
    await expect(menu.getByRole("menuitem", { name: "Refresh & rescan" })).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(menu.getByRole("menuitem", { name: "Rename…" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
    await expect(more).toBeFocused();

    // Each item does its old job: Refresh & rescan refreshes.
    await more.click();
    await page.getByRole("menuitem", { name: "Refresh & rescan" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/series/1/refresh").length).toBe(1);

    // Search missing is the whole-show search.
    await page.getByRole("button", { name: "Search missing" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/series/1/search").length).toBe(1);
    expect(api.callsTo("POST", "/api/v1/series/1/search")[0].body).toBeFalsy();
  });

  test("the Monitor menu pauses, applies a preset and toggles new seasons", async ({ page, api }) => {
    await page.goto("/series/1");
    const monitor = page.getByRole("button", { name: "Monitoring", exact: true });
    await monitor.click();
    await page.getByRole("menuitem", { name: /Monitor: Future episodes/ }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/series/1/monitor").map((c) => c.body)).toContainEqual({ preset: "future" });

    await monitor.click();
    await page.getByRole("menuitem", { name: /^Pause/ }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/series/1/monitor").map((c) => c.body)).toContainEqual({ monitored: false });

    await monitor.click();
    await page.getByRole("menuitem", { name: /Monitor new seasons/ }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/series/1/monitor").map((c) => c.body)).toContainEqual({ monitor_new_seasons: false });
  });
});

test.describe("desktop seasons", () => {
  test.skip(({ isMobile }) => isMobile, "desktop project only");

  test("opens on the first season with a gap, with stills and overviews showing", async ({ page }) => {
    await page.goto("/series/1");
    await expect(page.getByRole("button", { name: /^Season 2/, pressed: true })).toBeVisible();
    const cards = page.getByTestId("episode-card");
    await expect(cards).toHaveCount(8);
    const first = cards.first();
    await expect(first.locator("img")).toHaveAttribute("src", /\/t\/p\/w300\//);
    await expect(first).toContainText("What happens in S2E1");
    // E04 has no still: the warm fallback names the episode instead.
    await expect(cards.nth(3).locator("img")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Grab again" })).toBeVisible();
  });

  test("the season is kept in the address, and Specials have no Grab missing", async ({ page }) => {
    await page.goto("/series/1");
    await page.getByRole("button", { name: /^Season 3/ }).click();
    await expect(page).toHaveURL(/season=3/);
    await expect(page.getByTestId("episode-card")).toHaveCount(4);
    await page.reload();
    await expect(page.getByRole("button", { name: /^Season 3/, pressed: true })).toBeVisible();

    await page.getByRole("button", { name: /^Specials/ }).click();
    await expect(page.getByTestId("episode-card")).toHaveCount(2);
    await expect(page.getByRole("button", { name: /^Grab (missing|again)$/ })).toHaveCount(0);
  });

  test("a long anime season pages in 50 at a time", async ({ page }) => {
    await page.goto("/series/2");
    const cards = page.getByTestId("episode-card");
    await expect(cards).toHaveCount(50);
    await page.getByRole("button", { name: "Show more" }).click();
    await expect(cards).toHaveCount(100);
    await expect(page.getByText("100 of 130")).toBeVisible();
  });

  test("each episode's menu holds its actions", async ({ page, api }) => {
    await page.goto("/series/1");
    await page.getByRole("button", { name: "Actions for S02E06" }).click();
    const menu = page.getByRole("menu", { name: "Actions for S02E06" });
    await expect(menu.getByRole("menuitem")).toHaveText(["Grab again", "Choose release…", "Stop monitoring"]);
    await menu.getByRole("menuitem", { name: "Grab again" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/series/1/autograb").map((c) => c.body)).toContainEqual({ season: 2, episode: 6 });

    await page.getByRole("button", { name: "Actions for S02E01" }).click();
    await expect(page.getByRole("menu", { name: "Actions for S02E01" }).getByRole("menuitem")).toHaveText(["Choose release…", "Replace…", "Delete file…", "File details", "Stop monitoring"]);
    await page.getByRole("menuitem", { name: "Delete file…" }).click();
    await expect(page.getByRole("dialog")).toContainText("Delete the file for S02E01?");
  });
});

test.describe("phone", () => {
  test.skip(({ isMobile }) => !isMobile, "phone project only");

  test("the hero and its menus fit a 375px screen and work by touch", async ({ page }) => {
    await page.goto("/series/1");
    await expect(page.getByTestId("series-facts")).toContainText("Next: S03E03");
    await fitsScreen(page);

    await page.getByRole("button", { name: "More actions" }).tap();
    const menu = page.getByRole("menu", { name: "More actions" });
    for (const label of OVERFLOW) await expect(menu.getByRole("menuitem", { name: label })).toBeInViewport();
    await fitsScreen(page);
    await menu.getByRole("menuitem", { name: "Delete…" }).tap();
    await expect(page.getByRole("dialog")).toBeVisible();
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: "Monitoring", exact: true }).tap();
    const mon = page.getByRole("menu", { name: "Monitoring" });
    await expect(mon.getByRole("menuitem", { name: /Monitor: All episodes/ })).toBeInViewport();
    await fitsScreen(page);
  });

  test("episode cards: the overview opens on a tap and the menu works by touch", async ({ page }) => {
    await page.goto("/series/1");
    const card = page.getByTestId("episode-card").first();
    await expect(card.locator("img")).toBeVisible();
    const overview = card.getByRole("button", { name: /What happens in S2E1/ });
    await expect(overview).toHaveAttribute("aria-expanded", "false");
    await overview.tap();
    await expect(overview).toHaveAttribute("aria-expanded", "true");
    await fitsScreen(page);

    await card.getByRole("button", { name: "Actions for S02E01" }).tap();
    const menu = page.getByRole("menu", { name: "Actions for S02E01" });
    for (const item of ["Choose release…", "Replace…", "Delete file…", "File details", "Stop monitoring"]) {
      await expect(menu.getByRole("menuitem", { name: item })).toBeInViewport();
    }
    await fitsScreen(page);
  });
});
