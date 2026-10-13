import { test, expect } from "./mockApi";
import type { Persona } from "./fixtures/users";

// Before/after screenshots of every page (FE-18 widths, FE-30 type scale), run on
// demand and never in CI:
//
//   SCREENS_DIR=<folder outside the repo> npx playwright test --project=screens --update-snapshots   (before)
//   SCREENS_DIR=<same folder> npx playwright test --project=screens                                    (after)
//
// The first run writes the baseline; the second compares against it and fails each page
// that changed, with a diff image beside it. Snapshots live in SCREENS_DIR, so they are
// never committed. Each page is taken at 1440 and 375 px in both themes.
const STAFF: string[] = [
  "/",
  "/activity/queue",
  "/activity/needs-you",
  "/activity/wanted",
  "/activity/history",
  "/activity/blocklist",
  "/discover",
  "/movies",
  "/movies/wanted",
  "/movies/1",
  "/series",
  "/series/1",
  "/requests",
  "/calendar",
  "/music",
  "/books",
  "/subtitles",
  "/subtitles?tab=library",
  "/convert",
  "/insights",
  "/audiobooks",
  "/me",
  "/quality",
  "/quality/movie/hd-1080p",
  "/logs",
  "/settings/library",
  "/settings/media",
  "/settings/connections",
  "/settings/downloads",
  "/settings/tools",
  "/settings/alerts",
  "/settings/plex",
  "/settings/audiobook-server",
  "/settings/users",
  "/settings/import",
  "/settings/system",
  "/settings/status",
];
const REQUESTER: string[] = ["/discover", "/discover?tab=books", "/requests", "/calendar", "/shelf", "/audiobooks", "/me"];

// Two sections read an endpoint the shared fixtures leave to the specs that test them.
const EXTRA: Record<string, { path: string; body: unknown }> = {
  "/settings/downloads": { path: "/api/v1/downloads/disk-guard", body: { path: "/media/downloads", measurable: true, used_pct: 40, holding: 0, shared_with: [] } },
  "/settings/audiobook-server": {
    path: "/api/v1/audioserver",
    body: { enabled: true, running: true, error: "", host_port: "13378", public_url: "", items: 12, items_ready: 12, trace_until: 0, users: [], devices: [] },
  },
};

const shots: { persona: Persona; path: string }[] = [
  ...STAFF.map((path) => ({ persona: "admin" as const, path })),
  ...REQUESTER.map((path) => ({ persona: "requester" as const, path })),
];

for (const theme of ["dark", "light"] as const) {
  for (const { persona, path } of shots) {
    test.describe(`${theme} ${persona}`, () => {
      test.use({ persona });
      test(`${path}`, async ({ page, api }, info) => {
        await page.addInitScript((t) => localStorage.setItem("arrmada.theme", t), theme);
        const extra = EXTRA[path];
        if (extra) await page.route((u) => u.pathname === extra.path, (r) => r.fulfill({ json: extra.body }));
        await page.goto(path);
        await expect(page.getByRole("heading").first()).toBeVisible();
        await api.quiet();
        // Fonts and lazy images settle before the picture is taken.
        await page.evaluate(() => document.fonts.ready);
        const name = `${info.project.name}-${theme}-${persona}${path.replace(/[/?=#&]+/g, "_")}.png`;
        await expect.soft(page).toHaveScreenshot(name, { fullPage: true, animations: "disabled", caret: "hide", maxDiffPixels: 0 });
      });
    });
  }
}
