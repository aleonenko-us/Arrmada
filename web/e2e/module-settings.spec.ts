import { test, expect, type MockedApi } from "./mockApi";
import { convertSettings, subtitleSettings } from "./fixtures/modules";
import type { MockRoute } from "./fixtures/routes";

// CFG-17: the Subtitles, Convert and Audiobook-server settings live in the Settings hub.
// The module pages keep their working views, link to their settings with Configure →, and
// their old Settings-tab addresses land on the new sections.

const audioServer = {
  enabled: true, running: true, error: "", host_port: "13378", public_url: "", items: 12, items_ready: 12, trace_until: 0,
  users: [], devices: [],
};

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("old Settings-tab addresses land on the hub sections", async ({ page, api }) => {
    await installExtra(page, api, [{ method: "GET", path: "/api/v1/audioserver", body: audioServer }]);
    await page.goto("/subtitles?tab=settings");
    await expect(page).toHaveURL(/\/settings\/tools#subtitles$/);
    await expect(page.getByRole("heading", { name: "Subtitles", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Convert", exact: true })).toBeVisible();

    await page.goto("/convert?tab=settings");
    await expect(page).toHaveURL(/\/settings\/tools#convert$/);

    await page.goto("/audiobooks?tab=server");
    await expect(page).toHaveURL(/\/settings\/audiobook-server$/);
    await expect(page.getByText("Running", { exact: true })).toBeVisible();
  });

  test("the module pages have no Settings tab, and Configure opens their section", async ({ page }) => {
    await page.goto("/subtitles");
    await expect(page.getByRole("tab", { name: "Settings" })).toHaveCount(0);
    await page.getByRole("link", { name: "Configure →" }).click();
    await expect(page).toHaveURL(/\/settings\/tools#subtitles$/);

    await page.goto("/convert");
    await expect(page.getByRole("tab", { name: "Settings" })).toHaveCount(0);
    await page.getByRole("link", { name: "Configure →" }).click();
    await expect(page).toHaveURL(/\/settings\/tools#convert$/);
  });

  test("a Subtitles and a Convert setting changed in the hub are saved to their modules", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/subtitles/settings", respond: ({ body }) => ({ ...subtitleSettings, ...(body as object) }) },
      { method: "PUT", path: "/api/v1/convert/settings", respond: ({ body }) => ({ ...convertSettings, ...(body as object) }) },
    ]);
    await page.goto("/settings/tools");
    await page.getByRole("switch", { name: "Auto-ensure movies" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/subtitles/settings").map((c) => c.body)).toEqual([{ movies_auto: true }]);

    const audio = page.getByRole("textbox", { name: "Keep audio in" });
    await audio.fill("en, ja");
    await audio.press("Enter");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/convert/settings").map((c) => c.body)).toEqual([{ keep_audio_langs: "en, ja" }]);
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0);
  });

  // SUB-31: kept languages are searched (regional variants too) and ordered.
  test("kept languages: search adds a regional variant, and the order saves", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/subtitles/settings", respond: ({ body }) => ({ ...subtitleSettings, ...(body as object) }) },
    ]);
    await page.goto("/settings/tools");
    const box = page.getByRole("combobox", { name: "Add a kept language" });
    await box.fill("port");
    await expect(page.getByRole("listbox", { name: "Matching languages" }).getByRole("option")).toHaveText(["Portuguesept", "Portuguese (Brazil)pt-br", "Portuguese (Portugal)pt-pt"]);
    await page.getByRole("option", { name: /Portuguese \(Brazil\)/ }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/subtitles/settings").map((c) => c.body)).toEqual([{ languages: ["en", "pt-br"] }]);
    await page.getByRole("button", { name: "Move Portuguese (Brazil) up" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/subtitles/settings").map((c) => c.body).slice(-1)[0]).toEqual({ languages: ["pt-br", "en"] });
    await expect(page.getByText("A subtitle file with no language in its name counts as this one.")).toBeVisible();
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
