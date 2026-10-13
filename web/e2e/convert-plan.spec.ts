import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import type { ConvertAutoPreview, ConvertCandidate, ConvertPlan } from "../src/lib/api";
import { convertSettings, convertStats } from "./fixtures/modules";

// CONV-12/13: each Library file shows its conversion plan (tracks kept or removed, the
// warnings) and its poster, and nothing converts without a confirmation that says what.

const GB = 1024 ** 3;

const remux: ConvertCandidate = {
  kind: "movie", key: "movie:7", movie_id: 7, title: "Remux", year: 2020, poster_url: "https://image.tmdb.org/t/p/w500/remux.jpg",
  path: "/movies/Remux (2020).mp4", candidate: true, worth: true, save_bytes: 30 * GB, est_bytes: 40 * GB, tracks: "audio 3 → 1 · subtitles 3 → 1",
  needs: { video: true, subs: true, audio: true, save: 30 * GB, worth: true },
  info: { container: "MP4", video_codec: "hevc", width: 3840, height: 2160, resolution: "2160p", hdr: "Dolby Vision", dv_base: "HDR10",
    bitrate_kbps: 60000, frame_rate: 23.976, duration_sec: 7200, size_bytes: 70 * GB, audio_tracks: 3, sub_tracks: 3, ten_bit: true },
};

// A long track list: the card must wrap, not push the page sideways.
const manySubs = Array.from({ length: 24 }, (_, i) => ({
  type: "subtitle" as const, index: i + 2, codec: "subrip", lang: ["fre", "ger", "spa", "ita"][i % 4], title: `Subtitle track number ${i + 2} with a long descriptive title`,
  keep: false, reason: "language not kept",
}));

const plan: ConvertPlan = {
  key: "movie:7", kind: "movie", title: "Remux", poster_url: remux.poster_url, size_bytes: 70 * GB,
  needs: remux.needs,
  video: { from_codec: "HEVC", from_res: "2160p", from_hdr: "Dolby Vision", from_bits: 10, to_codec: "HEVC", to_bits: 10, to_hdr: "HDR10", crf: 18,
    est_bytes: 40 * GB, crop_note: "black bars are kept (crop is off)", hdr_note: "HDR10+ is checked at conversion time and kept if present (HEVC only)" },
  audio: [
    { type: "audio", index: 0, codec: "truehd", lang: "eng", channels: 8, keep: true },
    { type: "audio", index: 1, codec: "ac3", lang: "eng", channels: 2, title: "Director's commentary", keep: false, reason: "commentary" },
    { type: "audio", index: 2, codec: "aac", lang: "fre", channels: 2, keep: false, reason: "language not kept" },
  ],
  subs: [
    { type: "subtitle", index: 0, codec: "hdmv_pgs_subtitle", lang: "eng", image: true, keep: false, reason: "image subtitle covered by text" },
    { type: "subtitle", index: 1, codec: "hdmv_pgs_subtitle", lang: "eng", image: true, forced: true, keep: true },
    ...manySubs,
  ],
  container: { from: "MP4", to: "MKV" },
  warnings: ["embedded closed captions (CEA-608/708) are lost on re-encode", "Dolby Vision layer dropped — kept as HDR10", "the container changes from MP4 to MKV — the file's extension changes too"],
  eta_sec: null,
};

const libraryRoutes: MockRoute[] = [
  { method: "GET", path: "/api/v1/convert/library", body: { items: [remux] } },
  { method: "GET", path: "/api/v1/convert/plan", body: plan },
];

for (const size of ["desktop", "phone"] as const) {
  test.describe(`plan card (${size})`, () => {
    test.use({ persona: "admin", viewport: size === "phone" ? { width: 375, height: 812 } : { width: 1440, height: 900 } });

    test("a Library row shows its poster and expands to its plan", async ({ page, api }) => {
      await installExtra(page, api, libraryRoutes);
      await page.goto("/convert?tab=library");
      const toggle = page.getByRole("button", { name: "Show the conversion plan for Remux" });
      await expect(toggle).toBeVisible();
      await expect(toggle.locator("img")).toHaveAttribute("src", /\/t\/p\/w342\/remux\.jpg$/);
      await expect(toggle).toHaveAttribute("aria-expanded", "false");

      await toggle.click();
      await expect(page.getByRole("button", { name: "Hide the conversion plan for Remux" })).toHaveAttribute("aria-expanded", "true");
      await expect(page.getByText("HEVC 2160p Dolby Vision 10-bit → HEVC HDR10 10-bit · CRF 18")).toBeVisible();
      await expect(page.getByText("ENG · AC-3 stereo · Director's commentary")).toBeVisible();
      await expect(page.getByText("ENG · PGS · forced · image")).toBeVisible();
      await expect(page.getByText("⚠ Dolby Vision layer dropped — kept as HDR10")).toBeVisible();
      expect(api.callsTo("GET", "/api/v1/convert/plan?key=movie%3A7")).toHaveLength(1);

      // The page never scrolls sideways, however long the track list.
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      const card = page.getByText("Conversion plan", { exact: true }).locator("..");
      const box = await card.boundingBox();
      expect(box!.width).toBeLessThanOrEqual(size === "phone" ? 375 : 1440);

      await page.getByRole("button", { name: "Hide the conversion plan for Remux" }).click();
      await expect(page.getByText("Conversion plan", { exact: true })).toHaveCount(0);
    });
  });
}

const preview = (over: Partial<ConvertAutoPreview> = {}): ConvertAutoPreview => ({
  files: 120, reencode: 100, bytes: 20 * 1024 * GB, est_bytes: 11 * 1024 * GB, dv_files: 3, cc_files: 1, image_sub_tracks: 14,
  filtered_audio_tracks: 2, container_changes: 0, forced_tracks: 0, as_of: 0, ...over,
});

const episode: ConvertCandidate = {
  ...remux, kind: "episode", key: "episode:10:2:1", movie_id: undefined, series_id: 10, season: 2, episode: 1, title: "Show - S02E01", poster_url: undefined,
};

test.describe("confirm before converting", () => {
  test.use({ persona: "admin" });

  test("Convert now lists what will change, and only Confirm sends the request", async ({ page, api }) => {
    await installExtra(page, api, [...libraryRoutes, { method: "POST", path: "/api/v1/convert/requests", status: 202, body: { requested: "movie:7", starting: false, position: 2, ahead: "Show - S01E01" } }]);
    await page.goto("/convert?tab=library");
    await page.getByRole("button", { name: "Convert now" }).click();
    const dialog = page.getByRole("dialog", { name: "Convert “Remux” now?" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("• Picture: HEVC 2160p Dolby Vision 10-bit → HEVC HDR10 10-bit · CRF 18")).toBeVisible();
    await expect(dialog.getByText(/• Audio: 2 of 3 tracks removed — ENG · AC-3 stereo · Director's commentary \(commentary\)/)).toBeVisible();
    await expect(dialog.getByText("• Container: MP4 → MKV")).toBeVisible();
    await expect(dialog.getByText("⚠ Dolby Vision layer dropped — kept as HDR10")).toBeVisible();

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    expect(api.callsTo("POST", "/api/v1/convert/requests")).toHaveLength(0);

    await page.getByRole("button", { name: "Convert now" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Convert now" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/convert/requests").map((c) => c.body)).toEqual([{ key: "movie:7" }]);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    // The toast says what the server says happened: its place in line and who's ahead.
    await expect(page.getByRole("status").filter({ hasText: "“Remux” is #2 in line — after “Show - S01E01”" })).toBeVisible();
  });

  test("Switch on shows library-wide counts and switches on only after Confirm", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/convert/auto-preview", body: preview() },
      { method: "PUT", path: "/api/v1/convert/settings", respond: ({ body }) => ({ ...convertSettings, ...(body as object) }) },
    ]);
    await page.goto("/convert");
    await page.getByRole("button", { name: "Switch on" }).click();
    const dialog = page.getByRole("dialog", { name: "Switch on automatic conversion?" });
    await expect(dialog.getByText("• 120 files (100 re-encoded, 20 only rewritten to tidy tracks): about 20.00 TB → ~11.00 TB")).toBeVisible();
    await expect(dialog.getByText("• 3 files lose Dolby Vision (kept as HDR10 or HLG)")).toBeVisible();
    await expect(dialog.getByText("• 1 file loses embedded closed captions")).toBeVisible();
    await expect(dialog.getByText("• 14 image-subtitle tracks removed")).toBeVisible();
    await expect(dialog.getByText("• 2 audio tracks filtered out")).toBeVisible();

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    expect(api.callsTo("PUT", "/api/v1/convert/settings")).toHaveLength(0);

    await page.getByRole("button", { name: "Switch on" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Switch on" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/convert/settings").map((c) => c.body)).toEqual([{ auto: true }]);
    await expect(page.getByRole("button", { name: "Switch off" })).toBeVisible();

    // Switching off is one click.
    await page.getByRole("button", { name: "Switch off" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/convert/settings").map((c) => c.body)).toEqual([{ auto: true }, { auto: false }]);
  });

  test("the settings switch asks the same way", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/convert/auto-preview", body: preview() },
      { method: "PUT", path: "/api/v1/convert/settings", respond: ({ body }) => ({ ...convertSettings, ...(body as object) }) },
    ]);
    await page.goto("/settings/tools#convert");
    await page.getByRole("checkbox", { name: /Convert my library automatically/ }).click();
    const dialog = page.getByRole("dialog", { name: "Switch on automatic conversion?" });
    await expect(dialog.getByText("• 3 files lose Dolby Vision (kept as HDR10 or HLG)")).toBeVisible();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("checkbox", { name: /Convert my library automatically/ })).not.toBeChecked();
    expect(api.callsTo("PUT", "/api/v1/convert/settings")).toHaveLength(0);
  });

  test("a season convert shows that season's counts", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/convert/library", respond: ({ url }) => url.searchParams.get("series")
        ? { items: [episode] }
        : { series: [{ series_id: 10, title: "Show", year: 2019, files: 20, convertible: 5, reencode: 5, tidy_only: 0, total_bytes: 100 * GB, est_bytes: 60 * GB, save_bytes: 40 * GB }] } },
      { method: "GET", path: "/api/v1/convert/auto-preview", respond: ({ url }) => url.searchParams.get("season") === "2"
        ? preview({ files: 4, reencode: 4, bytes: 40 * GB, est_bytes: 22 * GB, dv_files: 0, cc_files: 2, image_sub_tracks: 0, filtered_audio_tracks: 0 })
        : preview() },
      { method: "POST", path: /^\/api\/v1\/convert\/series\/10$/, status: 202, body: { requested: 4 } },
    ]);
    await page.goto("/convert?tab=library&media=tv");
    await page.getByRole("button", { name: "Episodes" }).click();
    await page.getByRole("button", { name: "Convert S2" }).click();
    const dialog = page.getByRole("dialog", { name: "Convert Show season 2 now?" });
    await expect(dialog.getByText("• 4 files: about 40.0 GB → ~22.0 GB")).toBeVisible();
    await expect(dialog.getByText("• 2 files lose embedded closed captions")).toBeVisible();
    expect(api.callsTo("GET", "/api/v1/convert/auto-preview?series=10&season=2")).toHaveLength(1);
    await dialog.getByRole("button", { name: "Convert 4 episodes" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/convert/series/10").map((c) => c.path)).toEqual(["/api/v1/convert/series/10?season=2"]);
  });
});

// CONV-15: Dolby Vision is a stated, counted choice in Settings → Tools → Convert.
test.describe("Dolby Vision choice", () => {
  test.use({ persona: "admin" });

  test("shows how many files it applies to and saves the choice", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/convert/stats", body: { ...convertStats, total: { ...convertStats.total, dolby_vision_files: 47 } } },
      { method: "PUT", path: "/api/v1/convert/settings", respond: ({ body }) => ({ ...convertSettings, ...(body as object) }) },
    ]);
    await page.goto("/settings/tools#convert");
    const group = page.getByRole("radiogroup", { name: "Dolby Vision files" });
    await expect(group.getByText("Dolby Vision files (47 in your library)")).toBeVisible();
    await expect(group.getByRole("radio", { name: /Convert and drop the Dolby Vision layer/ })).toBeChecked();
    await group.getByRole("radio", { name: /Leave them alone/ }).check();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/convert/settings").map((c) => c.body)).toEqual([{ dolby_vision: "skip" }]);
    await expect(group.getByRole("radio", { name: /Leave them alone/ })).toBeChecked();
  });
});

// CONV-23: the daily library scan's time is a setting under Advanced, and saves on leaving it.
test.describe("Daily library scan time", () => {
  test.use({ persona: "admin" });

  test("round-trips through the settings", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/convert/settings", respond: ({ body }) => ({ ...convertSettings, ...(body as object) }) },
    ]);
    await page.goto("/settings/tools#convert");
    await page.getByText("Advanced", { exact: true }).click();
    const field = page.getByLabel("Daily library scan at");
    await expect(field).toHaveValue("03:00");
    await field.fill("04:30");
    await field.press("Tab");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/convert/settings").map((c) => c.body)).toEqual([{ scan_at: "04:30" }]);
    await expect(field).toHaveValue("04:30");
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
