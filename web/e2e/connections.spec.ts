import { test, expect } from "./mockApi";
import { plexConfig } from "./fixtures/modules";

// INT-16/17, CFG-15: Settings → Connections is where every integration lives. The old
// Indexers and Download clients pages redirect to it, the sidebar has one entry, Test
// updates a card in place, a health link lands on its card, and the metadata keys are
// edited inside their cards (admins only).
test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("the old pages redirect and Connections lives only in Settings", async ({ page }) => {
    await page.goto("/indexers");
    await expect(page).toHaveURL(/\/settings\/connections#search$/);
    await expect(page.getByRole("heading", { name: "Search", exact: true })).toBeVisible();
    await page.goto("/downloadclients");
    await expect(page).toHaveURL(/\/settings\/connections#download-clients$/);
    // The API keys' old address lands on their cards too.
    await page.goto("/settings/system#api-keys");
    await expect(page).toHaveURL(/\/settings\/connections#api-keys$/);

    // The app sidebar has no Connections entry (only Settings, which is the one lit up);
    // the Settings rail has it. (The Settings rail is an aside too.)
    const sidebar = page.getByRole("complementary").first();
    await expect(sidebar.locator('a[href="/settings/connections"]')).toHaveCount(0);
    await expect(sidebar.locator('a[href="/settings"][aria-current="page"]')).toHaveCount(1);
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
    // Plex is connected inside its card (CFG-16), not on another page.
    await expect(page.locator("#plex").getByRole("button", { name: "Connect Plex" })).toBeVisible();

    await qb.getByRole("button", { name: "Test" }).click();
    await expect(qb.getByRole("status")).toHaveText("✓ Connected");
    await expect(qb.getByText("Last OK just now")).toBeVisible();
    await expect(qb.getByRole("img", { name: "Working" })).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/connections/download_client/1/test")).toHaveLength(1);
  });

  test("Plex is connected, tested and signed in from its card; old addresses land there", async ({ page, api }) => {
    let saved = { ...plexConfig };
    await page.route("**/api/v1/insights/plex", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback();
      const body = route.request().postDataJSON() as { url: string; token?: string };
      api.calls.push({ method: "PUT", path: "/api/v1/insights/plex", body });
      saved = { ...saved, url: body.url, token_set: saved.token_set || !!body.token, status: "recording", server_name: "Basement" };
      return route.fulfill({ contentType: "application/json", body: JSON.stringify(saved) });
    });
    await page.route("**/api/v1/insights/plex/test", (route) =>
      route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true, server_name: "Basement", version: "1.41.0", libraries: [{ key: "1", title: "Movies", type: "movie" }] }) }));

    // The Plex page's old connection anchor, and Settings → Plex's own link, open the card.
    await page.goto("/settings/plex#plex-connection");
    await expect(page).toHaveURL(/\/settings\/connections#plex$/);
    const plex = page.locator("#plex");
    await expect(plex.getByRole("button", { name: "Sign in with Plex" })).toBeVisible();

    await plex.getByLabel("Server URL").fill("http://192.168.1.10:32400");
    await plex.getByLabel("X-Plex-Token").fill("tok-123");
    await plex.getByRole("button", { name: "Test connection" }).click();
    await expect(plex.getByText("✓ Connected to Basement · Plex 1.41.0")).toBeVisible();
    await plex.getByRole("button", { name: "Save" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/insights/plex").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/insights/plex")[0].body).toMatchObject({ url: "http://192.168.1.10:32400", token: "tok-123" });
    // The token goes back blank: it's never shown again.
    await expect(plex.getByLabel("X-Plex-Token")).toHaveValue("");
    await expect(plex.getByRole("link", { name: "Settings → Plex →" })).toHaveAttribute("href", "/settings/plex");
  });

  test("a redirect-mode Plex sign-in comes back to the Plex card and finishes there", async ({ page, api }) => {
    let polls = 0;
    await page.route(/\/api\/v1\/insights\/plex\/auth\/7$/, (route) => {
      polls++;
      api.calls.push({ method: "GET", path: "/api/v1/insights/plex/auth/7", body: null });
      return route.fulfill({ contentType: "application/json", body: JSON.stringify({ authorized: true, server_name: "Basement" }) });
    });
    await page.addInitScript(() => { if (!sessionStorage.getItem("e2e-once")) { sessionStorage.setItem("e2e-once", "1"); sessionStorage.setItem("arrmada.plexpin.connect", "7"); } });
    // An older build sent plex.tv back to Settings → Plex; that address still finishes.
    await page.goto("/settings/plex?plexpin=7");
    await expect(page.getByText("Signed in with Plex — connected to Basement")).toBeVisible({ timeout: 10_000 });
    await expect(page).toHaveURL(/\/settings\/connections$/);
    expect(polls).toBeGreaterThan(0);
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
