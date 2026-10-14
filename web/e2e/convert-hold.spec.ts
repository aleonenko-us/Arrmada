import { test, expect, type MockedApi } from "./mockApi";
import { convertHardware, convertSettings } from "./fixtures/modules";
import type { MockRoute } from "./fixtures/routes";
import type { ConvertHistoryEntry } from "../src/lib/api";
import { NOW } from "./fixtures/clock";

// CONV-08..11: converted originals wait in a hold so a conversion can be undone. The
// Overview shows freed and held space apart, History is the durable ledger with Revert and
// Release now on held rows, and Convert's settings have an Undo section.

const now = Math.floor(NOW / 1000);
const GB = 2 ** 30;

function row(id: number, over: Partial<ConvertHistoryEntry>): ConvertHistoryEntry {
  return {
    id, key: `movie:${id}`, kind: "movie", movie_id: id, season: 0, title: `Film ${id}`, outcome: "done", requested: false,
    src_size: 40 * GB, out_size: 12 * GB, src_spec: "H264 · 1920×1080 (1080p) · SDR", out_spec: "HEVC · 1920×1080 (1080p) · SDR",
    codec: "hevc", ssim_mean: 0.982, ssim_min: 0.971, ssim_windows: [0.98, 0.971], started_at: now - 7200, finished_at: now - id * 60,
    ...over,
  };
}

const held = row(1, { title: "The Two Towers", hold_state: "held", hold_until: now + 11 * 86400, hold_path: "/movies/.arrmada-hold/1/The Two Towers.mkv" });
const failed = row(2, { title: "Heat", outcome: "failed", note: "encode failed: exit status 1", src_spec: "", out_spec: "", codec: "", out_size: 0 });
const kept = row(3, { title: "Ran", outcome: "skipped", outcome_kind: "quality_gate", note: "couldn't reach the quality bar — kept the original", out_size: 0 });
const released = row(4, { title: "Alien", hold_state: "released" });

const hardwareWithHold = {
  ...convertHardware, reclaimed_bytes: 300 * GB,
  hold: { held_bytes: 480 * GB, held_saving_bytes: 300 * GB, held_count: 12, next_release_at: now + 3 * 86400, budget_bytes: 500 * GB, hold_days: 14 },
};

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("Space saved shows freed and held apart, and Recent reads the ledger", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/convert/hardware", body: hardwareWithHold },
      { method: "GET", path: "/api/v1/convert/history", body: { items: [held, failed], next: "" } },
    ]);
    await page.goto("/convert");
    await expect(page.getByText("300.0 GB", { exact: true })).toBeVisible();
    await expect(page.getByText("freed", { exact: true })).toBeVisible();
    await expect(page.getByText(/held so you can undo · 12 files · next release in 3 days/)).toBeVisible();
    await expect(page.getByRole("meter", { name: "Originals hold used" })).toHaveAttribute("aria-valuenow", "96");
    await expect(page.getByText("The Two Towers")).toBeVisible();
    await page.getByRole("button", { name: "See all in History →" }).click();
    await expect(page).toHaveURL(/tab=history/);
  });

  test("History lists every outcome, filters, pages without duplicates, and reverts a held row", async ({ page, api }) => {
    await installExtra(page, api, [
      {
        method: "GET", path: "/api/v1/convert/history",
        respond: ({ url }) => {
          if (url.searchParams.get("outcome") === "failed") return { items: [failed], next: "" };
          if (url.searchParams.get("before")) return { items: [kept, released], next: "" };
          return { items: [held, failed], next: `${failed.finished_at}:${failed.id}` };
        },
      },
      { method: "POST", path: /^\/api\/v1\/convert\/history\/(\d+)\/revert$/, respond: () => ({ ...held, hold_state: "reverted", reverted_at: now }) },
    ]);
    await page.goto("/convert?tab=history");
    const list = page.getByRole("tabpanel");
    await expect(list.getByText("The Two Towers")).toBeVisible();
    await expect(list.getByText("Heat")).toBeVisible();
    await expect(list.getByText("original held · 11 days left")).toBeVisible();

    await list.getByRole("button", { name: "Load more", exact: true }).click();
    await expect(list.getByText("Ran")).toBeVisible();
    await expect(list.getByText("Alien")).toBeVisible();
    await expect(list.getByText("The Two Towers")).toHaveCount(1);
    // Only the held row offers Revert and Release now.
    await expect(list.getByRole("button", { name: "Revert", exact: true })).toHaveCount(1);
    await expect(list.getByRole("button", { name: "Release now", exact: true })).toHaveCount(1);

    await list.getByRole("button", { name: "Revert", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("Puts back the original 40.0 GB H264 file, removes the 12.0 GB HEVC file");
    await dialog.getByRole("button", { name: "Revert", exact: true }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/convert/history/1/revert").length).toBe(1);
    await expect(list.getByText("reverted — the original is back")).toBeVisible();
    await expect(list.getByRole("button", { name: "Revert", exact: true })).toHaveCount(0);

    await list.getByRole("button", { name: "Failed", exact: true }).click();
    await expect.poll(() => api.callsTo("GET", "/api/v1/convert/history?outcome=failed").length).toBeGreaterThan(0);
    await expect(list.getByText("Heat")).toBeVisible();
    await expect(list.getByText("Ran")).toHaveCount(0);
  });

  test("Settings, Undo saves the hold's days and size, and warns at 0 days", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/convert/settings", respond: ({ body }) => ({ ...convertSettings, ...(body as object) }) },
    ]);
    await page.goto("/settings/convert");
    await expect(page.getByText("instant move ✓")).toBeVisible();
    const days = page.getByRole("textbox", { name: "Keep originals for (days)" });
    await days.fill("0");
    await days.press("Enter");
    await expect(page.getByText("Originals are deleted once the conversion is verified — there is no undo.")).toBeVisible();
    const size = page.getByRole("textbox", { name: "Hold up to (GB)" });
    await size.fill("800");
    await size.press("Enter");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/convert/settings").map((c) => c.body)).toEqual([{ hold_days: 0 }, { hold_max_gb: 800 }]);
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
