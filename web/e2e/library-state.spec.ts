import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import * as media from "./fixtures/media";

// FE-24: a library's filter and search live in the address, a sidebar link opens a page
// at the top, and Back puts the list back where it was scrolled to.

const mainTop = (page: Page) => page.locator("main").evaluate((el) => el.scrollTop);

test.describe("library state", () => {
  test.use({ persona: "manager" });

  test("a pasted /movies?filter=…&q=… shows that view, and typing keeps it in the address", async ({ page }) => {
    await page.goto("/movies?filter=unmonitored&q=lantern");
    await expect(page.getByRole("button", { name: /^Unmonitored \d+$/ })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("searchbox", { name: "Search movies" })).toHaveValue("lantern");
    await expect(page.getByRole("link", { name: "Lanterns Over the Northern Sea" }).first()).toBeVisible();
    await expect(page.getByRole("link", { name: "Anchor Point" })).toHaveCount(0);

    await page.getByRole("searchbox", { name: "Search movies" }).fill("iron");
    await expect(page).toHaveURL(/[?&]q=iron/);
    await page.getByRole("button", { name: /^All \d+$/ }).click();
    await expect(page).toHaveURL(/\/movies\?q=iron$/);
    await expect(page.getByRole("link", { name: "Iron Tide" }).first()).toBeVisible();
  });

  test("Back restores the scroll position; a sidebar link opens at the top", async ({ page }) => {
    // A library tall enough to scroll: 200 films.
    const many = Array.from({ length: 200 }, (_, i) => ({ ...media.movies.movies[1], id: 1000 + i, title: `Film ${String(i).padStart(3, "0")}`, sort_title: `film ${String(i).padStart(3, "0")}` }));
    await page.route((u) => u.pathname === "/api/v1/movies", (route) => route.fulfill({ json: { ...media.movies, movies: many } }));
    await page.goto("/movies");
    await expect(page.getByRole("link", { name: "Film 000" }).first()).toBeVisible();
    await page.locator("main").evaluate((el) => { el.scrollTop = 2400; });
    await expect.poll(() => mainTop(page)).toBeGreaterThan(2300);
    const before = await mainTop(page);

    await page.getByRole("link", { name: "Series", exact: true }).click();
    await expect(page.getByRole("heading", { level: 1, name: "Series" })).toBeVisible();
    expect(await mainTop(page)).toBe(0);

    await page.goBack();
    await expect(page.getByRole("heading", { level: 1, name: "Movies" })).toBeVisible();
    // Within about one card row.
    await expect.poll(async () => Math.abs((await mainTop(page)) - before)).toBeLessThan(300);
  });
});
