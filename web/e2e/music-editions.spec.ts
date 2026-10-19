import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import type { MusicAlbum } from "../src/lib/api";
import type { AlbumEditionView } from "../src/lib/musicApi";

// MUS-30: an album lists its editions, and the picker changes its target (the album's
// files move with it; nothing on disk changes).

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const album: MusicAlbum = {
  id: 51, artist_id: 5, mbid: "rg-51", title: "Low Tide", year: 2019, album_type: "Album", monitored: true,
  track_count: 2, have_tracks: 2, size_bytes: 90_000_000, edition_tracks: 2, target_edition_id: 7,
  editions: [
    { id: 7, album_id: 51, title: "Low Tide", track_count: 2, disc_count: 1, source: "musicbrainz", target: true, release_date: "2019-03-01", country: "GB", media_summary: "CD" },
    { id: 8, album_id: 51, title: "Low Tide", track_count: 2, disc_count: 1, source: "musicbrainz", release_date: "2019-03-05", country: "US", media_summary: "CD" },
  ],
  tracks: [
    { id: 1, album_id: 51, disc_number: 1, track_number: 1, title: "Undertow", monitored: true, has_file: true, format: "FLAC" },
    { id: 2, album_id: 51, disc_number: 1, track_number: 2, title: "Breakwater", monitored: true, has_file: true, format: "FLAC" },
  ],
};

const editions: AlbumEditionView[] = [
  { id: 7, title: "Low Tide", track_count: 2, tracks_fetched: true, target: true, country: "GB", release_date: "2019-03-01", media_summary: "CD", words: "GB CD 2019 · 2 tracks", acceptable: true, files: 2 },
  { id: 8, title: "Low Tide", track_count: 2, tracks_fetched: true, country: "US", release_date: "2019-03-05", media_summary: "CD", words: "US CD 2019 · 2 tracks", acceptable: true, same_as_target: true, files: 0 },
  { id: 9, title: "Low Tide", track_count: 2, tracks_fetched: false, status: "Bootleg", words: "US CD 2018 · 2 tracks", acceptable: false, files: 0 },
];

async function mockAlbum(page: Page) {
  const puts: unknown[] = [];
  await page.route((u) => u.pathname === "/api/v1/music/albums/51", (route) => json(route, album));
  await page.route((u) => u.pathname === "/api/v1/music/albums/51/editions", (route) => json(route, { editions, pinned: false }));
  await page.route((u) => u.pathname === "/api/v1/music/albums/51/edition", (route) => {
    puts.push(route.request().postDataJSON());
    return json(route, { moved: 2, kept: 0, pinned: false });
  });
  return puts;
}

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("the album page changes its edition", async ({ page }) => {
    const puts = await mockAlbum(page);
    await page.goto("/music/album/51");
    await page.getByRole("button", { name: "Change edition…" }).click();
    const dialog = page.getByRole("dialog", { name: "Edition" });
    await expect(dialog.getByText("same tracks as the current edition")).toBeVisible();
    // The bootleg waits behind "Show more".
    await expect(dialog.getByText("Bootleg", { exact: true })).toHaveCount(0);
    await dialog.getByRole("button", { name: /Show 1 more/ }).click();
    await expect(dialog.getByText("Bootleg", { exact: true })).toBeVisible();
    await dialog.getByRole("listitem").filter({ hasText: "US CD 2019 · 2 tracks" }).first().getByRole("button", { name: "Use this" }).click();
    await expect.poll(() => puts).toEqual([{ release_id: 8, pinned: false }]);
    await expect(page.getByText(/Switched to US CD 2019/)).toBeVisible();
  });
});
