import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import type { Artist, MusicAlbum, RankedRelease } from "../src/lib/api";

// MUS-09, MUS-16, MUS-17: searching music from the album and artist pages. The album page
// says how its last search went and where it stands against its profile's cutoff, Search
// now runs a job, and the interactive search lists every release with why it would or
// wouldn't be grabbed, with no video filters, and grabs by token. The artist page searches
// its wanted albums as one job, and each album row opens the same interactive search.

const album: MusicAlbum = {
  id: 71, artist_id: 7, mbid: "rg-71", title: "Paper Lanterns", year: 2018, album_type: "Album", monitored: true,
  track_count: 10, have_tracks: 10, size_bytes: 110_000_000, edition_tracks: 10, held_tier: "MP3-320",
  tracks: [{ id: 1, album_id: 71, disc_number: 1, track_number: 1, title: "Glow", monitored: true, has_file: true, format: "MP3-320" }],
};

const artist: Artist = {
  id: 7, mbid: "mb-7", name: "Field Notes", monitored: true, quality_profile: "custom:3", in_library: true,
  stats: { albums: 1, tracks: 10, have_tracks: 10, size_bytes: 110_000_000, albums_monitored: 1, albums_complete: 1, albums_unlisted: 0, albums_with_files: 1, albums_wanted: 0 },
  albums: [album],
};

const release = (r: Partial<RankedRelease> & { title: string }): RankedRelease => ({
  indexer: "Tracker", size_gb: 0.4, seeders: 20, peers: 25, summary: "FLAC", eligible: false, recommended: false, token: `tok-${r.title.length}`, ...r,
});

const releases: RankedRelease[] = [
  release({ title: "Field Notes - Paper Lanterns (2018) [FLAC]", summary: "FLAC · Deluxe", eligible: true, recommended: true, token: "tok-flac" }),
  release({ title: "Field Notes - Paper Lanterns (2018) [MP3 V0]", summary: "MP3-V0", size_gb: 0.08, reject_reason: "no better than the MP3-320 you have" }),
  release({ title: "Field Notes - Paper Lanterns Live 1080p BluRay x264", summary: "FLAC", size_gb: 7.5, reject_reason: "looks like a video release" }),
  release({ title: "Other Band - Paper Lanterns (2018) [FLAC]", reject_reason: "doesn't name Field Notes" }),
];

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

async function mockSearch(page: Page) {
  const sent = { grabs: [] as unknown[], albumSearches: 0, artistSearches: 0 };
  await page.route((u) => u.pathname === "/api/v1/music/artists", (route) => json(route, { artists: [artist] }));
  await page.route((u) => u.pathname === "/api/v1/music/artists/7", (route) => json(route, artist));
  await page.route((u) => u.pathname === "/api/v1/music/artists/7/history", (route) => json(route, { events: [] }));
  await page.route((u) => u.pathname === "/api/v1/music/albums/71", (route) => json(route, album));
  await page.route((u) => u.pathname === "/api/v1/music/albums/71/search-state", (route) => json(route, {
    upgrade: { profile: "Lossless, or the best MP3", held: "MP3-320", cutoff: "FLAC-24", state: "below" },
  }));
  await page.route((u) => u.pathname === "/api/v1/music/albums/71/releases", (route) => json(route, { profile: "custom:3", why: ["FLAC, the best tier found that your profile takes"], releases }));
  await page.route((u) => u.pathname === "/api/v1/music/albums/71/grab", (route) => {
    sent.grabs.push(route.request().postDataJSON());
    return json(route, { status: "grabbed", title: releases[0].title });
  });
  await page.route((u) => u.pathname === "/api/v1/wanted/music/71/search", (route) => {
    sent.albumSearches++;
    return json(route, { status: "searching", job_id: 931, existing: false }, 202);
  });
  await page.route((u) => u.pathname === "/api/v1/jobs/931", (route) => json(route, {
    id: 931, kind: "album.search", target: "album:71", trigger: "user:1", status: "succeeded", progress: 1,
    message: "Grabbed Field Notes - Paper Lanterns (2018) [FLAC]", error: "",
    result: { searched: true, returned: 4, matching: 3, usable: 3, grabbed: 1, grabbed_titles: ["Field Notes - Paper Lanterns (2018) [FLAC]"], reason: "grabbed" },
  }));
  await page.route((u) => u.pathname === "/api/v1/music/artists/7/search", (route) => {
    sent.artistSearches++;
    return json(route, { status: "searching", job_id: 932, existing: false }, 202);
  });
  await page.route((u) => u.pathname === "/api/v1/jobs/932", (route) => json(route, {
    id: 932, kind: "artist.search", target: "artist:7", trigger: "user:1", status: "succeeded", progress: 1,
    message: "Searched 2 albums: grabbed 1", error: "",
  }));
  return sent;
}

test.describe("Music searching", () => {
  test.use({ persona: "admin" });

  test("the album page says where it stands, and Search now runs a job", async ({ page }) => {
    const sent = await mockSearch(page);
    await page.goto("/music/album/71");
    await expect(page.getByTestId("album-upgrade")).toHaveText(/Upgrades: MP3-320 → FLAC-24 \(Lossless, or the best MP3\)/);
    await page.getByRole("button", { name: "Search now" }).click();
    await expect(page.getByText("Grabbed Field Notes - Paper Lanterns (2018) [FLAC]").first()).toBeVisible();
    expect(sent.albumSearches).toBe(1);
  });

  test("interactive search shows each release's reason and grabs by token", async ({ page }) => {
    const sent = await mockSearch(page);
    await page.goto("/music/album/71");
    await page.getByRole("button", { name: "Interactive search…" }).click();
    const dialog = page.getByRole("dialog", { name: /Search indexers — Paper Lanterns/ });
    await expect(dialog.getByText("Recommended")).toBeVisible();
    await expect(dialog.getByText("no better than the MP3-320 you have")).toBeVisible();
    await expect(dialog.getByText("looks like a video release")).toBeVisible();
    await expect(dialog.getByText("doesn't name Field Notes")).toBeVisible();
    // An album's search has no video filters, and sizes read in MB.
    await expect(dialog.getByText("Resolution", { exact: true })).toHaveCount(0);
    await expect(dialog.getByRole("option", { name: "Highest bitrate" })).toHaveCount(0);
    await expect(dialog.getByText("410 MB").first()).toBeVisible();
    await dialog.getByRole("button", { name: "Grab" }).first().click();
    await expect.poll(() => sent.grabs).toEqual([{ token: "tok-flac" }]);
  });

  test("the artist page searches its wanted albums, and each album row opens the search", async ({ page }) => {
    const sent = await mockSearch(page);
    await page.goto("/music/7");
    await page.getByRole("button", { name: "Search monitored" }).click();
    await expect(page.getByText("Searched 2 albums: grabbed 1")).toBeVisible();
    expect(sent.artistSearches).toBe(1);
    await page.getByRole("button", { name: "Interactive search for Paper Lanterns" }).click();
    await expect(page.getByRole("dialog", { name: /Search indexers — Paper Lanterns/ }).getByText("looks like a video release")).toBeVisible();
  });

  test("adding an artist sends the chosen profile and Search now", async ({ page }) => {
    await mockSearch(page);
    const added: unknown[] = [];
    await page.route((u) => u.pathname === "/api/v1/quality/profiles", (route) => json(route, {
      formats: [], profiles: [
        { key: "custom:3", name: "Lossless, or the best MP3", media_type: "music", built_in: false, is_default: true, summary: "" },
        { key: "custom:4", name: "High-quality lossy", media_type: "music", built_in: false, is_default: false, summary: "" },
      ],
    }));
    await page.route((u) => u.pathname === "/api/v1/music/lookup", (route) => json(route, { results: [{ mbid: "mb-9", name: "Night Ferry", type: "Group", country: "GB" }] }));
    await page.route((u) => u.pathname === "/api/v1/music/artists" && u.searchParams.size === 0, (route) => {
      if (route.request().method() === "POST") {
        added.push(route.request().postDataJSON());
        return json(route, { ...artist, id: 9, name: "Night Ferry" }, 201);
      }
      return json(route, { artists: [artist] });
    });
    await page.goto("/music");
    await page.getByRole("button", { name: "+ Add artist" }).click();
    const dialog = page.getByRole("dialog", { name: "Add an artist" });
    await expect(dialog.getByLabel("Quality")).toHaveValue("custom:3");
    await dialog.getByLabel("Quality").selectOption("custom:4");
    await dialog.getByLabel("Start searching now").uncheck();
    await dialog.getByRole("textbox", { name: "Artist name" }).fill("Night Ferry");
    await dialog.getByRole("button", { name: "Search", exact: true }).click();
    await dialog.getByRole("button", { name: "Add", exact: true }).click();
    await expect.poll(() => added).toEqual([{ mbid: "mb-9", monitored: true, monitor: "all", quality_profile: "custom:4", search_now: false }]);
  });
});
