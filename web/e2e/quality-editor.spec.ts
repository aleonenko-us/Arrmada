import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { StoredProfile } from "../src/lib/api";

// FE-25: the Quality editor has its own address (/quality/:media/new, /:key, /:key/copy),
// a refresh reopens it, a deleted profile's address says so, and the unsaved-edits guard
// asks before a sidebar link or Back throws a half-built profile away — but not after Save.

const hd: StoredProfile = {
  id: 7, media_type: "movie", name: "HD 1080p", allowed_resolutions: ["1080p"], min_source: "WEB-DL", max_source: "",
  bitrate_cap_mbps: 0, small_bias: 0, min_format_score: 0, format_scores: {}, min_seeders: 1, stall_minutes: 0,
  upgrades_enabled: true, upgrade_min_percent: 0, ideal: {},
};

// The editor's live calls, answered here; everything else falls through to the fixtures.
async function mockEditor(page: Page, api: MockedApi) {
  await page.route((u) => u.pathname.startsWith("/api/v1/quality/") || u.pathname === "/api/v1/library/fit/preview", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    let body: unknown;
    try { body = req.postDataJSON(); } catch { body = undefined; }
    const json = (b: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(b) });
    const call = () => api.calls.push({ method: req.method(), path: url.pathname, body });
    switch (`${req.method()} ${url.pathname}`) {
      case "GET /api/v1/quality/profiles/hd-1080p":
        call();
        return json(hd);
      case "GET /api/v1/quality/profiles/gone":
        return json({ message: "quality profile not found" }, 404);
      case "GET /api/v1/quality/profiles/uhd-2160p":
        return json({ message: "database is locked" }, 500);
      case "POST /api/v1/quality/preview":
        return json({ profile: "", decision: { winner: null, eligible: [], rejected: [] } });
      case "POST /api/v1/library/fit/preview":
        return json({ scope: "library", counts: { titles: 0, files: 0, fits: 0, over: 0, under: 0, mismatch: 0 } });
      case "POST /api/v1/quality/impact":
        return json({ replace: { files: 0, bytes: 0, examples: [] }, search: { files: 0, bytes: 0, examples: [] } });
      case "PUT /api/v1/quality/profiles/7":
      case "POST /api/v1/quality/profiles":
        call();
        return json({ status: "ok" });
    }
    return route.fallback();
  });
}

const nameBox = (page: Page) => page.getByRole("textbox", { name: "Name", exact: true });

test.describe("Quality editor", () => {
  test.use({ persona: "admin" });

  test("Edit has its own address; a sidebar link and Back ask before discarding edits", async ({ page, api }) => {
    await mockEditor(page, api);
    await page.goto("/quality");
    await page.getByRole("button", { name: "Edit", exact: true }).first().click();
    await expect(page).toHaveURL(/\/quality\/movie\/hd-1080p$/);
    await expect(nameBox(page)).toHaveValue("HD 1080p");
    await nameBox(page).fill("HD 1080p — tuned");

    // A sidebar link asks; Keep editing keeps every change.
    await page.getByRole("complementary").getByRole("link", { name: "Movies" }).click();
    const ask = page.getByRole("dialog", { name: "Discard your changes to this profile?" });
    await expect(ask).toBeVisible();
    await ask.getByRole("button", { name: "Keep editing" }).click();
    await expect(ask).toHaveCount(0);
    await expect(page).toHaveURL(/\/quality\/movie\/hd-1080p$/);
    await expect(nameBox(page)).toHaveValue("HD 1080p — tuned");

    // Back asks the same way; Discard leaves.
    await page.goBack();
    await expect(ask).toBeVisible();
    await ask.getByRole("button", { name: "Discard" }).click();
    await expect(page).toHaveURL(/\/quality$/);
    expect(api.callsTo("PUT", "/api/v1/quality/profiles/")).toEqual([]);
  });

  test("Save goes back to the list without asking", async ({ page, api }) => {
    await mockEditor(page, api);
    await page.goto("/quality/movie/hd-1080p");
    await nameBox(page).fill("HD 1080p — tuned");
    await page.getByRole("button", { name: "Save profile" }).click();
    await expect(page).toHaveURL(/\/quality\?media=movie$/);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(page.getByText("Saved HD 1080p — tuned.")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/quality/profiles/7").map((c) => (c.body as StoredProfile).name)).toEqual(["HD 1080p — tuned"]);
  });

  test("a refresh reopens the profile; a deleted profile's address shows an error", async ({ page, api }) => {
    await mockEditor(page, api);
    await page.goto("/quality/movie/hd-1080p");
    await expect(nameBox(page)).toHaveValue("HD 1080p");
    await page.reload();
    await expect(nameBox(page)).toHaveValue("HD 1080p");

    await page.goto("/quality/movie/gone");
    await expect(page.getByRole("alert")).toContainText("Couldn’t load this quality profile");
    await expect(page.getByRole("alert")).toContainText("quality profile not found");
  });

  test("New: the picker's template opens at /quality/movie/new; a cold visit picks again", async ({ page, api }) => {
    await mockEditor(page, api);
    await page.goto("/quality");
    await page.getByRole("button", { name: "+ New profile" }).click();
    await page.getByRole("dialog", { name: "New movie profile" }).getByRole("button", { name: /Compact/ }).click();
    await expect(page).toHaveURL(/\/quality\/movie\/new$/);
    await expect(page.getByRole("heading", { name: "New profile" })).toBeVisible();
    // The template rides in the history entry, so a reload keeps it.
    await page.reload();
    await expect(page.getByRole("heading", { name: "New profile" })).toBeVisible();

    // A cold visit (a bookmark: a new page load with no history state) has no template.
    await page.goto("/");
    await page.goto("/quality/movie/new");
    await expect(page).toHaveURL(/\/quality\?media=movie$/);
    await expect(page.getByRole("dialog", { name: "New movie profile" })).toBeVisible();
  });

  test("book and music profiles open their own builders at their own addresses", async ({ page, api }) => {
    await mockEditor(page, api);
    for (const [media, heading] of [["book", "New book profile"], ["music", "New music profile"]] as const) {
      await page.goto(`/quality?media=${media}`);
      await page.getByRole("button", { name: "+ New profile" }).click();
      await expect(page).toHaveURL(new RegExp(`/quality/${media}/new$`));
      await expect(page.getByRole("heading", { name: heading })).toBeVisible();
      // Nothing typed, so Cancel just leaves.
      await page.getByRole("button", { name: "Cancel" }).click();
      await expect(page).toHaveURL(new RegExp(`/quality\\?media=${media}$`));
      await expect(page.getByRole("dialog")).toHaveCount(0);
    }
  });

  test("the guard covers other forms with a Save: a typed mail login asks before leaving Settings", async ({ page }) => {
    await page.goto("/settings/alerts#outgoing-email");
    await page.getByLabel("Username", { exact: true }).fill("me@example.com");
    await page.getByRole("complementary").first().getByRole("link", { name: "Movies" }).click();
    const ask = page.getByRole("dialog", { name: "Discard your unsaved changes?" });
    await ask.getByRole("button", { name: "Keep editing" }).click();
    await expect(page).toHaveURL(/\/settings\/alerts/);
    await expect(page.getByLabel("Username", { exact: true })).toHaveValue("me@example.com");
    await page.getByRole("complementary").first().getByRole("link", { name: "Movies" }).click();
    await ask.getByRole("button", { name: "Discard" }).click();
    await expect(page).toHaveURL(/\/movies$/);
  });

  test("Duplicate opens a copy; a failed one says so in a toast", async ({ page, api }) => {
    await mockEditor(page, api);
    await page.goto("/quality");
    await page.getByRole("button", { name: "Duplicate" }).first().click();
    await expect(page).toHaveURL(/\/quality\/movie\/hd-1080p\/copy$/);
    await expect(nameBox(page)).toHaveValue("HD 1080p (copy)");
    await expect(page.getByRole("heading", { name: "New profile" })).toBeVisible();

    await page.goto("/quality");
    await page.getByRole("button", { name: "Duplicate" }).nth(1).click();
    await expect(page.getByText(/Couldn't duplicate Ultra HD: database is locked/)).toBeVisible();
    await expect(page).toHaveURL(/\/quality$/);
  });
});
