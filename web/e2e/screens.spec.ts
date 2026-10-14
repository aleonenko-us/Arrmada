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
  "/settings/subtitles",
  "/settings/convert",
  "/settings/alerts",
  "/settings/plex",
  "/settings/audiobook-server",
  "/settings/users",
  "/settings/import",
  "/settings/system",
  "/settings/status",
];
const REQUESTER: string[] = ["/discover", "/discover?tab=books", "/requests", "/calendar", "/shelf", "/audiobooks", "/me"];

// Pages that read an endpoint the shared fixtures leave to the specs that test them
// (method and path → body).
const EXTRA: Record<string, Record<string, unknown>> = {
  "/settings/downloads": { "GET /api/v1/downloads/disk-guard": { path: "/media/downloads", measurable: true, used_pct: 40, holding: 0, shared_with: [] } },
  "/settings/audiobook-server": {
    "GET /api/v1/audioserver": { enabled: true, running: true, error: "", host_port: "13378", public_url: "", items: 12, items_ready: 12, trace_until: 0, users: [], devices: [] },
  },
  // The movie target-file builder, as quality-editor.spec opens it.
  "/quality/movie/hd-1080p": {
    "GET /api/v1/quality/profiles/hd-1080p": {
      id: 7, media_type: "movie", name: "HD 1080p", allowed_resolutions: ["1080p"], min_source: "WEB-DL", max_source: "",
      bitrate_cap_mbps: 0, small_bias: 0, min_format_score: 0, format_scores: {}, min_seeders: 1, stall_minutes: 0,
      upgrades_enabled: true, upgrade_min_percent: 0, ideal: {},
    },
    "POST /api/v1/quality/preview": { profile: "", decision: { winner: null, eligible: [], rejected: [] } },
    "POST /api/v1/library/fit/preview": { scope: "library", counts: { titles: 0, files: 0, fits: 0, over: 0, under: 0, mismatch: 0 } },
    "POST /api/v1/quality/impact": { replace: { files: 0, bytes: 0, examples: [] }, search: { files: 0, bytes: 0, examples: [] } },
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
        if (extra) {
          await page.route((u) => u.pathname.startsWith("/api/"), (r) => {
            const key = `${r.request().method()} ${new URL(r.request().url()).pathname}`;
            return key in extra ? r.fulfill({ json: extra[key] }) : r.fallback();
          });
        }
        await page.goto(path);
        await expect(page.getByRole("heading").first()).toBeVisible();
        await api.quiet();
        // Fonts and lazy images settle before the picture is taken.
        await page.evaluate(() => document.fonts.ready);
        // FE-30's floor: no HTML text renders below 10.5px (the 2xs eyebrow size). Chart
        // labels inside an <svg> scale with the chart and are left out.
        const tiny = await page.evaluate(() => {
          const out: string[] = [];
          const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
          for (let n = walker.nextNode(); n; n = walker.nextNode()) {
            const el = n.parentElement;
            if (!el || !n.textContent?.trim() || el.closest("svg")) continue;
            const box = el.getBoundingClientRect();
            if (box.width === 0 || box.height === 0) continue;
            const px = parseFloat(getComputedStyle(el).fontSize);
            if (px < 10.5) out.push(`${px}px "${n.textContent.trim().slice(0, 30)}"`);
          }
          return out;
        });
        expect.soft(tiny, "text below the 10.5px floor").toEqual([]);
        const name = `${info.project.name}-${theme}-${persona}${path.replace(/[/?=#&]+/g, "_")}.png`;
        await expect.soft(page).toHaveScreenshot(name, { fullPage: true, animations: "disabled", caret: "hide", maxDiffPixels: 0 });
      });
    });
  }
}
