import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { mockLibrary } from "./fixtures/booksLibrary";

// BOOK-25: the admin Books screens. The header keeps + Add book, + Add author and Search
// missing in sight and the occasional jobs in the Library tools menu (keyboard-driven,
// Escape returns focus); one View control; a Sort that lives in the address; status words
// spelled out; and the book page's controls grouped into Search / Add files / Fix menus.

const sideways = (page: Page) => page.locator("main").evaluate((el) => el.scrollWidth - el.clientWidth);
const cardTitles = (page: Page) => page.locator("main .grid > div .truncate.font-semibold").allTextContents();

test.describe("Books library (desktop)", () => {
  test.use({ persona: "admin" });

  test("the header holds the everyday actions; the tools menu holds the rest", async ({ page, api }) => {
    await mockLibrary(page, { dupes: 2 });
    await page.goto("/books");
    await expect(page.getByRole("button", { name: "+ Add book" })).toBeVisible();
    await expect(page.getByRole("button", { name: "+ Add author" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Search missing (1)" })).toBeVisible();
    // The duplicates chip moved into the menu.
    await expect(page.getByRole("button", { name: /^Possible duplicates/ })).toHaveCount(0);

    // Keyboard: ArrowDown opens it on the first item, arrows move, Escape closes and
    // gives focus back to the trigger.
    const tools = page.getByRole("button", { name: "Library tools" });
    await tools.focus();
    await page.keyboard.press("ArrowDown");
    const menu = page.getByRole("menu", { name: "Library tools" });
    await expect(menu.getByRole("menuitem")).toHaveText(["Scan library", "Possible duplicates (2)", "Find series"]);
    await expect(menu.getByRole("menuitem", { name: "Scan library" })).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(menu.getByRole("menuitem", { name: "Possible duplicates (2)" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
    await expect(tools).toBeFocused();
    expect(await sideways(page)).toBeLessThanOrEqual(0);
    await api.quiet();
  });

  test("with Hardcover, Re-match is in the menu and Find series isn't", async ({ page }) => {
    await mockLibrary(page, { source: "hardcover", upgradable: 3 });
    await page.goto("/books");
    await page.getByRole("button", { name: "Library tools" }).click();
    await expect(page.getByRole("menuitem", { name: "Re-match 3 to Hardcover" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Find series" })).toHaveCount(0);
  });

  test("the sort re-orders the books and survives a reload", async ({ page }) => {
    await mockLibrary(page);
    await page.goto("/books");
    await page.getByLabel("View").selectOption("book-grid");
    // Author, then title: the order the library always had.
    await expect.poll(() => cardTitles(page)).toEqual(["Children of Dune", "Dune", "Anathem"]);
    await expect(page.getByText("Ebook · Audiobook ✓")).toBeVisible();

    await page.getByLabel("Sort").selectOption("added");
    await expect.poll(() => cardTitles(page)).toEqual(["Anathem", "Children of Dune", "Dune"]);
    await expect(page).toHaveURL(/sort=added/);
    await page.reload();
    await expect(page.getByLabel("Sort")).toHaveValue("added");
    await expect.poll(() => cardTitles(page)).toEqual(["Anathem", "Children of Dune", "Dune"]);

    // The author tiles have their own orders; a book-only order falls back to the name.
    await page.getByLabel("Sort").selectOption("year");
    await page.getByLabel("View").selectOption("author-grid");
    await expect(page.getByLabel("Sort")).toHaveValue("author");
  });

  test("the book page groups its controls", async ({ page }) => {
    const state = await mockLibrary(page);
    await page.goto("/books/13");
    await expect(page.getByRole("switch", { name: "Monitored" })).toBeVisible();
    for (const name of ["Search", "Add files", "Fix"]) await expect(page.getByRole("button", { name, exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Delete", exact: true })).toBeVisible();
    for (const old of ["Search indexers", "Upload torrent", "Manual import", "Rename", "Edit metadata", "Change match", "Refresh & rescan"]) {
      await expect(page.getByRole("button", { name: old, exact: true })).toHaveCount(0);
    }

    await page.getByRole("button", { name: "Fix", exact: true }).click();
    await expect(page.getByRole("menu", { name: "Fix" }).getByRole("menuitem")).toHaveText(["Edit metadata…", "Change match…", "Rename files", "Refresh & rescan"]);
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: "Search", exact: true }).click();
    await page.getByRole("menuitem", { name: "Automatic search" }).click();
    await expect.poll(() => state.searches).toBe(1);
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });
});
