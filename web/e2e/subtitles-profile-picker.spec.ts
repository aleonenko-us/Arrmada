import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import type { ProfileList } from "../src/pages/subtitles/profileApi";

// SUB-34: the Movie and Series pages' "Subtitles" selector pins the title to a language
// profile once there is more than Default to choose from, and fits a phone screen.

const profiles: ProfileList = {
  profiles: [
    { id: 1, name: "Default", is_default: true, titles: 0, items: [{ lang: "en", variant: "full", sdh: "any", only_if_audio_differs: false }] },
    { id: 2, name: "Anime", is_default: false, titles: 1, items: [{ lang: "ja", variant: "full", sdh: "avoid", only_if_audio_differs: false }, { lang: "en", variant: "full", sdh: "avoid", only_if_audio_differs: false }] },
  ],
  overrides: [{ kind: "series", id: 1, profile_id: 2, title: "Fixture Show" }],
};

const extra: MockRoute[] = [
  { method: "GET", path: "/api/v1/subtitles/profiles", body: profiles },
  { method: "PUT", path: /^\/api\/v1\/subtitles\/overrides\/(movie|series)\/(\d+)$/, status: 204 },
  { method: "DELETE", path: /^\/api\/v1\/subtitles\/overrides\/(movie|series)\/(\d+)$/, status: 204 },
];

async function install(page: Page, api: MockedApi) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method()) continue;
      const m = typeof r.path === "string" ? (url.pathname === r.path ? [] : null) : url.pathname.match(r.path);
      if (!m) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname, body });
      return r.status === 204 ? route.fulfill({ status: 204 }) : route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(r.body) });
    }
    return route.fallback();
  });
}

async function fitsScreen(page: Page) {
  const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
  expect(w.doc, "document width").toBeLessThanOrEqual(w.viewport);
}

test.describe("subtitle profile picker", () => {
  test.use({ persona: "admin" });

  test("a series pinned to Anime shows it, and goes back to Default", async ({ page, api }) => {
    await install(page, api);
    await page.goto("/series/1");
    const pick = page.getByLabel("Subtitles", { exact: true });
    await expect(pick).toHaveValue("2");
    await fitsScreen(page);
    await pick.selectOption("1");
    await expect.poll(() => api.callsTo("DELETE", "/api/v1/subtitles/overrides/series/1").length).toBe(1);
  });

  test("a movie on Default is pinned to Anime", async ({ page, api }) => {
    await install(page, api);
    await page.goto("/movies/1");
    const pick = page.getByLabel("Subtitles", { exact: true });
    await expect(pick).toHaveValue("1");
    await fitsScreen(page);
    await pick.selectOption("2");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/subtitles/overrides/movie/1").map((c) => c.body)).toEqual([{ profile_id: 2 }]);
  });
});
