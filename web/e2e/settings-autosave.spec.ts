import { test, expect, type MockedApi } from "./mockApi";
import { settings } from "./fixtures/system";
import type { MockRoute } from "./fixtures/routes";

// CFG-14: every setting saves itself — toggles on change, text on blur — with an inline
// "Saved ✓" or the reason it didn't save, and no Save bars. Folders save per row, only
// when one is picked or confirmed.

const putSettings: MockRoute = { method: "PUT", path: "/api/v1/settings", respond: ({ body }) => ({ ...settings, ...(body as object) }) };

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("the disk guard pair checks itself before sending, then saves both together", async ({ page, api }) => {
    await installExtra(page, api, [
      putSettings,
      { method: "GET", path: "/api/v1/downloads/disk-guard", body: { path: "/media/downloads", measurable: true, used_pct: 40, holding: 0, shared_with: [] } },
    ]);
    await page.goto("/settings/downloads");
    const pause = page.getByRole("textbox", { name: "Pause at (% full)" });
    const resume = page.getByRole("textbox", { name: "Resume at (% full)" });
    await pause.fill("85"); // below the saved resume point (90)
    await resume.focus(); // leaving pause commits the pair
    await expect(page.getByText(/Resume \(90%\) must be below pause \(85%\)/)).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0);

    await resume.fill("80");
    await resume.press("Enter");
    await expect(page.getByText("Saved ✓")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([
      { downloads_disk_guard_pause_pct: "85", downloads_disk_guard_resume_pct: "80" },
    ]);
    await expect(page.getByRole("button", { name: /^Save/ })).toHaveCount(0);
  });

  test("a server refusal shows under the field and nothing else is lost", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/settings", status: 400, body: { status: "error", message: "a name template can't be empty" } },
    ]);
    await page.goto("/settings/media");
    const folder = page.getByRole("textbox", { name: "Folder name" });
    await folder.fill("{Title}");
    await folder.blur();
    await expect(page.getByRole("alert").filter({ hasText: "a name template can't be empty" })).toBeVisible();
    await expect(folder).toHaveValue("{Title}");
    // Esc puts the saved template back.
    await folder.focus();
    await folder.press("Escape");
    await expect(folder).toHaveValue(settings.naming_movie_folder);
  });

  test("turning Books off saves at once and hides it from the nav", async ({ page, api }) => {
    await installExtra(page, api, [
      putSettings,
      { method: "GET", path: "/api/v1/apikeys", body: { keys: [] } },
      { method: "GET", path: "/api/v1/system/backups", body: { backups: [], total_bytes: 0, free_bytes: null, dir: "/data/backups", can_restart: true, settings: { enabled: true, hour: 3, keep_nightly: 7 } } },
    ]);
    await page.goto("/settings/system");
    const nav = page.getByRole("navigation").first();
    await expect(nav.getByRole("link", { name: "Books", exact: true })).toBeVisible();
    await page.getByRole("switch", { name: "Books" }).click();
    await expect(nav.getByRole("link", { name: "Books", exact: true })).toHaveCount(0);
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([{ books_enabled: false }]);
  });

  test("the Discovery region on Connections saves on Enter, with no Save button", async ({ page, api }) => {
    await installExtra(page, api, [putSettings]);
    await page.goto("/settings/connections");
    const card = page.locator("#discovery-region");
    const region = card.getByRole("textbox", { name: "Discovery region" });
    await expect(region).toHaveValue(settings.tmdb_region);
    await region.fill("AU");
    await region.press("Enter");
    await expect(card.getByText("Saved ✓")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([{ tmdb_region: "AU" }]);
    await expect(card.getByRole("button", { name: /^Save/ })).toHaveCount(0);
  });

  test("a title language picked on Library saves at once, and removes the same way", async ({ page, api }) => {
    await installExtra(page, api, [putSettings]);
    await page.goto("/settings/library#title-languages");
    const card = page.locator("#title-languages");
    await expect(card.getByText("Also match titles in")).toBeVisible();
    const picker = card.getByRole("combobox", { name: "Add a title language" });
    await picker.fill("Fren");
    await picker.press("Enter");
    await expect(card.getByText("Saved ✓")).toBeVisible();
    await expect(card.getByText("French")).toBeVisible();
    await card.getByRole("button", { name: "Remove French" }).click();
    await expect(card.getByText("French")).toHaveCount(0);
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([
      { match_title_languages: ["fr"] },
      { match_title_languages: [] },
    ]);
  });

  test("a typed folder saves only that row, and only when confirmed", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/system/library", respond: ({ body }) => ({ movies: "/media/movies", tv: "/media/tv", ebooks: "/media/ebooks", audiobooks: "/media/audiobooks", music: "/media/music", downloads: "/media/downloads", ...(body as object) }) },
    ]);
    await page.goto("/settings/library");
    const tv = page.getByRole("textbox", { name: "TV Shows folder" });
    await tv.fill("/media/shows");
    await tv.blur(); // leaving a folder field never saves it
    expect(api.callsTo("PUT", "/api/v1/system/library")).toHaveLength(0);
    await page.getByRole("button", { name: "Use this folder" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/system/library").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/system/library")[0].body).toEqual({ tv: "/media/shows" });
    await expect(page.getByText("Saved ✓")).toBeVisible();
    await expect(page.getByRole("button", { name: "Save folders" })).toHaveCount(0);
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
