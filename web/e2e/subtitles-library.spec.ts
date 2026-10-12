import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import type { SubFileEntry } from "../src/lib/api";

// SUB-26 / SUB-10: the Subtitles page's movie Library — text search, posters, rows a page
// at a time, and the Orphaned filter with Adopt / Replace, single and bulk.

function movie(i: number, extra: Partial<SubFileEntry> = {}): SubFileEntry {
  return {
    kind: "movie", movie_id: i, title: `Film ${String(i).padStart(3, "0")}`, year: 2000 + (i % 20),
    poster_url: `https://image.tmdb.org/t/p/w500/p${i}.jpg`, path: `/movies/Film ${i}/Film ${i}.mkv`,
    embedded: [], external: ["en"], languages: [{ lang: "en", have: true }], missing: 0, ...extra,
  };
}

// 250 movies: Dune is one of them, and two have orphaned subtitles.
const items: SubFileEntry[] = Array.from({ length: 250 }, (_, i) => movie(i + 1));
items[6] = movie(7, { title: "Dune", year: 2021 });
items[20] = movie(21, { missing: 1, external: [], languages: [{ lang: "en", have: false, orphan: true }], orphans: [{ name: "Old.Name.eng.srt", lang: "eng" }] });
items[40] = movie(41, { missing: 1, external: [], languages: [{ lang: "en", have: false, orphan: true }], orphans: [{ name: "Other.en.srt", lang: "en" }, { name: "Other.en.forced.srt", lang: "en", variant: "forced" }] });

const library: MockRoute[] = [
  { method: "GET", path: "/api/v1/subtitles/library", body: { items, scanning: false, stale: false } },
  { method: "POST", path: /^\/api\/v1\/subtitles\/movies\/(\d+)\/orphans\/adopt$/, respond: () => ({ name: "Film 021 (2001).en.srt" }) },
  { method: "POST", path: /^\/api\/v1\/subtitles\/movies\/(\d+)\/orphans\/replace$/, status: 202, respond: ({ params }) => ({ id: 9, kind: "movie", movie_id: Number(params[0]), title: "x", state: "queued", at: 0, priority: 1 }) },
  { method: "POST", path: "/api/v1/subtitles/orphans/bulk", body: { done: 1, skipped: [{ movie_id: 41, title: "Other", name: "Other.en.forced.srt", reason: "forced subtitles aren't replaced" }], errors: [] } },
];

test.describe("Subtitles movie Library", () => {
  test.use({ persona: "admin" });

  test("search, posters and paged rows", async ({ page, api }) => {
    await installExtra(page, api, library);
    await page.goto("/subtitles?tab=library");
    const rows = page.locator("tbody tr");
    await expect(rows).toHaveCount(100);
    await expect(rows.first().locator("img")).toHaveAttribute("src", /\/t\/p\/w92\//);
    await expect(rows.first().locator("img")).toHaveAttribute("loading", "lazy");

    const more = page.getByRole("button", { name: /Show more/ });
    await more.click();
    await expect(rows).toHaveCount(200);
    await more.click();
    await expect(rows).toHaveCount(250);
    await expect(more).toHaveCount(0);

    await page.getByRole("textbox", { name: "Search movies" }).fill("dune");
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText("Dune");
    await page.getByRole("textbox", { name: "Search movies" }).fill("2021");
    await expect(rows.first()).toContainText("Dune");
    await page.getByRole("textbox", { name: "Search movies" }).fill("no such film");
    await expect(page.getByText("No movies match.")).toBeVisible();
  });

  test("orphans: adopt and replace one, then adopt all in view", async ({ page, api }) => {
    await installExtra(page, api, library);
    await page.goto("/subtitles?tab=library");
    await page.getByRole("button", { name: /^Orphaned/ }).click();
    const rows = page.locator("tbody tr");
    await expect(rows).toHaveCount(2);
    // A forced orphan can be adopted but not replaced.
    await expect(rows.filter({ hasText: "Other.en.forced.srt" }).getByRole("button", { name: "Replace" })).toHaveCount(1);

    await rows.filter({ hasText: "Old.Name.eng.srt" }).getByRole("button", { name: "Adopt" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/subtitles/movies/21/orphans/adopt").map((c) => c.body)).toEqual([{ name: "Old.Name.eng.srt" }]);

    await rows.filter({ hasText: "Old.Name.eng.srt" }).getByRole("button", { name: "Replace" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("recycle bin");
    await dialog.getByRole("button", { name: "Replace" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/subtitles/movies/21/orphans/replace").length).toBe(1);

    await page.getByRole("button", { name: "Adopt all" }).click();
    await expect(dialog).toContainText("Adopt 3 orphaned subtitles?");
    await dialog.getByRole("button", { name: "Adopt all" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/subtitles/orphans/bulk").map((c) => c.body)).toEqual([{ action: "adopt", movie_ids: [21, 41] }]);
  });

  test.describe("phone", () => {
    test.use({ viewport: { width: 375, height: 812 } });

    test("the Library fits a phone: the table scrolls, the page doesn't", async ({ page, api }) => {
      await installExtra(page, api, library);
      await page.goto("/subtitles?tab=library");
      await expect(page.locator("tbody tr")).toHaveCount(100);
      await expect(page.getByRole("textbox", { name: "Search movies" })).toBeVisible();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(overflow).toBeLessThanOrEqual(0);
    });
  });
});

// installExtra puts fixtures in front of the defaults for one test.
async function installExtra(page: import("@playwright/test").Page, api: MockedApi, extra: MockRoute[]) {
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
