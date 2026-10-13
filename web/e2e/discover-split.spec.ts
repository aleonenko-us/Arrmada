import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { cards } from "./fixtures/discover";
import { requestableBook } from "./fixtures/books";
import type { DiscoverCard } from "../src/lib/api";

// FE-32: Discover split into features/discover, its card and rail shared with the Books
// tab. The Movies tab and the Books tab render their rows, a card opens its sheet and
// Escape closes it, and the cross-row de-duplication still keeps a title to one row.

const extra = (n: number, title: string): DiscoverCard => ({ ...cards[0], tmdb_id: 5000 + n, title, media_type: "movie" });
const fresh = [extra(1, "Brine"), extra(2, "Keel"), extra(3, "Mooring"), extra(4, "Spindrift"), extra(5, "Squall")];

// Trending claims the fixture's ten cards; Popular repeats three of them plus five of its
// own, so after de-duplication Popular shows only its own five.
async function overlappingRows(page: Page) {
  await page.route((u) => u.pathname === "/api/v1/discover/popular", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [...cards.slice(0, 3), ...fresh] }) }));
}

// A row: its heading sits in the header, and the header and the strip share the row's div.
const row = (page: Page, title: string) => page.locator(`xpath=//h2[normalize-space()="${title}"]/../..`);

test.describe("Discover after the split", () => {
  test.use({ persona: "requester" });

  test("Movies: rows render, a title shows in one row only, and the sheet opens and closes", async ({ page, api }) => {
    await overlappingRows(page);
    await page.goto("/discover?tab=movies");
    await api.quiet();
    const trending = row(page, "Trending movies");
    const popular = row(page, "Popular movies");
    await expect(trending.getByRole("button", { name: `View details for ${cards[0].title}` })).toBeVisible();
    await expect(popular.getByRole("button", { name: "View details for Brine" })).toBeVisible();
    // Already in Trending above it, so Popular drops it.
    await expect(popular.getByRole("button", { name: `View details for ${cards[0].title}` })).toHaveCount(0);

    await trending.getByRole("button", { name: `View details for ${cards[0].title}` }).click();
    const sheet = page.getByRole("dialog", { name: cards[0].title });
    await expect(sheet).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
  });

  test("Books: rows render with the shared card, and the sheet opens and closes", async ({ page, api }) => {
    await page.goto("/discover?tab=books");
    await api.quiet();
    const card = page.getByRole("button", { name: `View details for ${requestableBook.title}` }).first();
    await expect(card).toBeVisible();
    await card.click();
    const sheet = page.getByRole("dialog", { name: requestableBook.title });
    await expect(sheet).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(sheet).toHaveCount(0);
  });

  test("the omnibox's dropdown loads on focus and still picks a title", async ({ page, api }) => {
    await page.goto("/discover");
    await api.quiet();
    const box = page.getByRole("textbox", { name: "Search movies and TV" });
    await box.fill("harbour");
    await page.getByRole("button", { name: /See all results for “harbour”/ }).waitFor();
    await box.press("Enter");
    await expect(page).toHaveURL(/\/discover\?q=harbour$/);
  });
});
