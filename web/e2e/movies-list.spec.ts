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

  // MOV-19: the mass editor sets a profile on the whole selection in one request, and offers
  // to download smaller versions of the files the new profile no longer accepts.
  test("the mass editor sets a profile in one request and offers smaller versions", async ({ page, api }) => {
    await page.goto("/movies");
    await page.getByRole("button", { name: "Select", exact: true }).click();
    await page.getByRole("button", { name: "Select all (5)" }).click();
    await page.getByRole("combobox", { name: /quality profile/i }).selectOption({ label: "Ultra HD" });
    await expect.poll(() => api.callsTo("PUT", "/api/v1/movies/bulk").length).toBe(1);
    const sent = api.callsTo("PUT", "/api/v1/movies/bulk")[0].body as { ids: number[]; quality_profile: string; search: string };
    expect([...sent.ids].sort()).toEqual([1, 2, 3, 4, 5]);
    expect(sent).toMatchObject({ quality_profile: "uhd-2160p", search: "missing_and_upgrade" });
    await expect(page.getByText("Quality profile set on 5 movies.")).toBeVisible();
    const prompt = page.getByRole("dialog", { name: "1 file doesn't fit this profile" });
    await expect(prompt.getByText("The Cartographer")).toBeVisible();
    await prompt.getByRole("button", { name: "Download smaller versions" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/movies/bulk/regrab").map((c) => c.body)).toEqual([{ ids: [1] }]);
    await expect(page.getByText("0 selected")).toBeVisible(); // a clean run clears the selection
  });

  test("a partial failure says how many failed and keeps them selected", async ({ page }) => {
    await page.goto("/movies");
    await page.getByRole("button", { name: "Select", exact: true }).click();
    await page.getByRole("button", { name: /^Driftwood/ }).first().click();
    await page.getByRole("button", { name: /^Anchor Point/ }).first().click();
    await expect(page.getByText("2 selected")).toBeVisible();
    await page.getByRole("button", { name: "Search selected" }).click();
    await expect(page.getByText(/1 movie couldn't be searched — still selected/)).toBeVisible();
    await expect(page.getByText("1 selected")).toBeVisible();
  });

  test("the mass editor deletes a selection through one request", async ({ page, api }) => {
    await page.goto("/movies");
    await page.getByRole("button", { name: "Select", exact: true }).click();
    await page.getByRole("button", { name: /^Anchor Point/ }).first().click();
    await page.getByRole("button", { name: "Delete…" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("Remove 1 movie?")).toBeVisible();
    await dialog.getByRole("button", { name: "Remove", exact: true }).click();
    await expect.poll(() => api.callsTo("DELETE", "/api/v1/movies/bulk").map((c) => c.body)).toEqual([{ ids: [2], delete_files: false, cancel_downloads: true }]);
    await expect(page.getByText("Removed 1 movie.")).toBeVisible();
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
