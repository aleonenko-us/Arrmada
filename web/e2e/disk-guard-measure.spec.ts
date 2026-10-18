import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { settings } from "./fixtures/system";

// "Measure a different drive" in the Download disk guard card (Settings → Downloads). On
// Unraid the downloads folder is a user share, which reads as the whole array; the cache
// pool mapped in at /cache is what fills. Pick it with the folder picker, see the guard
// read it, and see a clear warning when it can't be read any more.

const TB = 1024 ** 4;
const GB = 1024 ** 3;

type Route = { method: string; path: string; status?: number; respond: (body: unknown, url: URL) => unknown };

// answer serves the given endpoints before the default fixtures, recording the calls.
async function answer(page: Page, api: MockedApi, routes: Route[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of routes) {
      if (r.method !== req.method() || url.pathname !== r.path) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(r.respond(body, url) ?? {}) });
    }
    return route.fallback();
  });
}

const onArray = {
  enabled: true, measurable: true, path: "/storage/downloads", used_pct: 69.2, pause_pct: 85, resume_pct: 80, holding: 0,
  measure_path: "", measured_path: "/storage/downloads", override: false, measure_unreadable: false,
  total_bytes: 40 * TB, free_bytes: 12 * TB,
  shared_with: [{ role: "movies", label: "Movies", path: "/storage/media/movies" }, { role: "tv", label: "TV", path: "/storage/media/tv" }],
  shared_with_library: true,
};
const onPool = {
  ...onArray, used_pct: 91, measure_path: "/cache", measured_path: "/cache", override: true,
  total_bytes: TB, free_bytes: 92 * GB, holding: 2, shared_with: [], shared_with_library: false,
};
const poolGone = {
  ...onArray, measurable: false, used_pct: 0, measure_path: "/cache", measured_path: "/cache", override: true,
  measure_unreadable: true, total_bytes: 0, free_bytes: 0, holding: 2, shared_with: [], shared_with_library: false,
};

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("picks the cache pool with the folder picker and watches it instead of the array", async ({ page, api }) => {
    let saved = "";
    await answer(page, api, [
      { method: "GET", path: "/api/v1/downloads/disk-guard", respond: () => (saved ? onPool : onArray) },
      { method: "GET", path: "/api/v1/system/browse", respond: (_b, url) => (url.searchParams.get("path") === "/cache"
        ? { path: "/cache", parent: "/", dirs: [], path_disabled: false }
        : { path: "/", parent: "/", dirs: [{ name: "cache", path: "/cache" }, { name: "storage", path: "/storage" }], path_disabled: true }) },
      { method: "PUT", path: "/api/v1/settings", respond: (b) => {
        saved = (b as { downloads_disk_guard_measure_path?: string }).downloads_disk_guard_measure_path ?? saved;
        return { ...settings, downloads_disk_guard_measure_path: saved };
      } },
    ]);
    await page.goto("/settings/downloads#disk-guard");
    const card = page.locator("#disk-guard");

    // Measuring the share: the array's number, the libraries on the same drive, and the
    // amber note pointing at the new option.
    await expect(card.getByText("69.2% full · 40 TB, 12 TB free")).toBeVisible();
    await expect(card.getByText("Downloads folder", { exact: true })).toBeVisible();
    await expect(card.getByText(/Shares a drive with: Movies, TV/)).toBeVisible();
    await expect(card.getByText(/Point the guard at the right drive/)).toBeVisible();
    await expect(card.getByText(/Container Path/)).toBeVisible();
    await expect(card.getByText("- /mnt/cache:/cache:ro")).toBeVisible();

    await card.getByRole("button", { name: "Browse…" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "cache" }).click();
    await page.getByRole("button", { name: "Select this folder" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/settings")[0].body).toEqual({ downloads_disk_guard_measure_path: "/cache" });

    // Now reading the pool: its size and free space, marked as the different drive, and
    // no "shares a drive" warning (the pool is its own filesystem).
    await expect(card.getByText("91.0% full · 1.0 TB, 92 GB free")).toBeVisible();
    await expect(card.getByText("Different drive", { exact: true })).toBeVisible();
    await expect(card.getByText(/Measured instead of the downloads folder/)).toBeVisible();
    await expect(card.getByText(/Shares a drive with/)).toHaveCount(0);
    await expect(card.getByText(/Point the guard at the right drive/)).toHaveCount(0);
    await expect(card.getByRole("textbox", { name: "Measure a different drive (optional)" })).toHaveValue("/cache");
  });

  test("a typed path is checked: relative refused before sending, the server's reason shown", async ({ page, api }) => {
    await answer(page, api, [
      { method: "GET", path: "/api/v1/downloads/disk-guard", respond: () => onArray },
      { method: "PUT", path: "/api/v1/settings", status: 400, respond: () => ({ status: "error", message: "/storage/other is on the same drive as the downloads folder (/storage/downloads) — that's already what the guard measures" }) },
    ]);
    await page.goto("/settings/downloads#disk-guard");
    const field = page.getByRole("textbox", { name: "Measure a different drive (optional)" });
    await field.fill("cache");
    await field.press("Enter");
    await expect(page.getByText("Enter a full path starting with /, like /cache.")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0);

    await field.fill("/storage/other");
    await page.locator("#disk-guard").getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("alert").filter({ hasText: "that's already what the guard measures" })).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([{ downloads_disk_guard_measure_path: "/storage/other" }]);
  });

  test("a saved drive that can't be read is a clear warning, and can be cleared", async ({ page, api }) => {
    let saved = "/cache";
    await answer(page, api, [
      { method: "GET", path: "/api/v1/settings", respond: () => ({ ...settings, downloads_disk_guard_measure_path: saved }) },
      { method: "GET", path: "/api/v1/downloads/disk-guard", respond: () => (saved ? poolGone : onArray) },
      { method: "PUT", path: "/api/v1/settings", respond: (b) => {
        saved = (b as { downloads_disk_guard_measure_path: string }).downloads_disk_guard_measure_path;
        return { ...settings, downloads_disk_guard_measure_path: saved };
      } },
    ]);
    await page.goto("/settings/downloads#disk-guard");
    const card = page.locator("#disk-guard");
    const warning = card.getByRole("alert").filter({ hasText: /can't read the drive at \/cache any more/ });
    await expect(warning).toBeVisible();
    await expect(warning).toContainText("doesn't fall back to the downloads folder");
    await expect(warning).toContainText("isn't pausing or resuming downloads until it can be read");
    await expect(warning).toContainText("resume it by hand, or turn the guard off to release it");
    await expect(card.getByText(/\d% full/)).toHaveCount(0);

    await card.getByRole("button", { name: "Clear" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/settings")[0].body).toEqual({ downloads_disk_guard_measure_path: "" });
    await expect(warning).toHaveCount(0);
    await expect(card.getByText("69.2% full · 40 TB, 12 TB free")).toBeVisible();
  });

  test("a saved drive that turned into a RAM folder says so, and other drives are still guarded", async ({ page, api }) => {
    await answer(page, api, [
      { method: "GET", path: "/api/v1/settings", respond: () => ({ ...settings, downloads_disk_guard_measure_path: "/cache" }) },
      { method: "GET", path: "/api/v1/downloads/disk-guard", respond: () => ({
        ...poolGone, measurable: true, used_pct: 50, measure_not_disk: "tmpfs", measured_path: "/mnt/disk3/dl", total_bytes: 8 * TB, free_bytes: 4 * TB,
        volumes: [
          { path: "/storage/downloads", measurable: false, used_pct: 0, measured_path: "/cache", override: true, total_bytes: 0, free_bytes: 0 },
          { path: "/mnt/disk3/dl", measurable: true, used_pct: 50, measured_path: "/mnt/disk3/dl", override: false, total_bytes: 8 * TB, free_bytes: 4 * TB },
        ],
      }) },
    ]);
    await page.goto("/settings/downloads#disk-guard");
    const warning = page.locator("#disk-guard").getByRole("alert");
    await expect(warning).toContainText("reads as a RAM or container filesystem (tmpfs), not a disk");
    await expect(warning).toContainText("Host Path doesn't match your pool's name");
    await expect(warning).toContainText("downloads folders on other drives are still guarded");
    await expect(page.locator("#disk-guard").getByText("can't be read")).toBeVisible();
  });

  test("on a ZFS pool the card points at the free-space limit, which saves as whole GB", async ({ page, api }) => {
    await answer(page, api, [
      { method: "GET", path: "/api/v1/settings", respond: () => ({ ...settings, downloads_disk_guard_measure_path: "/cache" }) },
      { method: "GET", path: "/api/v1/downloads/disk-guard", respond: () => ({ ...onPool, used_pct: 2.1, zfs: true, holding: 0 }) },
      { method: "PUT", path: "/api/v1/settings", respond: (b) => ({ ...settings, ...(b as object) }) },
    ]);
    await page.goto("/settings/downloads#disk-guard");
    const card = page.locator("#disk-guard");
    await expect(card.getByText("This is a ZFS pool: the percentage only counts part of it — use the free-space limit below.")).toBeVisible();
    const floor = card.getByRole("textbox", { name: "Also pause when free space drops below (GB)" });
    await expect(floor).toHaveValue("0");
    await floor.fill("150");
    await floor.press("Enter");
    await expect(card.getByText("Saved ✓")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([{ downloads_disk_guard_pause_free_gb: "150" }]);
  });
});
