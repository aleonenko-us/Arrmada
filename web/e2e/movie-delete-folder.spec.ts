import { test, expect } from "./mockApi";
import type { Page } from "@playwright/test";
import type { MovieDeletePreview } from "../src/lib/api";
import * as media from "./fixtures/media";

// GH #59: deleting a movie with its files lists, before anything happens, the artwork and
// metadata that go with it and whether its folder is removed — or why the folder stays.

const preview = (over: Partial<MovieDeletePreview>): MovieDeletePreview => ({
  ...media.movieDeletePreview,
  versions: [{ id: 0, label: "Default", file_name: "Anchor Point (2019).mkv", size_bytes: 4e9 }],
  sidecars: 1,
  bytes: 4e9,
  ...over,
});

async function answerPreview(page: Page, body: MovieDeletePreview) {
  await page.route((u) => u.pathname === "/api/v1/movies/2/delete-preview", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }));
}

async function openDelete(page: Page) {
  await page.goto("/movies/2");
  await page.getByRole("button", { name: "More actions" }).click();
  await page.getByRole("menuitem", { name: "Delete movie…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Remove “Anchor Point”?")).toBeVisible();
  await dialog.getByRole("checkbox", { name: /Also delete files from disk/ }).check();
  return dialog;
}

test.describe("manager deleting a movie with its files", () => {
  test.use({ persona: "manager" });

  test("its own folder: the artwork, the metadata and the folder are listed", async ({ page, api }) => {
    await answerPreview(page, preview({
      extras: [
        { folder: "Anchor Point (2019)", name: "movie.nfo", path: "/media/movies/Anchor Point (2019)/movie.nfo", size_bytes: 2_000 },
        { folder: "Anchor Point (2019)", name: "poster.jpg", path: "/media/movies/Anchor Point (2019)/poster.jpg", size_bytes: 300_000 },
        { folder: "Anchor Point (2019)", name: "extrafanart/fanart1.jpg", path: "/media/movies/Anchor Point (2019)/extrafanart/fanart1.jpg", size_bytes: 500_000 },
      ],
      folders: [{ name: "Anchor Point (2019)", path: "/media/movies/Anchor Point (2019)", removed: true }],
    }));
    const dialog = await openDelete(page);
    await expect(dialog).toContainText("Anchor Point (2019).mkv and 1 subtitle.");
    await expect(dialog).toContainText("Also its artwork and metadata: movie.nfo, poster.jpg, extrafanart/fanart1.jpg.");
    await expect(dialog).toContainText("The folder “Anchor Point (2019)” is removed.");
    await expect(dialog).toContainText("recycle bin");
    const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
    expect(w.doc, "document width").toBeLessThanOrEqual(w.viewport);
    await dialog.getByRole("button", { name: "Remove + delete files" }).click();
    await expect.poll(() => api.callsTo("DELETE", "/api/v1/movies/2").length).toBe(1);
    const call = api.callsTo("DELETE", "/api/v1/movies/2")[0];
    expect(call.path).toContain("delete_files=true");
    // Exactly the leftovers it listed go back with the delete: nothing else can go.
    expect(call.body).toEqual({ leftovers: [
      "/media/movies/Anchor Point (2019)/movie.nfo",
      "/media/movies/Anchor Point (2019)/poster.jpg",
      "/media/movies/Anchor Point (2019)/extrafanart/fanart1.jpg",
    ] });
  });

  test("versions in two folders: each leftover is named with its folder", async ({ page }) => {
    await answerPreview(page, preview({
      extras: [
        { folder: "Anchor Point (2019)", name: "poster.jpg", path: "/media/movies/Anchor Point (2019)/poster.jpg", size_bytes: 1 },
        { folder: "Anchor Point (2019) 4K", name: "poster.jpg", path: "/media/movies/Anchor Point (2019) 4K/poster.jpg", size_bytes: 1 },
      ],
      folders: [
        { name: "Anchor Point (2019)", path: "/media/movies/Anchor Point (2019)", removed: true },
        { name: "Anchor Point (2019) 4K", path: "/media/movies/Anchor Point (2019) 4K", removed: true },
      ],
    }));
    const dialog = await openDelete(page);
    await expect(dialog).toContainText("Also its artwork and metadata: Anchor Point (2019)/poster.jpg, Anchor Point (2019) 4K/poster.jpg.");
  });

  test("a shared folder: nothing but the movie's files, and the dialog says why the folder stays", async ({ page, api }) => {
    await answerPreview(page, preview({
      extras: [],
      folders: [{ name: "Marvel", path: "/media/movies/Marvel", removed: false, kept: "another video is inside it (Iron Man 2 (2010)/Iron Man 2 (2010).mkv)" }],
    }));
    const dialog = await openDelete(page);
    await expect(dialog).toContainText("Anchor Point (2019).mkv and 1 subtitle.");
    await expect(dialog).not.toContainText("artwork and metadata:");
    await expect(dialog).toContainText("The folder “Marvel” stays: another video is inside it");
    await dialog.getByRole("button", { name: "Remove + delete files" }).click();
    await expect.poll(() => api.callsTo("DELETE", "/api/v1/movies/2").length).toBe(1);
    expect(api.callsTo("DELETE", "/api/v1/movies/2")[0].body ?? null).toBeNull(); // no leftovers to send
  });
});
