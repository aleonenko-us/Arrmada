import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import type { SubtitleJob } from "../src/lib/api";
import { NOW } from "./fixtures/clock";

// SUB-24 / SUB-25: the Subtitles Queue tab — the fast lane and the AI lane side by side with
// the AI backlog estimate, and the saved history with filters, per-language outcomes, a
// title link, Retry and paging.

const NOW_S = Math.floor(NOW / 1000);

const live: SubtitleJob[] = [
  { id: 40, kind: "episode", series_id: 9, season: 1, episode: 2, title: "Show - S01E02", state: "running", at: NOW_S, priority: 0, lane: "fast", stage: "extracting embedded subtitles", started_at: NOW_S },
  { id: 38, kind: "movie", movie_id: 3, title: "Alien", state: "running", at: NOW_S, priority: 2, lane: "gpu", stage: "AI transcribing (en)", progress: 40, started_at: NOW_S - 600, langs: ["en"] },
  { id: 39, kind: "movie", movie_id: 4, title: "Ran", state: "queued", at: NOW_S, priority: 2, lane: "gpu", langs: ["en"] },
];

function finished(id: number, state: SubtitleJob["state"], extra: Partial<SubtitleJob> = {}): SubtitleJob {
  return { id, kind: "movie", movie_id: id, title: `Film ${id}`, state, at: NOW_S - 3600, finished_at: NOW_S - 3500, priority: 2, lane: "gpu", ...extra };
}
const heat = finished(30, "failed", {
  movie_id: 1, title: "Heat", note: "en: no OpenSubtitles match → no whisper model installed",
  outcomes: [
    { lang: "en", rung: "opensubtitles", result: "no_match", detail: "no OpenSubtitles match" },
    { lang: "en", rung: "ai", result: "model_missing", detail: "no whisper model installed" },
  ],
});
const page1 = [heat, ...Array.from({ length: 29 }, (_, i) => finished(29 - i, i % 2 ? "done" : "skipped"))];

function routes(): MockRoute[] {
  return [
    {
      method: "GET", path: "/api/v1/subtitles/jobs", respond: ({ url }) => {
        const q = url.searchParams;
        if (!q.has("limit")) return { jobs: [...live, heat], gpu_backlog: 2, gpu_backlog_audio_sec: 211680, gpu_eta_sec: 50400, rtf: 4.2 };
        if (q.get("state") === "failed") return { jobs: [heat], next_before: 0 };
        if (q.get("before")) return { jobs: [finished(0, "cancelled", { title: "Oldest film", movie_id: 99 })], next_before: 0 };
        return { jobs: page1, next_before: 1 };
      },
    },
    { method: "POST", path: /^\/api\/v1\/subtitles\/library\/movies\/(\d+)$/, status: 202, respond: ({ params }) => ({ id: 50, kind: "movie", movie_id: Number(params[0]), title: "Heat", state: "queued", at: NOW_S, priority: 1 }) },
  ];
}

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

test.describe("Subtitles queue", () => {
  test.use({ persona: "admin" });

  test("lanes, AI backlog, history filters, outcomes, retry and paging", async ({ page, api }) => {
    await installExtra(page, api, routes());
    await page.goto("/subtitles?tab=queue");

    // Both lanes, each with its own jobs; the AI lane says how far behind it is.
    const backlog = "AI backlog: 2 files · ~14h 00m at 4.2× realtime";
    await expect(page.getByText(backlog)).toBeVisible();
    await expect(page.getByText("Extract & download")).toBeVisible();
    await expect(page.getByRole("link", { name: "Show - S01E02" })).toHaveAttribute("href", "/series/9");
    await expect(page.getByRole("link", { name: "Ran" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Clear AI backlog" })).toBeVisible();

    // History: the title opens the movie, Why lists each rung's outcome.
    await expect(page.getByRole("link", { name: "Heat" })).toHaveAttribute("href", "/movies/1");
    const heatRow = page.locator("div.flex-col", { has: page.getByRole("link", { name: "Heat" }) }).last();
    await heatRow.getByRole("button", { name: "Why" }).click();
    await expect(heatRow.getByText("no OpenSubtitles match", { exact: true })).toBeVisible();
    await expect(heatRow.getByText("no whisper model installed", { exact: true })).toBeVisible();

    // Filter to failed: one request with state=failed.
    await page.getByRole("button", { name: "Failed", exact: true }).click();
    await expect(page.getByText("Film 29")).toHaveCount(0);
    expect(api.calls.some((c) => c.method === "GET" && c.path.includes("state=failed"))).toBe(true);

    // Retry queues the file again at manual priority.
    await page.getByRole("button", { name: "Retry" }).click();
    await expect(page.getByText("Queued “Heat” again")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/subtitles/library/movies/1")).toHaveLength(1);

    // Back to all, then page further back.
    await page.getByRole("button", { name: "All", exact: true }).click();
    await page.getByRole("button", { name: "Older history" }).click();
    await expect(page.getByText("Oldest film")).toBeVisible();
    expect(api.calls.some((c) => c.path.includes("before=1"))).toBe(true);

    // The Overview's Working now card carries the same backlog line.
    await page.goto("/subtitles");
    await expect(page.getByText(backlog)).toBeVisible();
  });
});

test.describe("AI backlog with no download source", () => {
  test.use({ persona: "admin" });

  // A big AI backlog and no OpenSubtitles: the overview says why and points at the setup.
  test("the overview suggests OpenSubtitles", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/subtitles/jobs", body: { jobs: live, gpu_backlog: 839, gpu_backlog_audio_sec: 0, gpu_eta_sec: 0, rtf: 0 } },
    ]);
    await page.goto("/subtitles");
    const note = page.getByRole("note").filter({ hasText: "839 files are waiting for AI subtitles" });
    await expect(note).toBeVisible();
    await note.getByRole("button", { name: "Set up OpenSubtitles" }).click();
    await expect(page).toHaveURL(/\/settings/);
    await expect(page.getByText("OpenSubtitles (optional download source)")).toBeVisible();
    // The AI card has the hold switch, on by default.
    await expect(page.getByRole("switch", { name: "Pause while someone is watching" })).toBeChecked();
  });
});
