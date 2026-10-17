import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import * as media from "./fixtures/media";
import { settings } from "./fixtures/system";
import type { MockRoute } from "./fixtures/routes";

// GitHub #27: a film can be shown under another name than TMDB's English title — its
// original title, a translation, or one picked on its page — while `title` (matching,
// file names, the API) stays English.

const GREEK = "Τα φανάρια της βόρειας θάλασσας";

// The library list with Lanterns Over the Northern Sea (id 4) shown under its Greek title,
// sorted by it (the server's sort title follows the shown one).
const moviesWithGreek = {
  ...media.movies,
  movies: media.movies.movies.map((m) => (m.id === 4 ? { ...m, display_title: GREEK, sort_title: GREEK.toLowerCase() } : { ...m, display_title: m.title })),
};

async function installExtra(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method()) continue;
      const m = typeof r.path === "string" ? (url.pathname === r.path ? [] : null) : url.pathname.match(r.path);
      if (!m) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      const out = r.respond ? r.respond({ url, params: m.slice(1), body }) : r.body;
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out ?? {}) });
    }
    return route.fallback();
  });
}

test.describe("display titles", () => {
  test.use({ persona: "manager" });

  test("the library shows and sorts by the shown title, and finds a film by either name", async ({ page, api }) => {
    await installExtra(page, api, [{ method: "GET", path: "/api/v1/movies", body: moviesWithGreek }]);
    await page.goto("/movies");
    const titles = page.locator("main a[href^='/movies/'] .truncate.font-semibold");
    await expect(page.getByRole("link", { name: GREEK }).first()).toBeVisible();
    await expect(page.getByRole("link", { name: "Lanterns Over the Northern Sea" })).toHaveCount(0);
    // Greek sorts after the Latin titles.
    await expect(titles.last()).toHaveText(GREEK);

    await page.getByRole("searchbox", { name: "Search movies" }).fill("lanterns");
    await expect(titles).toHaveText([GREEK]);
    await page.getByRole("searchbox", { name: "Search movies" }).fill("φανάρια");
    await expect(titles).toHaveText([GREEK]);
  });

  test("the movie page shows its shown title and sets its own from the known names", async ({ page, api }) => {
    let override = "";
    const detail = () => ({ ...media.movieDetail(4), display_title: override || GREEK, title_override: override || undefined });
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/movies/4", respond: detail },
      { method: "GET", path: "/api/v1/movies/4/title-choices", body: { choices: [
        { title: "Lanterns Over the Northern Sea", kind: "english" },
        { title: GREEK, kind: "original", lang: "el" },
        { title: "Les Lanternes de la mer du Nord", kind: "translation", lang: "fr", country: "FR" },
      ] } },
      { method: "PUT", path: "/api/v1/movies/4/display-title", respond: ({ body }) => {
        override = (body as { title: string }).title;
        return { id: 4, title: "Lanterns Over the Northern Sea", display_title: override || GREEK, title_override: override };
      } },
    ]);
    await page.goto("/movies/4");
    await expect(page.getByRole("heading", { name: GREEK }).first()).toBeVisible();
    // The English title is still there, small, for whoever needs it.
    await expect(page.getByText("Lanterns Over the Northern Sea", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Display title…" }).click();
    const dialog = page.getByRole("dialog", { name: "Display title" });
    await dialog.getByRole("button", { name: /Les Lanternes de la mer du Nord/ }).click();
    await expect(dialog.getByRole("textbox", { name: "Title to show" })).toHaveValue("Les Lanternes de la mer du Nord");
    await dialog.getByRole("button", { name: "Show this title" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/movies/4/display-title").map((c) => c.body)).toEqual([{ title: "Les Lanternes de la mer du Nord" }]);
    await expect(page.getByRole("heading", { name: "Les Lanternes de la mer du Nord" }).first()).toBeVisible();
  });
});

test.describe("settings", () => {
  test.use({ persona: "admin" });

  test("Show titles as: a language mode waits for its language, then saves both together", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/settings", respond: ({ body }) => ({ ...settings, ...(body as object) }) },
    ]);
    await page.goto("/settings/library#display-titles");
    const mode = page.getByRole("combobox", { name: "Show titles as" });
    await expect(mode).toHaveValue("english");
    await mode.selectOption("language");
    await expect(page.getByText("Pick the language to show titles in.")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0);

    await page.getByRole("combobox", { name: "Language to show titles in" }).selectOption("fr");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([
      { display_title_mode: "language", display_title_language: "fr" },
    ]);

    await page.getByRole("combobox", { name: "Add an original language" }).fill("Japanese");
    await page.getByRole("listbox", { name: "Matching languages" }).getByRole("option", { name: /Japanese/ }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toContainEqual({ display_title_original_in: ["ja"] });
  });
});
