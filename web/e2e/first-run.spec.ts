import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import { setup as configuredSetup, setupChecklist as allDone } from "./fixtures/system";

// CFG-21/22/23/24: a fresh install walks Metadata → Folders → Plex → Indexers → Finish,
// ending on the Getting started checklist as links; Skip is on the first step only; Run
// setup again reopens the wizard from Settings → System; the Dashboard card ticks itself.

// override answers the given endpoints before the default fixtures (the last route
// registered is tried first), recording the calls like the defaults do.
async function override(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method() || url.pathname !== r.path) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(r.body ?? {}) });
    }
    return route.fallback();
  });
}

const blank = { movies: "", tv: "", ebooks: "", audiobooks: "", music: "", downloads: "" };
// A fresh install: nothing chosen, no keys, no Plex, no indexers, no Prowlarr answering.
const fresh = {
  ...configuredSetup, needed: true, complete: false, tmdb_configured: false, libraries_chosen: false,
  library: blank, running: blank, suggestions: {},
  plex_connected: false, plex_server: "", plex_family: { enabled: false, auto_approve_types: "" },
  indexers_enabled: 0, prowlarr_available: false,
};
// Half done: the folders are chosen; indexers and the rest are still to do.
const halfDone = {
  ...allDone,
  done: false,
  items: allDone.items.map((it) => (["tmdb", "folders"].includes(it.key) ? it : { ...it, done: false })),
};

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("a fresh install walks every step and ends on the checklist", async ({ page, api }) => {
    await override(page, api, [
      { method: "GET", path: "/api/v1/setup", body: fresh },
      { method: "PUT", path: "/api/v1/system/library", body: blank },
      { method: "POST", path: "/api/v1/setup/complete", body: { status: "ok" } },
      { method: "GET", path: "/api/v1/setup/checklist", body: halfDone },
    ]);
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Welcome to Arrmada" })).toBeVisible();
    await expect(page.getByText("Skip for now — you can run it again from Settings → System")).toBeVisible();
    await page.getByRole("button", { name: "Continue" }).click();

    await expect(page.getByRole("heading", { name: "Where's your media?" })).toBeVisible();
    await expect(page.getByText(/Skip/)).toHaveCount(0);
    await page.getByRole("button", { name: "Save and continue" }).click();

    await expect(page.getByRole("heading", { name: "Connect Plex" })).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Let family sign in with Plex" })).not.toBeChecked();
    await page.getByText("Skip this step").click();

    await expect(page.getByRole("heading", { name: "Where releases come from" })).toBeVisible();
    await expect(page.getByText(/No Prowlarr answered at its saved address/)).toBeVisible();
    expect(api.callsTo("GET", "/api/v1/setup?prowlarr=1").length).toBeGreaterThan(0);
    await page.getByText("Skip this step").click();

    await expect(page.getByRole("heading", { name: "Here's where you are" })).toBeVisible();
    await expect(page.getByRole("link", { name: /Choose your library folders \(done\)/ })).toBeVisible();
    const indexer = page.getByRole("link", { name: /Add an indexer, or sync them from Prowlarr \(to do\)/ });
    await expect(indexer).toHaveAttribute("href", "/settings/connections#search");
    await indexer.click();
    await expect(page).toHaveURL(/\/settings\/connections#search$/);
    expect(api.callsTo("POST", "/api/v1/setup/complete")).toHaveLength(1);
    // Nothing was typed into the wizard, so nothing was saved behind the admin's back.
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0);
    expect(api.callsTo("PUT", "/api/v1/apikeys")).toHaveLength(0);
  });

  test("Run setup again reopens the wizard and comes back to Settings → System", async ({ page }) => {
    await page.goto("/settings/system");
    await page.getByRole("link", { name: "Run setup again" }).click();
    await expect(page).toHaveURL(/\/setup$/);
    await expect(page.getByRole("heading", { name: "Run setup again" })).toBeVisible();
    await page.getByText("Back to Settings → System").click();
    await expect(page).toHaveURL(/\/settings\/system$/);
  });

  test("the Dashboard shows Getting started until it's done", async ({ page, api }) => {
    await override(page, api, [{ method: "GET", path: "/api/v1/setup/checklist", body: halfDone }]);
    await page.goto("/");
    const card = page.getByRole("region", { name: /Getting started/ });
    await expect(card).toBeVisible();
    await expect(card.getByRole("link", { name: /Connect a download client \(to do\)/ })).toHaveAttribute("href", "/settings/connections#download-clients");
  });

  test("no card once everything is done", async ({ page, api }) => {
    await page.goto("/");
    await expect(page.getByText("Realtime")).toBeVisible();
    expect(api.callsTo("GET", "/api/v1/setup/checklist").length).toBeGreaterThan(0);
    await expect(page.getByText(/Getting started/)).toHaveCount(0);
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("no Getting started card, and no Run setup again", async ({ page, api }) => {
    await page.goto("/");
    await expect(page.getByText("Realtime")).toBeVisible();
    expect(api.callsTo("GET", "/api/v1/setup/checklist")).toHaveLength(0);
    await page.goto("/setup");
    await expect(page.getByRole("heading", { name: "Run setup again" })).toHaveCount(0);
  });

  // CFG-25: Scan in Settings → Library follows the running scan's progress, then says what
  // it found, instead of reloading after a fixed five seconds.
  test("a second Scan in Settings → Library follows the running scan's progress", async ({ page, api }) => {
    const scanJob = { id: 88, kind: "series.scan", target: "all", trigger: "user:2", status: "running", progress: 0.5,
      message: "Scanning 20 / 40 show folders", error: "", created_at: null, started_at: null, finished_at: null };
    await override(page, api, [
      { method: "POST", path: "/api/v1/series/scan", status: 409, body: { status: "error", message: "a TV library scan is already running", existing: true, job_id: 88 } },
      { method: "GET", path: "/api/v1/jobs/88", body: scanJob },
    ]);
    await page.goto("/settings/library");
    await page.getByRole("button", { name: "Scan" }).nth(1).click(); // TV Shows
    await expect(page.getByText("TV Shows: Scanning 20 / 40 show folders · 50%")).toBeVisible();
    await expect(page.getByText("Already scanning — showing its progress.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Scanning…" })).toBeDisabled();
  });
});
