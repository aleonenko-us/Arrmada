import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { person } from "./fixtures/discover";

// #55: an actor in a library page's Cast row opens their person page on Discover (what
// else they've made), and Close comes back to the library page. An actor from a record
// stored before person ids were kept stays a plain tile. Desktop and phone.

const castLink = (page: Page) => page.getByRole("link", { name: `See ${person.name}’s movies and shows` });

async function fits(page: Page) {
  const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
  expect(w.doc, "document width").toBeLessThanOrEqual(w.viewport);
}

async function opensPersonAndComesBack(page: Page, from: RegExp) {
  const link = castLink(page);
  await link.scrollIntoViewIfNeeded();
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute("href", `/discover/person/${person.id}`);
  await link.click();
  await expect(page).toHaveURL(new RegExp(`/discover/person/${person.id}$`));
  const sheet = page.getByRole("dialog", { name: person.name });
  await expect(sheet.getByRole("heading", { name: person.name })).toBeVisible();
  await expect(sheet.getByRole("heading", { name: /Filmography/ })).toBeVisible();
  await fits(page);
  // Close steps back to the library page it was opened from, not to Discover.
  await sheet.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page).toHaveURL(from);
  await expect(castLink(page)).toBeVisible();
}

test.describe("staff", () => {
  test("a movie's cast member opens their person page", async ({ page, api }) => {
    await page.goto("/movies/1");
    await api.quiet();
    await expect(page.getByRole("heading", { name: "Cast" })).toBeVisible();
    // Theo's record predates person ids: shown, but not a link.
    await expect(page.getByText("Theo Keel", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: /Theo Keel/ })).toHaveCount(0);
    await opensPersonAndComesBack(page, /\/movies\/1$/);
  });

  test("a show's cast member opens their person page", async ({ page, api }) => {
    await page.goto("/series/1");
    await api.quiet();
    await expect(page.getByRole("heading", { name: "Cast" })).toBeVisible();
    await expect(page.getByText("Ada Fenwick", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: /Ada Fenwick/ })).toHaveCount(0);
    await opensPersonAndComesBack(page, /\/series\/1$/);
  });

  test("the keyboard opens a cast member, and Back returns", async ({ page, api, isMobile }) => {
    test.skip(isMobile, "keyboard on a desktop");
    await page.goto("/movies/1");
    await api.quiet();
    const link = castLink(page);
    await link.focus();
    await expect(link).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("dialog", { name: person.name })).toBeVisible();
    await page.goBack();
    await expect(page).toHaveURL(/\/movies\/1$/);
  });
});

test.describe("requester", () => {
  test.use({ persona: "requester" });

  // Library pages are staff-only: a requester never sees a Cast row to follow.
  test("has no library detail page to start from", async ({ page, api }) => {
    await page.goto("/movies/1");
    await api.quiet();
    await expect(page).toHaveURL(/\/discover$/);
    await expect(castLink(page)).toHaveCount(0);
  });
});
