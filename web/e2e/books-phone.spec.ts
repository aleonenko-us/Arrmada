import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { mockLibrary } from "./fixtures/booksLibrary";

// BOOK-25 at phone width, with touch only (no hover): + Add book stays in sight, the
// secondary buttons fold into the Library tools menu, a card's Search now and Remove are
// reachable from its always-visible ⋯ button, and the book page fits.

const sideways = (page: Page) => page.locator("main").evaluate((el) => el.scrollWidth - el.clientWidth);

test.describe("Books on a phone", () => {
  test.use({ persona: "admin" });

  test("the header fits and folds + Add author and Search missing into the menu", async ({ page, api }) => {
    await mockLibrary(page);
    await page.goto("/books");
    const add = page.getByRole("button", { name: "+ Add book" });
    await expect(add).toBeVisible();
    await expect(page.getByRole("button", { name: "+ Add author" })).toHaveCount(0);
    await page.getByRole("button", { name: "Library tools" }).click();
    for (const name of ["Scan library", "Possible duplicates", "Find series", "Search missing (1)", "+ Add author"]) {
      await expect(page.getByRole("menuitem", { name, exact: true })).toBeVisible();
    }
    await page.getByRole("menuitem", { name: "+ Add author" }).click();
    const dialog = page.getByRole("dialog", { name: "Add an author" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "Search by author" })).toBeFocused();
    expect(await sideways(page)).toBeLessThanOrEqual(0);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await api.quiet();
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });

  test("a card's Search now and Remove are reachable by touch", async ({ page, api }) => {
    await mockLibrary(page);
    await page.goto("/books");
    await page.getByLabel("View").selectOption("book-grid");
    const more = page.getByRole("button", { name: "Actions for Children of Dune" });
    await expect(more).toBeVisible();
    await more.tap();
    await expect(page.getByRole("menuitem", { name: "Search now" })).toBeVisible();
    await page.getByRole("menuitem", { name: "Remove…" }).tap();
    await expect(page.getByRole("dialog", { name: "Remove “Children of Dune”?" })).toBeVisible();
    await api.quiet();
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });

  test("the book page fits, its menus included", async ({ page, api }) => {
    await mockLibrary(page);
    await page.goto("/books/13");
    await page.getByRole("button", { name: "Fix", exact: true }).tap();
    await expect(page.getByRole("menuitem", { name: "Refresh & rescan" })).toBeVisible();
    await api.quiet();
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });
});
