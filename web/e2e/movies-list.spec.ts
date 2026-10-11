import { test, expect } from "./mockApi";

// MOV-10: the library list is slim summaries; an upgrade downloading on a film that has
// its file shows a labelled indicator (and the film stays Downloaded); while downloads run
// the grid polls only the small downloads endpoint, never the whole list.
test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("the grid labels upgrade progress and polls only the downloads endpoint", async ({ page, api }) => {
    await page.goto("/movies");
    await expect(page.getByText("Upgrade", { exact: true })).toBeVisible();
    await expect(page.getByText("Downloaded", { exact: true }).first()).toBeVisible(); // The Cartographer keeps its badge
    await page.waitForRequest((r) => new URL(r.url()).pathname === "/api/v1/movies/downloads", { timeout: 8000 });
    expect(api.calls.filter((c) => c.method === "GET" && c.path === "/api/v1/movies")).toHaveLength(1);
  });

  // MOV-18: the movie page's overflow menu works from the keyboard — Enter opens it on the
  // first item, arrows move, Escape closes it and puts focus back on ⋯.
  test("the movie page's ⋯ menu works by keyboard", async ({ page }) => {
    await page.goto("/movies/2");
    const trigger = page.getByRole("button", { name: "More actions" });
    await trigger.focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("menuitem", { name: "Interactive search" })).toBeFocused();
    await page.keyboard.press("End");
    await expect(page.getByRole("menuitem", { name: "Delete movie…" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("menu")).toHaveCount(0);
    await expect(trigger).toBeFocused();
    // Enter on an item runs it: Delete opens the confirm dialog.
    await page.keyboard.press("Enter");
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("dialog").getByText("Remove “Anchor Point”?")).toBeVisible();
  });

  // The grid's ⋯ appears for a mouse on hover or keyboard focus — the card has no
  // hover-only controls left.
  test("a grid card's ⋯ menu is reachable by keyboard", async ({ page }) => {
    await page.goto("/movies");
    const trigger = page.getByRole("button", { name: "Actions for Anchor Point" });
    await trigger.focus();
    await expect(trigger).toBeVisible();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("menuitem", { name: "Search now" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(trigger).toBeFocused();
  });
});
