import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import { libraryFoldersMulti } from "./fixtures/system";

// GitHub #36: the Movies and Series tables sort by Year and Added from their headers (again
// to reverse), and the grid's Sort menu is the same sort. GitHub #34 (the lighter version):
// with several folders the library pages have a Folder picker kept in the address, and Add
// while one folder is shown adds into it; with one folder there's no picker at all.

// override answers the given endpoints before the default fixtures, recording the calls.
async function override(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method() || url.pathname !== r.path) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(r.body ?? {}) });
    }
    return route.fallback();
  });
}

const multi: MockRoute = { method: "GET", path: "/api/v1/library/folders", body: libraryFoldersMulti };
const rowTitles = (page: Page) => page.locator("main tbody tr td:first-child a");
const cardTitles = (page: Page, base: string) => page.locator(`main a[href^='/${base}/'] .truncate.font-semibold`);

test.describe("library order", () => {
  test.use({ persona: "manager" });

  test("movies: Year and Added headers sort, reverse, and are the Sort menu's sort", async ({ page }) => {
    await page.goto("/movies");
    await page.getByRole("button", { name: "table", exact: true }).click();
    const sort = page.getByRole("combobox", { name: "Sort" });

    // Year: newest first, then oldest first; the menu follows.
    await page.getByRole("columnheader", { name: /Year/ }).getByRole("button").click();
    await expect(rowTitles(page)).toHaveText(["Driftwood", "The Cartographer", "Anchor Point", "Iron Tide", "Lanterns Over the Northern Sea"]);
    await expect(page.getByRole("columnheader", { name: /Year/ })).toHaveAttribute("aria-sort", "descending");
    await expect(sort).toHaveValue("year");
    await page.getByRole("columnheader", { name: /Year/ }).getByRole("button").click();
    await expect(rowTitles(page)).toHaveText(["Lanterns Over the Northern Sea", "Iron Tide", "Anchor Point", "The Cartographer", "Driftwood"]);
    await expect(page.getByRole("columnheader", { name: /Year/ })).toHaveAttribute("aria-sort", "ascending");
    await expect(page.getByRole("button", { name: /^Ascending/ })).toBeVisible();

    // Added: most recent first; a film with no date goes last.
    await page.getByRole("columnheader", { name: /Added/ }).getByRole("button").click();
    await expect(rowTitles(page)).toHaveText(["Anchor Point", "The Cartographer", "Iron Tide", "Lanterns Over the Northern Sea", "Driftwood"]);
    await expect(sort).toHaveValue("added");

    // The grid keeps the same order, and a reload keeps it.
    await page.getByRole("button", { name: "grid", exact: true }).click();
    await expect(cardTitles(page, "movies")).toHaveText(["Anchor Point", "The Cartographer", "Iron Tide", "Lanterns Over the Northern Sea", "Driftwood"]);
    await page.reload();
    await expect(sort).toHaveValue("added");

    // Picking Year in the menu is the Year header's first click: newest first.
    await sort.selectOption("year");
    await expect(cardTitles(page, "movies").first()).toHaveText("Driftwood");
    await page.getByRole("button", { name: "table", exact: true }).click();
    await expect(page.getByRole("columnheader", { name: /Year/ })).toHaveAttribute("aria-sort", "descending");

    // A column the menu doesn't list joins it while it's the order.
    await page.getByRole("columnheader", { name: /Codec/ }).getByRole("button").click();
    await expect(sort).toHaveValue("codec");
  });

  test("series: the Year header sorts newest first, then reverses", async ({ page }) => {
    await page.goto("/series");
    await page.getByRole("button", { name: "table", exact: true }).click();
    await page.getByRole("columnheader", { name: /Year/ }).getByRole("button").click();
    await expect(rowTitles(page)).toHaveText(["Harbour Lights", "Saltwind", "The Lighthouse Keepers"]);
    await page.getByRole("columnheader", { name: /Year/ }).getByRole("button").click();
    await expect(rowTitles(page)).toHaveText(["The Lighthouse Keepers", "Saltwind", "Harbour Lights"]);
    await page.getByRole("columnheader", { name: /Added/ }).getByRole("button").click();
    await expect(rowTitles(page)).toHaveText(["Saltwind", "Harbour Lights", "The Lighthouse Keepers"]);
    await expect(page.getByRole("combobox", { name: "Sort" })).toHaveValue("added");
  });

  test("with one folder there's no Folder picker, and ?folder= changes nothing", async ({ page }) => {
    await page.goto("/movies?folder=3");
    await expect(cardTitles(page, "movies")).toHaveCount(5);
    await expect(page.getByRole("combobox", { name: "Folder", exact: true })).toHaveCount(0);
    await page.goto("/series?folder=4");
    await expect(cardTitles(page, "series")).toHaveCount(3);
    await expect(page.getByRole("combobox", { name: "Folder", exact: true })).toHaveCount(0);
  });

  test("movies: with two folders the picker filters, sits in the address, and Add goes into it", async ({ page, api }) => {
    await override(page, api, [
      multi,
      { method: "GET", path: "/api/v1/movies/lookup", body: { results: [
        { tmdb_id: 862, title: "Toy Story", year: 1995, overview: "Toys.", poster_url: "", vote_average: 8, in_library: false },
      ] } },
      { method: "POST", path: "/api/v1/movies", status: 201, body: { id: 98, tmdb_id: 862, title: "Toy Story", year: 1995, monitored: true, has_file: false, quality_profile: "hd", min_availability: "released", library_folder_id: 3 } },
    ]);
    await page.goto("/movies");
    const folder = page.getByRole("combobox", { name: "Folder", exact: true });
    await expect(folder).toHaveValue("");
    await expect(cardTitles(page, "movies")).toHaveCount(5);

    await folder.selectOption({ label: "Kids" });
    await expect(page).toHaveURL(/folder=3/);
    await expect(cardTitles(page, "movies")).toHaveText(["Lanterns Over the Northern Sea"]);
    await expect(page.getByRole("button", { name: /^All 1$/ })).toBeVisible(); // the pills count inside the folder
    await expect(page.getByText("Library / Movies / Kids")).toBeVisible();

    // The default folder holds the rest.
    await folder.selectOption("0");
    await expect(cardTitles(page, "movies")).toHaveCount(4);
    await expect(cardTitles(page, "movies").filter({ hasText: "Lanterns" })).toHaveCount(0);

    // A link straight to the Kids view, then Add: the folder is preselected and sent.
    await page.goto("/movies?folder=3");
    await expect(cardTitles(page, "movies")).toHaveText(["Lanterns Over the Northern Sea"]);
    await page.getByRole("button", { name: "+ Add movie" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Search TMDB for a movie").fill("toy story");
    await dialog.getByRole("button", { name: "Search" }).click();
    await dialog.getByRole("button", { name: /Toy Story/ }).click();
    await expect(dialog.getByLabel("Folder")).toHaveValue("3");
    await dialog.getByRole("button", { name: "Add movie" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/movies").length).toBe(1);
    expect((api.callsTo("POST", "/api/v1/movies")[0].body as { folder_id?: number }).folder_id).toBe(3);
    // Shown with a folder already chosen, the rules weren't asked.
    expect(api.calls.filter((c) => c.path.startsWith("/api/v1/library/folders/suggest"))).toHaveLength(0);

    await expect(dialog).toHaveCount(0);
    // All folders brings everything back and clears the address.
    await folder.selectOption("");
    await expect(cardTitles(page, "movies")).toHaveCount(5);
    await expect(page).not.toHaveURL(/folder=/);
  });

  test("movies: switching folders clears the selection, and bulk actions reach only the folder shown", async ({ page, api }) => {
    await override(page, api, [multi]);
    await page.goto("/movies");
    await expect(page.getByText("5 in library")).toBeVisible();
    await page.getByRole("button", { name: "Select", exact: true }).click();
    await page.getByRole("button", { name: "Select all (5)" }).click();
    await expect(page.getByText("5 selected")).toBeVisible();

    await page.getByRole("combobox", { name: "Folder", exact: true }).selectOption({ label: "Kids" });
    await expect(page.getByText("0 selected")).toBeVisible();
    await expect(page.getByText("1 in library")).toBeVisible(); // the count follows the folder

    // Select all now means the one Kids film, and a bulk delete sends only it.
    await page.getByRole("button", { name: "Select all (1)" }).click();
    await expect(page.getByText("1 selected")).toBeVisible();
    await page.getByRole("button", { name: "Delete…" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("Remove 1 movie?")).toBeVisible();
    await dialog.getByRole("button", { name: "Remove", exact: true }).click();
    await expect.poll(() => api.callsTo("DELETE", "/api/v1/movies/bulk").map((c) => (c.body as { ids: number[] }).ids)).toEqual([[4]]);
  });

  test("series: the picker filters and Add sends the folder shown", async ({ page, api }) => {
    await override(page, api, [
      multi,
      { method: "GET", path: "/api/v1/series/lookup", body: { results: [
        { tmdb_id: 31910, title: "Naruto Shippuden", year: 2007, overview: "Ninjas.", poster_url: "" },
      ] } },
      { method: "POST", path: "/api/v1/series", status: 201, body: { id: 77, tmdb_id: 31910, title: "Naruto Shippuden", year: 2007, monitored: true, library_folder_id: 4 } },
    ]);
    await page.goto("/series?folder=4");
    await expect(cardTitles(page, "series")).toHaveText(["The Lighthouse Keepers"]);
    await page.getByRole("button", { name: "+ Add series" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByLabel("Folder")).toHaveValue("4");
    await dialog.getByLabel("Search TMDB for a series").fill("naruto");
    await dialog.getByRole("button", { name: "Search", exact: true }).click();
    await dialog.getByRole("button", { name: /Naruto Shippuden/ }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/series").length).toBe(1);
    expect((api.callsTo("POST", "/api/v1/series")[0].body as { folder_id?: number }).folder_id).toBe(4);
  });
});
