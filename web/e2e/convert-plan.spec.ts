import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import type { ConvertCandidate, ConvertPlan } from "../src/lib/api";

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
