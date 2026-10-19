import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import { settings } from "./fixtures/system";
import type { MusicAlbum } from "../src/lib/api";
import type { MusicRenameItem } from "../src/lib/musicNamingApi";

// MUS-33/34/36: Settings → Naming & metadata has a Music naming & tags card (house tags
// on/off, the templates with a live preview, the extras allowlist), and an album page can
// preview a rename and apply exactly what it showed, or write the house tags after asking.

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

async function trackSettings(page: Page) {
  const puts: unknown[] = [];
  await page.route((u) => u.pathname === "/api/v1/settings", (route) => {
    if (route.request().method() !== "PUT") return route.fallback();
    const body = route.request().postDataJSON();
    puts.push(body);
    return json(route, { ...settings, ...body });
  });
  return puts;
}

const album: MusicAlbum = {
  id: 61, artist_id: 5, mbid: "rg-61", title: "Low Tide", year: 2019, album_type: "Album", monitored: true,
  track_count: 2, have_tracks: 2, size_bytes: 60_000_000, edition_tracks: 2, artist_credit: "The Quiet Harbours", target_edition_id: 7,
  editions: [{ id: 7, album_id: 61, title: "Low Tide", track_count: 2, disc_count: 1, source: "musicbrainz", target: true }],
  discs: [{ id: 1, position: 1, track_count: 2 }],
  tracks: [
    { id: 1, album_id: 61, disc_number: 1, track_number: 1, title: "Undertow", monitored: true, has_file: true, format: "FLAC" },
    { id: 2, album_id: 61, disc_number: 1, track_number: 2, title: "Breakwater", monitored: true, has_file: true, format: "FLAC" },
  ],
};

const moves: MusicRenameItem[] = [
  { album_id: 61, track_id: 1, from: "/music/The Quiet Harbours/Low Tide/01 - Undertow.flac", to: "/music/The Quiet Harbours/Low Tide (2019)/01 - Undertow.flac" },
  { album_id: 61, from: "/music/The Quiet Harbours/Low Tide/booklet.pdf", to: "/music/The Quiet Harbours/Low Tide (2019)/booklet.pdf" },
  { album_id: 61, track_id: 2, from: "/music/The Quiet Harbours/Low Tide/02 - Breakwater.flac", to: "/music/The Quiet Harbours/Low Tide (2019)/02 - Breakwater.flac", conflict: "a different file already exists there" },
];

async function mockAlbum(page: Page) {
  const state = { renames: [] as unknown[], retags: 0 };
  await page.route((u) => u.pathname === "/api/v1/music/albums/61", (route) => json(route, album));
  await page.route((u) => u.pathname === "/api/v1/music/albums/61/rename", (route) => {
    if (route.request().method() === "POST") {
      state.renames.push(route.request().postDataJSON());
      return json(route, { renamed: 2, skipped: [] });
    }
    return json(route, { items: moves });
  });
  await page.route((u) => u.pathname === "/api/v1/music/albums/61/retag", (route) => {
    state.retags++;
    return json(route, { status: "retagging", job_id: 777, existing: false }, 202);
  });
  await page.route((u) => u.pathname === "/api/v1/jobs/777", (route) =>
    json(route, { id: 777, kind: "music.retag", target: "61", status: "succeeded", progress: 1, message: "Retagged 2 file(s) · 0 skipped · 0 failed" }));
  return state;
}

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("the music preview follows the template, and it saves on Enter", async ({ page }) => {
    const puts = await trackSettings(page);
    await page.goto("/settings/media#music-naming");
    const card = page.locator("#music-naming");
    const examples = card.getByRole("group", { name: "Music examples" });
    await expect(examples.getByText("Radiohead/OK Computer (1997)/01 - Airbag.flac")).toBeVisible();

    const track = card.getByRole("textbox", { name: "Track file", exact: true });
    await track.fill("Disc {Disc}/{Track:00} - {Track Title}");
    await expect(examples.getByText("Radiohead/OK Computer (1997)/Disc 1/01 - Airbag.flac")).toBeVisible();
    expect(puts).toHaveLength(0); // previewing saves nothing
    await track.press("Enter");
    await expect.poll(() => puts).toEqual([{ naming_music_track: "Disc {Disc}/{Track:00} - {Track Title}" }]);
  });

  test("a folder with no album title is explained before it's saved", async ({ page }) => {
    const puts = await trackSettings(page);
    await page.goto("/settings/media#music-naming");
    const card = page.locator("#music-naming");
    await card.getByRole("textbox", { name: "Album folder" }).fill("{Album Artist}");
    await expect(card.getByText(/Album folder: the last folder must have \{Album Title\}/)).toBeVisible();
    expect(puts).toHaveLength(0);
  });

  test("house tags can be switched off, keeping files as downloaded", async ({ page }) => {
    const puts = await trackSettings(page);
    await page.goto("/settings/media#music-naming");
    const card = page.locator("#music-naming");
    const toggle = card.getByRole("switch", { name: /Write house tags/ });
    await expect(toggle).toBeChecked();
    await toggle.click();
    await expect.poll(() => puts).toEqual([{ music_write_tags: false }]);
  });

  test("an album's rename is previewed first and applies exactly the moves shown", async ({ page }) => {
    const state = await mockAlbum(page);
    await page.goto("/music/album/61");
    await page.getByRole("button", { name: "Rename files…" }).click();
    const dialog = page.getByRole("dialog", { name: "Rename this album's files" });
    await expect(dialog.getByText("Low Tide (2019)/01 - Undertow.flac")).toBeVisible();
    await expect(dialog.getByText("Won't be renamed: a different file already exists there")).toBeVisible();
    expect(state.renames).toHaveLength(0); // the preview moves nothing

    await dialog.getByRole("button", { name: "Rename 2 files" }).click();
    await page.getByRole("dialog", { name: "Rename 2 files?" }).getByRole("button", { name: "Cancel" }).click();
    expect(state.renames).toHaveLength(0);

    await dialog.getByRole("button", { name: "Rename 2 files" }).click();
    await page.getByRole("dialog", { name: "Rename 2 files?" }).getByRole("button", { name: "Rename", exact: true }).click();
    await expect(dialog.getByText("Renamed 2 files.")).toBeVisible();
    expect(state.renames).toEqual([{ items: moves.filter((m) => !m.conflict) }]);
  });

  test("writing house tags asks first", async ({ page }) => {
    const state = await mockAlbum(page);
    await page.goto("/music/album/61");
    await page.getByRole("button", { name: "Write house tags" }).click();
    await page.getByRole("dialog", { name: "Write house tags into this album's files?" }).getByRole("button", { name: "Cancel" }).click();
    expect(state.retags).toBe(0);
    await page.getByRole("button", { name: "Write house tags" }).click();
    await page.getByRole("dialog", { name: "Write house tags into this album's files?" }).getByRole("button", { name: "Write tags" }).click();
    await expect.poll(() => state.retags).toBe(1);
    await expect(page.getByText("Retagged 2 file(s) · 0 skipped · 0 failed")).toBeVisible();
  });
});
