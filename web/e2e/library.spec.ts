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
