import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";

// Issue #17: the command palette (Ctrl K / ⌘K). It opens from anywhere, finds library
// titles, pages and settings cards, moves with the arrows and opens with Enter; a
// requester's palette never asks for (or shows) library titles.

// pressShortcut presses Ctrl K until the palette is up: the shortcut only works once the
// shell's palette host has mounted, and a press before that is dropped on purpose.
async function pressShortcut(page: Page) {
  const dialog = page.getByRole("dialog", { name: "Search" });
  await expect(async () => {
    if (!(await dialog.isVisible())) await page.keyboard.press("Control+K");
    await expect(dialog).toBeVisible({ timeout: 500 });
  }).toPass();
}

test.describe("command palette (staff)", () => {
  test("Ctrl+K opens it, typing finds a movie, Enter opens the movie", async ({ page, api }) => {
    await page.goto("/movies");
    await expect(page.getByRole("heading", { name: "Movies" }).first()).toBeVisible();
    await pressShortcut(page);
    const dialog = page.getByRole("dialog", { name: "Search" });
    await expect(dialog).toBeVisible();
    const input = dialog.getByRole("combobox");
    await expect(input).toBeFocused();
    await input.fill("cartog");
    const library = dialog.getByRole("group", { name: "Library" });
    await expect(library.getByRole("option", { name: /The Cartographer/ })).toBeVisible();
    expect(api.callsTo("GET", "/api/v1/library/search").some((c) => c.path.includes("q=cartog"))).toBe(true);
    // The best match is highlighted first; Enter opens it and the palette closes.
    await expect(dialog.getByRole("option", { selected: true })).toContainText("The Cartographer");
    await page.keyboard.press("Enter");
    await expect(dialog).toBeHidden();
    await expect(page).toHaveURL(/\/movies\/1$/);
    // Back returns to where the palette was opened, not to an empty palette entry.
    await page.goBack();
    await expect(page).toHaveURL(/\/movies$/);
    await expect(page.getByRole("dialog", { name: "Search" })).toBeHidden();
  });

  test("the arrows move the highlight and Escape closes it", async ({ page }) => {
    await page.goto("/activity/queue");
    await pressShortcut(page);
    const dialog = page.getByRole("dialog", { name: "Search" });
    const input = dialog.getByRole("combobox");
    await input.fill("naming");
    const first = await dialog.getByRole("option", { selected: true }).textContent();
    await page.keyboard.press("ArrowDown");
    await expect(dialog.getByRole("option", { selected: true })).not.toHaveText(first ?? "");
    await page.keyboard.press("ArrowUp");
    await expect(dialog.getByRole("option", { selected: true })).toHaveText(first ?? "");
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(page).toHaveURL(/\/activity\/queue$/);
  });

  test("jumps straight to a settings card", async ({ page }) => {
    await page.goto("/movies");
    // The sidebar's search field look-alike.
    await page.locator("aside").getByRole("button", { name: /^Search/ }).click();
    const dialog = page.getByRole("dialog", { name: "Search" });
    await dialog.getByRole("combobox").fill("two-factor");
    await expect(dialog.getByRole("option", { selected: true })).toContainText("Two-factor sign-in");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/settings\/users#two-factor$/);
    await expect(page.locator("#two-factor")).toBeInViewport();

    await pressShortcut(page);
    await dialog.getByRole("combobox").fill("homepage");
    await expect(dialog.getByRole("option", { selected: true })).toContainText("Dashboard widget");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/settings\/system#dashboard-widget$/);
  });

  test("Add movie opens the Movies page's Add dialog", async ({ page }) => {
    await page.goto("/activity/queue");
    await pressShortcut(page);
    await page.getByRole("dialog", { name: "Search" }).getByRole("combobox").fill("add movie");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/movies$/);
    await expect(page.getByRole("dialog")).toBeVisible();
  });

  test("lists recent picks before anything is typed", async ({ page }) => {
    await page.goto("/movies");
    await pressShortcut(page);
    await page.getByRole("dialog", { name: "Search" }).getByRole("combobox").fill("calendar");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/calendar$/);
    await pressShortcut(page);
    const recent = page.getByRole("dialog", { name: "Search" }).getByRole("group", { name: "Recent" });
    await expect(recent.getByRole("option", { name: /Calendar/ })).toBeVisible();
  });

  test("the Settings search offers the palette", async ({ page }) => {
    await page.goto("/settings/library");
    await page.getByRole("searchbox", { name: "Search settings" }).fill("shortcut");
    await page.getByRole("button", { name: /Search everything/ }).click();
    // It carries on with what was typed.
    await expect(page.getByRole("dialog", { name: "Search" }).getByRole("combobox")).toHaveValue("shortcut");
  });
});

test.describe("command palette (requester)", () => {
  test.use({ persona: "requester" });

  test("shows their pages and Discover, never a library group", async ({ page, api }) => {
    await page.goto("/discover");
    await pressShortcut(page);
    const dialog = page.getByRole("dialog", { name: "Search" });
    const input = dialog.getByRole("combobox");
    await input.fill("cartographer");
    const discover = dialog.getByRole("group", { name: "Discover" });
    await expect(discover.getByRole("option").first()).toContainText("Search Discover for");
    await api.quiet();
    await expect(dialog.getByRole("group", { name: "Library" })).toHaveCount(0);
    expect(api.callsTo("GET", "/api/v1/library/search")).toEqual([]);
    // Their own pages only: no Settings, no console pages.
    await input.fill("settings");
    await expect(dialog.getByRole("group", { name: "Pages and settings" })).toHaveCount(0);
    await input.fill("requests");
    await expect(dialog.getByRole("option", { selected: true })).toContainText("Requests");
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/requests$/);
  });

  test("the Discover row opens Discover's search", async ({ page }) => {
    await page.goto("/requests");
    await page.getByRole("button", { name: "Search everything" }).click();
    const dialog = page.getByRole("dialog", { name: "Search" });
    await dialog.getByRole("combobox").fill("harbour");
    await dialog.getByRole("option", { name: /Search Discover for/ }).click();
    await expect(page).toHaveURL(/\/discover\?q=harbour$/);
  });
});
