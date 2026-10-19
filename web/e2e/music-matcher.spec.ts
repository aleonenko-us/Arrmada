import type { Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import type { ImportReview } from "../src/lib/api";

// MUS-31: the music matcher's review reasons with their breakdown in words, importing
// into an edition it weighed, and the Music matching settings card.

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

// A download the matcher held: two pressings fit almost equally.
const heldMusic: ImportReview = {
  id: 7, hash: "7777777777777777777777777777777777777777", name: "The Quiet Harbours - Low Tide (2019) [FLAC]",
  content_path: "/media/downloads/The Quiet Harbours - Low Tide (2019) [FLAC]", media_type: "music",
  expected_id: 51, expected_title: "Low Tide", parsed_title: "Low Tide",
  reason: "Two editions fit almost equally: \"Low Tide\" (GB CD 2019 · 2 tracks) and \"Low Tide\" (JP CD 2019 · 3 tracks).",
  reason_code: "close_editions", size_bytes: 9e8, indexer: "Fixture Indexer", created_at: "2026-10-08 09:00:00",
  match: {
    reason: "close_editions", summary: "Two editions fit almost equally.", expected: true, threshold: 0.15,
    candidates: [
      {
        album_id: 51, release_id: 7, album: "Low Tide", artist: "The Quiet Harbours", edition: "GB CD 2019 · 2 tracks", target: true,
        distance: 0.021, fit: 0.021,
        components: [{ name: "tracks", weight: 2, penalty: 0.2, count: 2, share: 0.021 }, { name: "album", weight: 3, penalty: 0, count: 1, share: 0 }],
        tracks: [{ file: "02 - Breakwater.flac", track: "02 Breakwater", distance: 0.2, notes: ["length off by 14 s"] }],
      },
      {
        album_id: 51, release_id: 12, album: "Low Tide", edition: "JP CD 2019 · 3 tracks", distance: 0.04, fit: 0.01,
        components: [{ name: "missing_tracks", weight: 0.9, penalty: 1, count: 1, share: 0.03 }],
        missing: ["03 Lantern (bonus)"],
      },
    ],
    fingerprint: "off",
  },
};

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("a matcher hold explains itself and imports into the edition picked", async ({ page, api }) => {
    await page.route((u) => u.pathname === "/api/v1/reviews", (route) => json(route, { reviews: [heldMusic] }));
    await page.goto("/activity/needs-you");
    const card = page.getByTestId("review-card").filter({ hasText: "Low Tide (2019)" });
    await expect(card.getByText("Two editions", { exact: true })).toBeVisible();
    const why = card.getByTestId("match-breakdown");
    await expect(why).toContainText("GB CD 2019 · 2 tracks");
    await expect(why).toContainText("distance 0.02");
    await expect(why).toContainText("track titles, numbers and lengths +0.02");
    await expect(why).toContainText("length off by 14 s");
    await expect(why).toContainText("No file for: 03 Lantern (bonus)");
    await expect(why.getByRole("link", { name: "Settings → Connections" })).toHaveAttribute("href", "/settings/connections#apikey-acoustid");

    await card.getByRole("button", { name: "Import as JP CD 2019 · 3 tracks" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/reviews/7/import").map((c) => c.body)).toEqual([
      { target_id: 51, target_kind: "music", release_id: 12 },
    ]);
  });
});

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("the Music matching card saves a threshold", async ({ page, api }) => {
    await page.goto("/settings/media");
    const card = page.locator("#music-matching");
    await expect(card.getByRole("heading", { name: "Music matching" })).toBeVisible();
    const field = card.getByLabel("Import by itself (grabbed for the album)");
    await field.fill("0.2");
    await field.press("Enter");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/music/matching").map((c) => c.body)).toEqual([{ expected: 0.2 }]);
  });
});
