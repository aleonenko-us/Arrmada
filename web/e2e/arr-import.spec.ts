import { test, expect } from "./mockApi";

// ARRIMPORT: Settings → Import & migrate → Radarr / Sonarr. Connect, look at what would come
// over (folders, where the files are, quality profiles), then import as a job and read the
// summary. The API key goes with each call and is never shown back.
test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("connect, map, import and read the summary", async ({ page, api }) => {
    await page.goto("/settings/import#arr-import");
    const card = page.locator("#arr-import");
    await expect(card.getByRole("heading", { name: "Import from Radarr / Sonarr" })).toBeVisible();
    await expect(card.getByText(/Tags, blocklists, history and download-client settings aren't brought over/)).toBeVisible();
    await expect(card.getByLabel("API key")).toHaveAttribute("type", "password");

    await card.getByLabel("Radarr URL").fill("http://radarr:7878");
    await card.getByLabel("API key").fill("e2e-radarr-key");
    await card.getByRole("button", { name: "Connect" }).click();

    await expect(card.getByText("Radarr 4K 6.4.4")).toBeVisible();
    await expect(card.getByText(/4 films: 1 already in Arrmada, 3 to add \(1 with their files\)\. 1 wanted film will be searched for a few at a time/)).toBeVisible();
    // The root folder Arrmada has by name, and the one it hasn't.
    await expect(card.getByLabel("Arrmada folder for /movies")).toHaveValue("0");
    await expect(card.getByLabel("Arrmada folder for /kids")).toHaveValue("");
    await expect(card.getByRole("button", { name: "Add it as a Movies folder…" })).toBeVisible();
    // The suggested path map, editable; a second one added for /kids.
    await expect(card.getByLabel("Path in Radarr 4K").first()).toHaveValue("/movies");
    await expect(card.getByText("1 of 2 files found · 1 not found.")).toBeVisible();
    await card.getByRole("button", { name: "Add a mapping" }).click();
    await card.getByLabel("Path in Radarr 4K").nth(1).fill("/kids");
    await card.getByLabel("Path inside Arrmada").nth(1).fill("/media/kids");
    await card.getByRole("button", { name: "Check again" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/import/arr/preview").length).toBe(2);
    expect(api.callsTo("POST", "/api/v1/import/arr/preview")[1].body).toMatchObject({
      app: "radarr", api_key: "e2e-radarr-key",
      path_maps: [{ from: "/movies", to: "/media/movies" }, { from: "/kids", to: "/media/kids" }],
    });
    // Profiles in use, each with the suggested Arrmada profile; one changed by hand.
    await expect(card.getByLabel("Arrmada profile for HD-1080p")).toHaveValue("custom:1");
    await expect(card.getByLabel("Arrmada profile for SD")).toHaveCount(0);
    await card.getByLabel("Arrmada profile for Ultra-HD").selectOption("custom:1");

    await card.getByRole("button", { name: "Import from Radarr 4K" }).click();
    await expect(card.getByText("Imported 3 films · 2 files recorded · 1 already here · 0 skipped · 0 failed · 1 wanted, searched for a few at a time")).toBeVisible();
    await card.getByText("Imported, with a note (1)").click();
    await expect(card.getByText(/added without its file/)).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/import/arr/run")[0].body).toMatchObject({
      app: "radarr", url: "http://radarr:7878", api_key: "e2e-radarr-key", import_missing: false,
      profiles: { 4: "custom:1", 5: "custom:1", 6: "custom:1" }, folders: { "/movies": 0 },
    });
    // Another instance next: the card starts over.
    await card.getByRole("button", { name: "Import another" }).click();
    await expect(card.getByLabel("Radarr URL")).toHaveValue("");
    await expect(page.getByText("e2e-radarr-key")).toHaveCount(0);
  });

  test("the old folders card's address lands on the import", async ({ page }) => {
    await page.goto("/settings/import#arr-folders");
    await expect(page).toHaveURL(/\/settings\/import#arr-import$/);
  });
});
