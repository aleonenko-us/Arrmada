import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";

// Issue #17 on a phone: the search button in the top bar opens the palette as a
// full-screen sheet with large rows, nothing scrolls sideways at 375px, and Back closes it
// without leaving the page.

async function openFromButton(page: Page) {
  await page.getByRole("button", { name: "Search everything" }).click();
  const dialog = page.getByRole("dialog", { name: "Search" });
  await expect(dialog).toBeVisible();
  return dialog;
}

async function expectFullScreen(page: Page) {
  const box = await page.getByRole("dialog", { name: "Search" }).boundingBox();
  const vp = page.viewportSize();
  expect(box && vp).toBeTruthy();
  expect(Math.round(box!.width)).toBe(vp!.width);
  expect(Math.round(box!.height)).toBe(vp!.height);
  // Nothing wider than the screen.
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(vp!.width);
}

test.describe("palette at 375px (requester)", () => {
  test.use({ persona: "requester" });

  test("is a full-screen sheet with big rows; Back closes it and stays put", async ({ page, api }) => {
    await page.goto("/discover");
    await api.quiet();
    const dialog = await openFromButton(page);
    await expectFullScreen(page);
    await dialog.getByRole("combobox").fill("the lighthouse keepers and a very long title that should not widen anything");
    await expectFullScreen(page);
    await dialog.getByRole("combobox").fill("me");
    for (const row of await dialog.getByRole("option").all()) {
      const h = (await row.boundingBox())?.height ?? 0;
      expect(h).toBeGreaterThanOrEqual(48);
    }
    // A plain Cancel for a phone without an Escape key.
    await expect(dialog.getByRole("button", { name: "Cancel" })).toBeVisible();
    await page.goBack();
    await expect(dialog).toBeHidden();
    await expect(page).toHaveURL(/\/discover$/);
  });

  test("a tap opens a page", async ({ page }) => {
    await page.goto("/discover");
    const dialog = await openFromButton(page);
    await dialog.getByRole("combobox").fill("shelf");
    await dialog.getByRole("option", { name: /My shelf/ }).click();
    await expect(page).toHaveURL(/\/shelf$/);
    await expect(dialog).toBeHidden();
  });
});

test.describe("palette at 375px (staff)", () => {
  test("opens from the top bar and finds a show", async ({ page }) => {
    await page.goto("/activity/queue");
    const dialog = await openFromButton(page);
    await expectFullScreen(page);
    await dialog.getByRole("combobox").fill("harbour");
    await dialog.getByRole("group", { name: "Library" }).getByRole("option", { name: /Harbour Lights/ }).click();
    await expect(page).toHaveURL(/\/series\/1$/);
  });
});
