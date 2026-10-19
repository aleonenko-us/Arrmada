import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { libraryFoldersMulti } from "./fixtures/system";

// GitHub #34/#36 on a phone (375px): the grid (a phone never gets the table) orders by the
// Sort menu and reverses with its ↑/↓ button, and with several folders the Folder picker
// narrows the grid — all without the page scrolling sideways.

const sideways = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
const cardTitles = (page: Page) => page.locator("main a[href^='/movies/'] .truncate.font-semibold");

test.describe("library order on a phone", () => {
  test.use({ persona: "manager" });

  test("sort, reverse and the folder picker fit and work", async ({ page }) => {
    await page.route((u) => u.pathname === "/api/v1/library/folders", (route) =>
      route.fulfill({ contentType: "application/json", body: JSON.stringify(libraryFoldersMulti) }));
    await page.goto("/movies");
    await expect(page.getByRole("button", { name: "table", exact: true })).toHaveCount(0);

    await page.getByRole("combobox", { name: "Sort" }).selectOption("year");
    await expect(cardTitles(page).first()).toHaveText("Driftwood");
    await page.getByRole("button", { name: /^Descending/ }).click();
    await expect(cardTitles(page).first()).toHaveText("Lanterns Over the Northern Sea");
    await expect(page.getByRole("button", { name: /^Ascending/ })).toBeVisible();

    await page.getByRole("combobox", { name: "Folder", exact: true }).selectOption({ label: "Kids" });
    await expect(page).toHaveURL(/folder=3/);
    await expect(cardTitles(page)).toHaveText(["Lanterns Over the Northern Sea"]);
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });
});
