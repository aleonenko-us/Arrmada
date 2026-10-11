import { test, expect } from "./mockApi";

// FE-31: Movies and Series are one LibraryGrid with two configs. Both keep the grid/table
// toggle, the filter pills, the delete confirm and the bulk profile change.
test.describe("library pages", () => {
  test.use({ persona: "manager" });

  for (const lib of [
    { path: "/movies", one: "Anchor Point", filter: "Unmonitored", onlyAfterFilter: "Lanterns Over the Northern Sea", hiddenAfterFilter: "Anchor Point", api: "/api/v1/movies", add: "+ Add movie" },
    { path: "/series", one: "Harbour Lights", filter: "Ended", onlyAfterFilter: "The Lighthouse Keepers", hiddenAfterFilter: "Harbour Lights", api: "/api/v1/series", add: "+ Add series" },
  ]) {
    test(`${lib.path}: grid and table, a filter, remove and bulk profile`, async ({ page, api }) => {
      await page.goto(lib.path);
      await expect(page.getByRole("button", { name: lib.add })).toBeVisible();
      await expect(page.getByRole("link", { name: lib.one }).first()).toBeVisible();

      // The table, then back to the grid.
      await page.getByRole("button", { name: "table", exact: true }).click();
      await expect(page.getByRole("columnheader", { name: /Status/ })).toBeVisible();
      await page.getByRole("button", { name: "grid", exact: true }).click();
      await expect(page.getByRole("columnheader")).toHaveCount(0);

      // A filter narrows the grid.
      await page.getByRole("button", { name: new RegExp(`^${lib.filter} \\d+$`) }).click();
      await expect(page.getByRole("link", { name: lib.onlyAfterFilter }).first()).toBeVisible();
      await expect(page.getByRole("link", { name: lib.hiddenAfterFilter })).toHaveCount(0);
      await page.getByRole("button", { name: /^All \d+$/ }).click();

      // Remove asks first and calls DELETE only on confirm.
      await page.getByRole("button", { name: `Actions for ${lib.one}` }).click();
      await page.getByRole("menuitem", { name: "Remove…" }).click();
      const dialog = page.getByRole("dialog");
      await expect(dialog).toContainText(lib.one);
      await dialog.getByRole("button", { name: "Remove", exact: true }).click();
      await expect.poll(() => api.callsTo("DELETE", `${lib.api}/`).length).toBe(1);

      // Bulk: select two, set a profile — one PUT each.
      await page.getByRole("button", { name: "Select", exact: true }).click();
      await page.getByRole("button", { name: /^Select all \(\d+\)$/ }).click();
      await page.getByRole("combobox", { name: /quality profile/i }).selectOption("uhd-2160p");
      await expect.poll(() => api.calls.filter((c) => c.method === "PUT" && c.path.endsWith("/profile")).length).toBeGreaterThan(1);
      await expect(page.getByText(/Quality profile set on \d+/)).toBeVisible();
    });
  }

  // MOV-22: quality badges and the monitored dot, the filter words and counts, a remembered
  // sort, and a search that takes the year.
  test("movies: badges, filters with counts, a remembered sort, title + year search", async ({ page }) => {
    await page.goto("/movies");
    const lanterns = page.locator("main .group").filter({ hasText: "Lanterns Over the Northern Sea" });
    await expect(lanterns.getByText("4K", { exact: true })).toBeVisible();
    await expect(lanterns.getByText("DV", { exact: true })).toBeVisible();
    await expect(lanterns.getByText("Atmos", { exact: true })).toBeVisible();
    await expect(lanterns.getByRole("img", { name: "Unmonitored" })).toBeVisible();
    await expect(page.locator("main .group").filter({ hasText: "Anchor Point" }).getByRole("img", { name: "Monitored" })).toBeVisible();

    // The Wanted count is the Wanted view's (Anchor Point); every pill counts.
    for (const name of [/^All 5$/, /^Wanted 1$/, /^Downloading 2$/, /^No file \(unmonitored\) 0$/, /^File missing 1$/, /^Doesn't fit 1$/, /^Unmonitored 1$/]) {
      await expect(page.getByRole("button", { name })).toBeVisible();
    }

    const titles = page.locator("main a[href^='/movies/'] .truncate.font-semibold");
    await expect(titles.first()).toHaveText("Anchor Point"); // Title: "The Cartographer" files under C
    await page.getByRole("combobox", { name: "Sort" }).selectOption("size");
    await expect(titles.first()).toHaveText("Lanterns Over the Northern Sea");
    await page.reload();
    await expect(page.getByRole("combobox", { name: "Sort" })).toHaveValue("size");
    await expect(titles.first()).toHaveText("Lanterns Over the Northern Sea");

    await page.getByRole("searchbox", { name: "Search movies" }).fill("tide 2020");
    await expect(titles).toHaveText(["Iron Tide"]);
  });

  // FE-11: the release search is a kit Modal — named, Escape closes it, and a long list
  // scrolls inside it in a short window.
  test("the table's release search opens as a dialog, scrolls inside, and closes on Escape", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 700 });
    await page.goto("/movies");
    await page.getByRole("button", { name: "table", exact: true }).click();
    await page.getByRole("row", { name: /Anchor Point/ }).getByRole("button", { name: "Search" }).click();
    const dialog = page.getByRole("dialog", { name: "Search indexers — Anchor Point" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Grab" })).toHaveCount(12);
    const box = await dialog.boundingBox();
    expect(box?.height ?? 0).toBeLessThanOrEqual(700);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
  });
});
