import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { settings } from "./fixtures/system";
import type { MockRoute } from "./fixtures/routes";

// Issue #25: audiobooks and ebooks get their own folder and file naming, like movies and
// TV — with a live preview on sample books, presets, and a rename for the books already in
// the library (a dry run first, then a background job).

// override answers the given endpoints before the default fixtures, recording the calls.
async function override(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method() || url.pathname !== r.path) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      const out = r.respond ? r.respond({ url, params: [], body }) : r.body;
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out ?? {}) });
    }
    return route.fallback();
  });
}

const putSettings: MockRoute = { method: "PUT", path: "/api/v1/settings", respond: ({ body }) => ({ ...settings, ...(body as object) }) };

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("the preview follows the template as it's typed, and the template saves on Enter", async ({ page, api }) => {
    await override(page, api, [putSettings]);
    await page.goto("/settings/media#book-naming");
    const card = page.locator("#book-naming");
    const examples = card.getByRole("group", { name: "Audiobook examples" });
    await expect(examples.getByText("Pierce Brown/Golden Son/Golden Son.m4b")).toBeVisible();

    const folder = card.getByRole("textbox", { name: "Audiobook folder" });
    await folder.fill("{Author}/{Series}/{Series Position} - {Title}");
    await expect(examples.getByText("Pierce Brown/Red Rising Saga/2 - Golden Son/Golden Son.m4b")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0); // previewing saves nothing

    await folder.press("Enter");
    await expect(card.getByText("Saved ✓")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([
      { naming_audiobook_folder: "{Author}/{Series}/{Series Position} - {Title}" },
    ]);
  });

  test("a folder template with no title is explained before it's saved", async ({ page, api }) => {
    await page.goto("/settings/media#book-naming");
    const card = page.locator("#book-naming");
    await card.getByRole("textbox", { name: "Ebook folder" }).fill("{Author}");
    await expect(card.getByText(/Folder: the last folder must have \{Title\} in it/)).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0);
  });

  test("a preset fills every book template after asking", async ({ page, api }) => {
    await override(page, api, [putSettings]);
    await page.goto("/settings/media#book-naming");
    const card = page.locator("#book-naming");
    await expect(card.getByRole("button", { name: "Using Author/Title (default)" })).toBeDisabled();
    await card.getByRole("button", { name: "Audiobookshelf (Author/Series/Title)" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Use it" }).click();
    await expect(page.getByText("Book naming set to Audiobookshelf (Author/Series/Title).")).toBeVisible();
    const sent = api.callsTo("PUT", "/api/v1/settings").map((c) => c.body);
    expect(sent).toContainEqual({ naming_audiobook_folder: "{Author}/{Series}/{Series Position} - {Title}" });
    expect(sent).toContainEqual({ naming_ebook_folder: "{Author}/{Series}/{Series Position} - {Title}" });
    await expect(card.getByRole("textbox", { name: "Audiobook folder" })).toHaveValue("{Author}/{Series}/{Series Position} - {Title}");
  });

  test("the book rename lists what would move, then runs as a job", async ({ page, api }) => {
    await override(page, api, [
      { method: "GET", path: "/api/v1/books/organize", body: {
        counts: { rename: 1, same: 4, conflict: 1, missing: 0 }, total: 2, offset: 0, token: "plan-1",
        items: [
          { media: "book", book_id: 3, version_id: 0, kind: "audiobook", title: "Golden Son", current: "/media/audiobooks/Pierce Brown/Golden Son", proposed: "/media/audiobooks/Pierce Brown/Red Rising Saga/2 - Golden Son", file_count: 12, status: "ok" },
          { media: "book", book_id: 4, version_id: 0, kind: "ebook", title: "Morning Star", current: "/media/ebooks/Pierce Brown/Morning Star/Morning Star.epub", proposed: "/media/ebooks/Pierce Brown/Red Rising Saga/3 - Morning Star/Morning Star.epub", file_count: 1, status: "conflict", reason: "a different file already exists there" },
        ],
      } },
      { method: "POST", path: "/api/v1/books/organize", status: 202, body: { status: "organizing", total: 1, job_id: 4401 } },
      { method: "GET", path: "/api/v1/jobs/4401", body: {
        id: 4401, kind: "book.organize", target: "books", trigger: "user:2", status: "succeeded", progress: 1,
        message: "Renamed 1 edition", error: "", result: { total: 1, renamed: 1, skipped: 0 }, created_at: null, started_at: null, finished_at: null,
      } },
    ]);
    await page.goto("/settings/media#book-naming");
    await page.locator("#book-naming").getByRole("button", { name: "Preview book rename…" }).click();
    const dialog = page.getByRole("dialog", { name: "Rename books" });
    await expect(dialog.getByText("1 edition to rename")).toBeVisible();
    await expect(dialog.getByText("Golden Son · Audiobook · 12 files")).toBeVisible();
    await expect(dialog.getByText("→ Pierce Brown/Red Rising Saga/2 - Golden Son")).toBeVisible();
    await expect(dialog.getByText("Won't be renamed: a different file already exists there")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/books/organize")).toHaveLength(0);

    await dialog.getByRole("button", { name: "Rename 1 edition" }).click();
    await page.getByRole("dialog", { name: "Rename 1 book edition?" }).getByRole("button", { name: "Rename" }).click();
    await expect(dialog.getByText("Renamed 1 edition")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/books/organize").map((c) => c.body)).toEqual([{ token: "plan-1" }]);
  });
});
