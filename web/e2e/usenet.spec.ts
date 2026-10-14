import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { mockUsenet, SAB_ID, SAB_KEY, USENET_DOWNLOADING, USENET_FAILED } from "./fixtures/usenet";

// Usenet U4: SABnzbd sits beside qBittorrent. It's added on Settings → Connections with its
// URL and API key (sent once, never shown back), its card says what Arrmada needs from it,
// and usenet releases and downloads carry a small Usenet chip — with no seeds or ratio, and
// Retry for a job SABnzbd failed. The network preference shows only with a usenet client.

// USENET_SHOTS=<folder> saves the report's screenshots there (never into the repo).
const SHOTS = process.env.USENET_SHOTS;
async function shot(page: Page, name: string) {
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: false });
}

test.describe("admin, desktop", () => {
  test.use({ persona: "admin", colorScheme: "dark" });
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("arrmada.theme", "dark"));
  });

  test("adds SABnzbd with its URL and API key, tests it, and never shows the key again", async ({ page, api }) => {
    await mockUsenet(page, api, { withClient: false });
    await page.goto("/settings/connections");
    await page.getByRole("button", { name: "+ Add client" }).click();
    const form = page.getByRole("form", { name: "Add a download client" });
    await form.getByRole("radio", { name: /SABnzbd/ }).check();
    await expect(form.getByLabel("Name")).toHaveValue("SABnzbd");
    await expect(form.getByLabel("Username")).toHaveCount(0);
    await expect(form.getByText("SABnzbd → Config → General → API Key")).toBeVisible();
    await form.getByLabel("URL").fill("http://sabnzbd:8080");
    await form.getByLabel("API key").fill(SAB_KEY);
    await shot(page, "usenet-add-sabnzbd-desktop-dark");
    await form.getByRole("button", { name: "Save client" }).click();

    await expect.poll(() => api.callsTo("POST", "/api/v1/downloadclients").length).toBe(1);
    expect(api.callsTo("POST", "/api/v1/downloadclients")[0].body).toEqual({ name: "SABnzbd", kind: "sabnzbd", url: "http://sabnzbd:8080", username: "", password: SAB_KEY, priority: 25 });
    // Saved, it's tested at once and its card shows the answer.
    const card = page.locator(`#download_client-${SAB_ID}`);
    await expect(card.getByRole("status")).toHaveText("✓ Connected · SABnzbd 4.5.5");
    expect(api.callsTo("POST", `/api/v1/connections/download_client/${SAB_ID}/test`)).toHaveLength(1);

    // The key is in no field and nowhere on the page, and the edit form keeps it blank.
    await card.getByRole("button", { name: "Edit" }).click();
    await expect(card.getByRole("form", { name: "Edit SABnzbd" }).getByLabel("API key")).toHaveValue("");
    expect(await page.content()).not.toContain(SAB_KEY);
  });

  test("the SABnzbd card shows categories, the completed folder, servers and warnings; Add missing categories only on the click", async ({ page, api }) => {
    await mockUsenet(page, api);
    await page.goto("/settings/connections");
    const card = page.locator(`#download_client-${SAB_ID}`);
    // Closed, the card asks SABnzbd nothing.
    await api.quiet();
    expect(api.callsTo("GET", `/api/v1/downloadclients/${SAB_ID}/status`)).toHaveLength(0);

    await card.getByRole("button", { name: "Edit" }).click();
    const facts = card.getByRole("region", { name: "SABnzbd" });
    const cats = facts.getByRole("list", { name: "Categories" });
    await expect(cats.getByRole("listitem")).toHaveText(["✓ arrmada (movies)", "✓ arrmada-tv (TV)", "✕ arrmada-books (books)", "✕ arrmada-music (music)"]);
    await expect(facts.getByText("Arrmada sees it at", { exact: false })).toBeVisible();
    await expect(facts).toContainText("✓ Completed folder /downloads/complete · Arrmada sees it at /media/downloads/complete");
    await expect(facts).toContainText("Usenet servers: 1 server, working");
    await expect(facts.getByRole("list", { name: "Needs a look" })).toContainText('language is "de"');
    expect(api.callsTo("POST", `/api/v1/downloadclients/${SAB_ID}/categories`)).toHaveLength(0);
    await card.evaluate((el) => el.scrollIntoView({ block: "start" }));
    await shot(page, "usenet-card-desktop-dark");

    await facts.getByRole("button", { name: "Add missing categories" }).click();
    await expect(facts.getByRole("status")).toHaveText("Added arrmada-books, arrmada-music.");
    await expect(cats.getByRole("listitem").filter({ hasText: "✕" })).toHaveCount(0);
    await expect(facts.getByRole("button", { name: "Add missing categories" })).toHaveCount(0);
    expect(api.callsTo("POST", `/api/v1/downloadclients/${SAB_ID}/categories`)).toHaveLength(1);
    // Read once when the card opened: never polled.
    await api.quiet(600);
    expect(api.callsTo("GET", `/api/v1/downloadclients/${SAB_ID}/status`)).toHaveLength(1);
  });

  test("a release search marks usenet rows, shows no seeders for them, and grabs them once SABnzbd exists", async ({ page, api }) => {
    await mockUsenet(page, api);
    await page.goto("/movies");
    await page.getByRole("button", { name: "table", exact: true }).click();
    await page.getByRole("row", { name: /Anchor Point/ }).getByRole("button", { name: "Search" }).click();
    const dialog = page.getByRole("dialog", { name: "Search indexers — Anchor Point" });
    const nzb = dialog.getByTitle("Anchor.Point.2022.1080p.WEB-DL.H264-NZBGRP", { exact: true }).locator("xpath=ancestor::div[contains(@class, 'rounded-xl')][1]");
    await expect(nzb.getByTestId("usenet-chip")).toHaveText("Usenet");
    await expect(nzb.getByText(/seeders\/peers/)).toHaveCount(0);
    await expect(nzb.getByRole("button", { name: "Grab" })).toBeEnabled();
    // Torrent rows carry no chip and keep their seeders.
    await expect(dialog.getByTestId("usenet-chip")).toHaveCount(1);
    await expect(dialog.getByText(/seeders\/peers/).first()).toBeVisible();
    await shot(page, "usenet-release-search-desktop-dark");
  });

  test("without a usenet client a usenet release says why and can't be grabbed", async ({ page, api }) => {
    await mockUsenet(page, api, { withClient: false });
    await page.goto("/movies");
    await page.getByRole("button", { name: "table", exact: true }).click();
    await page.getByRole("row", { name: /Anchor Point/ }).getByRole("button", { name: "Search" }).click();
    const dialog = page.getByRole("dialog", { name: "Search indexers — Anchor Point" });
    const nzb = dialog.getByTitle("Anchor.Point.2022.1080p.WEB-DL.H264-NZBGRP", { exact: true }).locator("xpath=ancestor::div[contains(@class, 'rounded-xl')][1]");
    await expect(nzb.getByText("Usenet release — no usenet download client is set up")).toBeVisible();
    await expect(nzb.getByRole("button", { name: "Grab" })).toBeDisabled();
  });

  test("the queue marks usenet jobs: no seeds or ratio, what's left and ETA, Retry when SABnzbd failed one, never under Seeding", async ({ page, api }) => {
    await mockUsenet(page, api);
    await page.goto("/activity/queue");
    const card = (title: string) => page.getByTestId("transfer-card").filter({ has: page.getByTestId("transfer-title").filter({ hasText: title }) });

    const tide = card("Tidewater");
    await expect(tide.getByTestId("usenet-chip")).toBeVisible();
    await expect(tide.getByTestId("usenet-left")).toHaveText("953.7 MB left · ETA 5m");
    // A torrent row has no chip.
    await expect(card("Driftwood").getByTestId("usenet-chip")).toHaveCount(0);
    // No tracker controls in the menu: only Block and Delete beside Pause.
    await expect(tide.getByRole("button", { name: /More actions/ })).toHaveCount(0);

    const glass = card("Glasshouse");
    await expect(glass.getByTestId("usenet-fail")).toHaveText("Out of retention: articles are missing on the server");
    await expect(glass.getByRole("button", { name: "Pause" })).toHaveCount(0);
    await shot(page, "usenet-queue-desktop-dark");
    await glass.getByRole("button", { name: "Retry" }).click();
    await expect.poll(() => api.callsTo("POST", `/api/v1/queue/${USENET_FAILED}/action`).length).toBe(1);
    expect(api.callsTo("POST", `/api/v1/queue/${USENET_FAILED}/action`)[0].body).toEqual({ action: "retry" });
    expect(api.callsTo("POST", `/api/v1/queue/${USENET_DOWNLOADING}/action`)).toHaveLength(0);

    // Unpacking and an imported job sit under Finishing, read Unpacking / Imported.
    const views = page.getByRole("group", { name: "Queue views" });
    await views.getByRole("button", { name: /^Finishing/ }).click();
    await expect(card("Harbour Lights").filter({ hasText: "Unpacking" })).toContainText("SABnzbd is checking and unpacking it");
    await expect(card("Saltwater").getByText("Imported", { exact: true })).toBeVisible();
    await expect(card("Saltwater").getByTestId("usenet-chip")).toBeVisible();

    // Seeding has torrents only.
    await views.getByRole("button", { name: /^Seeding/ }).click();
    await expect(card("Harbour Lights").first()).toBeVisible();
    await expect(page.getByTestId("usenet-chip")).toHaveCount(0);
    await expect(page.getByText("Saltwater")).toHaveCount(0);
  });

  test("the network preference shows only with a usenet client, and saves itself", async ({ page, api }) => {
    await mockUsenet(page, api, { withClient: false });
    await page.route("**/api/v1/downloads/disk-guard", (r) => r.fulfill({ json: { path: "/media/downloads", measurable: true, used_pct: 40, holding: 0, shared_with: [] } }));
    await page.goto("/settings/downloads");
    await expect(page.getByRole("heading", { name: "Stalled downloads" })).toBeVisible();
    await api.quiet();
    await expect(page.locator("#network")).toHaveCount(0);

    await mockUsenet(page, api);
    await page.reload();
    const card = page.locator("#network");
    const select = card.getByLabel("When a release is on both Usenet and torrents");
    await expect(select).toHaveValue("best");
    await expect(select.locator("option")).toHaveText(["Best release wins", "Prefer Usenet", "Prefer torrents"]);
    await select.selectOption("usenet");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/settings")[0].body).toEqual({ download_network_preference: "usenet" });
  });
});
