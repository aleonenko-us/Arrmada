import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";

// BOOK-12: Books → Possible duplicates. The header says how many groups there are; the
// review shows the rows side by side, merges only after a confirm and lists what was
// left on disk, and "Not the same" makes a pair go away for good. At phone and desktop
// width, with nothing scrolling sideways.

const book = (id: number, key: string, extra: Record<string, unknown> = {}) => ({
  id, title: "The Long Quiet", author: "Mara Ellis", key, source: key.startsWith("hc:") ? "hardcover" : "openlibrary",
  audio_versions: 0, monitored: true, added_at: "2026-09-01 10:00:00", ...extra,
});
const group = {
  books: [
    book(7, "OL7W", { ebook: { path: "/books/Mara Ellis/The Long Quiet.epub", format: "EPUB", size_bytes: 2_000_000, file_count: 1 } }),
    book(9, "hc:9", {
      ebook: { path: "/books/Mara Ellis/The Long Quiet (1).epub", format: "EPUB", size_bytes: 2_100_000, file_count: 1 },
      audiobook: { path: "/audiobooks/Mara Ellis/The Long Quiet", format: "MP3", size_bytes: 400_000_000, file_count: 12 },
    }),
  ],
  suggested_keep_id: 9,
  reasons: ["Same title and author"],
};

async function mockDupes(page: Page) {
  const state = { groups: [group] as unknown[], merges: [] as unknown[], ignores: [] as unknown[] };
  await page.route((u) => u.pathname === "/api/v1/books/duplicates", (route: Route) =>
    route.fulfill({ json: { groups: state.groups, past_merges: [] } }));
  await page.route((u) => u.pathname === "/api/v1/books/duplicates/merge", (route: Route) => {
    state.merges.push(route.request().postDataJSON());
    state.groups = [];
    return route.fulfill({ json: { keep_id: 9, left_files: ["/books/Mara Ellis/The Long Quiet.epub"], results: [
      { drop_id: 7, drop_title: "The Long Quiet", moved_ebook: false, moved_audiobook: false, versions: 0, left_files: ["/books/Mara Ellis/The Long Quiet.epub"], moved: {} },
    ] } });
  });
  await page.route((u) => u.pathname === "/api/v1/books/duplicates/ignore", (route: Route) => {
    state.ignores.push(route.request().postDataJSON());
    state.groups = [];
    return route.fulfill({ status: 204 });
  });
  return state;
}

const sideways = (page: Page) => page.locator("main").evaluate((el) => el.scrollWidth - el.clientWidth);

for (const size of [{ width: 375, height: 812 }, { width: 1440, height: 900 }]) {
  test.describe(`possible duplicates at ${size.width}px`, () => {
    test.use({ persona: "admin" });

    test("merges into the kept book after a confirm and lists what stayed on disk", async ({ page, api }) => {
      await page.setViewportSize(size);
      const state = await mockDupes(page);
      await page.goto("/books");
      await page.getByRole("button", { name: "Possible duplicates (1)" }).click();
      const dialog = page.getByRole("dialog", { name: "Possible duplicates" });
      await expect(dialog.getByText("No files are deleted.", { exact: false })).toBeVisible();
      await expect(dialog.getByRole("radio", { name: "Keep “The Long Quiet”" }).nth(1)).toBeChecked();
      expect(await sideways(page)).toBeLessThanOrEqual(0);

      await dialog.getByRole("button", { name: "Merge into the kept book" }).click();
      await page.getByRole("button", { name: "Cancel" }).click();
      expect(state.merges).toHaveLength(0);

      await dialog.getByRole("button", { name: "Merge into the kept book" }).click();
      await page.getByRole("button", { name: "Merge", exact: true }).click();
      await expect(dialog.getByText("/books/Mara Ellis/The Long Quiet.epub")).toBeVisible();
      await expect(dialog.getByText("No possible duplicates")).toBeVisible();
      expect(state.merges).toEqual([{ keep_id: 9, drop_ids: [7] }]);
      await api.quiet();
    });

    test("Not the same hides the pair, and it stays gone on reload", async ({ page, api }) => {
      await page.setViewportSize(size);
      const state = await mockDupes(page);
      await page.goto("/books");
      await page.getByRole("button", { name: "Possible duplicates (1)" }).click();
      const dialog = page.getByRole("dialog", { name: "Possible duplicates" });
      await dialog.getByRole("button", { name: "Not the same" }).click();
      await expect(dialog.getByText("Mara Ellis", { exact: false })).toHaveCount(0);
      expect(state.ignores).toEqual([{ ids: [7, 9] }]);

      await page.reload();
      await api.quiet();
      await expect(page.getByRole("button", { name: /^Possible duplicates \(/ })).toHaveCount(0);
    });
  });
}
