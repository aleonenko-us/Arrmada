import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import type { Artist } from "../src/lib/api";

// FE-12: Music on the kit, same features as before. Add artist is a Modal (named, focus
// on the search box, Escape closes); removing artists, in bulk or from the artist page,
// asks the same question through the shared confirm and Cancel leaves them alone.

const artist: Artist = {
  id: 5, mbid: "mb-5", name: "The Quiet Harbours", monitored: true, quality_profile: "lossless",
  stats: { albums: 2, tracks: 20, have_tracks: 8, size_bytes: 400_000_000 },
  albums: [{ id: 51, artist_id: 5, mbid: "rg-51", title: "Low Tide", year: 2019, album_type: "Album", monitored: true, track_count: 10, have_tracks: 8 } as never],
};

const json = (route: Route, body: unknown) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });

async function mockMusic(page: Page) {
  const state = { deletes: 0 };
  await page.route((u) => u.pathname === "/api/v1/music/artists", (route) => json(route, { artists: [artist] }));
  await page.route((u) => u.pathname === "/api/v1/music/artists/5", (route) => {
    if (route.request().method() === "DELETE") { state.deletes++; return route.fulfill({ status: 204 }); }
    return json(route, artist);
  });
  await page.route((u) => u.pathname === "/api/v1/music/artists/5/history", (route) => json(route, { events: [] }));
  return state;
}

test.describe("Music on the kit", () => {
  test.use({ persona: "admin" });

  test("Add artist is a dialog that Escape closes", async ({ page }) => {
    await mockMusic(page);
    await page.goto("/music");
    await page.getByRole("button", { name: "+ Add artist" }).click();
    const dialog = page.getByRole("dialog", { name: "Add an artist" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "Artist name" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
  });

  test("bulk Remove asks first; Cancel keeps the artists", async ({ page }) => {
    const state = await mockMusic(page);
    await page.goto("/music");
    await expect(page.getByText("Partial", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Select", exact: true }).click();
    await page.getByRole("button", { name: /The Quiet Harbours/ }).click();
    await page.getByRole("button", { name: "Remove", exact: true }).click();
    const ask = page.getByRole("dialog", { name: "Remove 1 artist from Arrmada?" });
    await expect(ask.getByText("Files on disk are kept.")).toBeVisible();
    await ask.getByRole("button", { name: "Cancel" }).click();
    await expect(ask).toHaveCount(0);
    expect(state.deletes).toBe(0);
  });

  test("the artist page's Delete asks the same question, then removes", async ({ page }) => {
    const state = await mockMusic(page);
    await page.goto("/music/5");
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    const ask = page.getByRole("dialog", { name: "Remove The Quiet Harbours and its albums?" });
    await expect(ask).toBeVisible();
    await ask.getByRole("button", { name: "Delete" }).click();
    await expect(page).toHaveURL(/\/music$/);
    expect(state.deletes).toBe(1);
  });
});
