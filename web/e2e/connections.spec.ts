import { test, expect } from "./mockApi";

// INT-16/17, CFG-15: Settings → Connections is where every integration lives. The old
// Indexers and Download clients pages redirect to it, the sidebar has one entry, Test
// updates a card in place, a health link lands on its card, and the metadata keys are
// edited inside their cards (admins only).
test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("the old pages redirect and the sidebar has one Connections entry", async ({ page }) => {
    await page.goto("/indexers");
    await expect(page).toHaveURL(/\/settings\/connections#search$/);
    await expect(page.getByRole("heading", { name: "Search", exact: true })).toBeVisible();
    await page.goto("/downloadclients");
    await expect(page).toHaveURL(/\/settings\/connections#download-clients$/);
    // The API keys' old address lands on their cards too.
    await page.goto("/settings/system#api-keys");
    await expect(page).toHaveURL(/\/settings\/connections#api-keys$/);

    // The app sidebar (the Settings rail is an aside too).
    const sidebar = page.getByRole("complementary").first();
    await expect(sidebar.getByRole("link", { name: "Connections" })).toHaveAttribute("href", "/settings/connections");
    await expect(sidebar.locator('a[href="/indexers"]')).toHaveCount(0);
    await expect(sidebar.locator('a[href="/downloadclients"]')).toHaveCount(0);
  });

  test("every section's cards show their state, and Test updates a card in place", async ({ page, api }) => {
    await page.goto("/settings/connections");
    const qb = page.locator("#download_client-1");
    await expect(qb.getByText(/Failing since .*: connection refused/)).toBeVisible();
    await expect(qb.getByRole("img", { name: "Failing" })).toBeVisible();
    await expect(page.locator("#indexer-1").getByText("214 searches · 2 grabs · 3 failed (24h)")).toBeVisible();
    await expect(page.locator("#apikey-tvdb").getByText("Not set up")).toBeVisible();
    await expect(page.locator("#plex").getByRole("link", { name: "Set up →" })).toHaveAttribute("href", "/settings/plex#plex-connection");

    await qb.getByRole("button", { name: "Test" }).click();
    await expect(qb.getByRole("status")).toHaveText("✓ Connected");
    await expect(qb.getByText("Last OK just now")).toBeVisible();
    await expect(qb.getByRole("img", { name: "Working" })).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/connections/download_client/1/test")).toHaveLength(1);
  });

  test("a health warning's link lands on its card", async ({ page }) => {
    await page.goto("/settings/connections#download_client-1");
    const qb = page.locator("#download_client-1");
    await expect(qb).toBeInViewport();
    expect(await qb.evaluate((el) => (el as HTMLElement).style.border)).toContain("var(--accent)");
  });

  test("the TMDB key is edited inside its card, never shown back", async ({ page }) => {
    await page.goto("/settings/connections");
    const tmdb = page.locator("#apikey-tmdb");
    await tmdb.getByRole("button", { name: "Edit" }).click();
    await expect(tmdb.getByText("Set (…9e14)")).toBeVisible();
    await expect(tmdb.getByLabel("TMDB")).toHaveValue("");
    await expect(page.locator("#discovery-region")).toBeVisible();
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("runs the indexers and clients, but the keys are read-only", async ({ page, api }) => {
    await page.goto("/settings/connections");
    await expect(page.locator("#indexer-1").getByRole("button", { name: "More for Fixture Indexer" })).toBeVisible();
    await expect(page.locator("#apikey-tmdb").getByRole("button", { name: "Edit" })).toHaveCount(0);
    await expect(page.locator("#discovery-region")).toHaveCount(0);
    await api.quiet();
    expect(api.callsTo("GET", "/api/v1/apikeys")).toHaveLength(0);
  });
});
