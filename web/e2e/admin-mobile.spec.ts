import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";

// FE-20: the four library pages at phone and tablet width. Nothing scrolls sideways (with
// Select on or off), every "+ Add …" button stays on one line, and a phone gets the grid
// even when the saved view is the table — which the desktop keeps.

const sideways = (page: Page) => page.locator("main").evaluate((el) => el.scrollWidth - el.clientWidth);

const LIBRARIES = [
  { path: "/movies", add: "+ Add movie" },
  { path: "/series", add: "+ Add series" },
  { path: "/books", add: "+ Add book" },
  { path: "/music", add: "+ Add artist" },
];

for (const size of [{ width: 375, height: 812 }, { width: 768, height: 1024 }]) {
  test.describe(`admin at ${size.width}px`, () => {
    test.use({ persona: "admin" });

    for (const lib of LIBRARIES) {
      test(`${lib.path} fits without sideways scrolling`, async ({ page, api }) => {
        await page.setViewportSize(size);
        await page.goto(lib.path);
        const add = page.getByRole("button", { name: lib.add });
        await expect(add).toBeVisible();
        await api.quiet();
        expect(await sideways(page)).toBeLessThanOrEqual(0);
        // One line: no taller than a single-line button.
        expect((await add.boundingBox())?.height ?? 99).toBeLessThan(40);

        await page.getByRole("button", { name: "Select", exact: true }).click();
        await expect(page.getByRole("button", { name: /^Select all/ })).toBeVisible();
        expect(await sideways(page)).toBeLessThanOrEqual(0);
      });
    }
  });
}

test.describe("the saved table view", () => {
  test.use({ persona: "admin" });

  test("a phone shows the grid and hides the toggle; the desktop gets its table back", async ({ page }) => {
    await page.addInitScript(() => { try { localStorage.setItem("movies.view", "table"); } catch { /* ignore */ } });
    await page.setViewportSize({ width: 375, height: 812 });
    await page.goto("/movies");
    await expect(page.getByRole("link", { name: "Anchor Point" }).first()).toBeVisible();
    await expect(page.getByRole("table")).toHaveCount(0);
    await expect(page.getByRole("group", { name: "View" })).toHaveCount(0);

    await page.setViewportSize({ width: 1440, height: 900 });
    await expect(page.getByRole("table")).toBeVisible();
    await expect(page.getByRole("group", { name: "View" })).toBeVisible();
  });
});
