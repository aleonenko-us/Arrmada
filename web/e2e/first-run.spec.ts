import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import { freshKeys, setup as configuredSetup, setupChecklist as allDone } from "./fixtures/system";

// CFG-21/22/23/24: a fresh install walks Keys → Folders → Media server → Indexers → Finish,
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
  ...configuredSetup, needed: true, complete: false, tmdb_configured: false, libraries_chosen: false, keys: freshKeys,
  library: blank, running: blank, suggestions: {},
  plex_connected: false, plex_server: "", plex_family: { enabled: false, auto_approve_types: "" },
  jellyfin_connected: false, jellyfin_server: "", jellyfin_family: { enabled: false, auto_approve_types: "" },
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
    // No TMDB key at hand: the step says what that means and moves on to the folders, it
    // doesn't end setup.
    await expect(page.getByText(/No key yet\? Movies, TV and Discover find nothing until one is added/)).toBeVisible();
    await page.getByRole("button", { name: "Continue without a key" }).click();

    await expect(page.getByRole("heading", { name: "Where's your media?" })).toBeVisible();
    await expect(page.getByText(/Skip/)).toHaveCount(0);
    // No folder at all is allowed, but only once asked.
    await page.getByRole("button", { name: "Save and continue" }).click();
    const noFolders = page.getByRole("dialog", { name: "Continue without library folders?" });
    await expect(noFolders).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/system/library")).toHaveLength(0);
    await noFolders.getByRole("button", { name: "Continue without them" }).click();

    // The Media server step (setup-media-server.spec.ts walks its choices); skipped here.
    await expect(page.getByRole("heading", { name: "Connect your media server" })).toBeVisible();
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

  // With nothing at /storage, the Folders step warns about managed storage only when /media
  // is Arrmada's own volume; a host folder there (ARRMADA_MEDIA_HOST) is the owner's media.
  for (const [hostFolder, what] of [[false, "the managed volume"], [true, "a host folder"]] as const) {
    test(`the Folders step with /media as ${what}`, async ({ page, api }) => {
      await override(page, api, [
        { method: "GET", path: "/api/v1/setup", body: { ...fresh, mounts: ["/media"], media_host_folder: hostFolder } },
      ]);
      await page.goto("/");
      await page.getByRole("button", { name: "Continue without a key" }).click();
      await expect(page.getByRole("heading", { name: "Where's your media?" })).toBeVisible();
      const warning = page.getByText(/Arrmada is using its own managed storage/);
      const note = page.getByText(/Your media folder is mounted at/);
      if (hostFolder) {
        await expect(note).toBeVisible();
        await expect(warning).toHaveCount(0);
      } else {
        await expect(warning).toBeVisible();
        await expect(note).toHaveCount(0);
      }
    });
  }

  // Ending setup early is its own small link, and it says what's still left first.
  test("finishing setup later from the first step says what's left", async ({ page, api }) => {
    await override(page, api, [
      { method: "GET", path: "/api/v1/setup", body: fresh },
      { method: "POST", path: "/api/v1/setup/complete", body: { status: "ok" } },
    ]);
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Welcome to Arrmada" })).toBeVisible();
    await page.getByRole("button", { name: "Finish setup later" }).click();
    const dialog = page.getByRole("dialog", { name: "Finish setup later?" });
    await expect(dialog.getByText(/The TMDB key, library folders, a media server and indexers aren't set up yet/)).toBeVisible();
    await dialog.getByRole("button", { name: "Keep going" }).click();
    await expect(page.getByRole("heading", { name: "Welcome to Arrmada" })).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/setup/complete")).toHaveLength(0);

    await page.getByRole("button", { name: "Finish setup later" }).click();
    await page.getByRole("dialog", { name: "Finish setup later?" }).getByRole("button", { name: "Finish later" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/setup/complete").length).toBe(1);
  });

  // The optional keys are open under TMDB, Hardcover first; a typed one is checked before
  // it's saved, and the step carries on with only the keys that were typed.
  test("optional keys are offered, checked before saving, and never required", async ({ page, api }) => {
    await override(page, api, [
      { method: "GET", path: "/api/v1/setup", body: fresh },
      { method: "POST", path: "/api/v1/apikeys/hardcover/test", body: { ok: true, detail: "OK: Hardcover accepted the key (signed in as reader)." } },
      { method: "POST", path: "/api/v1/apikeys/opensubtitles_api/test", body: { ok: false, detail: "OpenSubtitles rejected the API key (HTTP 403)" } },
      { method: "PUT", path: "/api/v1/apikeys/hardcover", body: { keys: freshKeys } },
      { method: "PUT", path: "/api/v1/system/library", body: blank },
    ]);
    await page.goto("/");
    const optional = page.getByRole("region", { name: "Optional keys — Arrmada works without them" });
    await expect(optional).toBeVisible();
    await expect(optional.getByText("Recommended")).toHaveCount(1);
    await expect(optional.getByRole("link", { name: /Get a free Hardcover key/ })).toHaveAttribute("href", "https://hardcover.app/account/api");

    await optional.getByLabel("Hardcover", { exact: true }).fill("hc-token");
    await optional.getByLabel("Hardcover", { exact: true }).blur();
    await expect(optional.getByText("✓ OK: Hardcover accepted the key (signed in as reader).")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/apikeys/hardcover/test")[0]?.body).toEqual({ value: "hc-token" });

    // OpenSubtitles is one item: the key with its login, checked when focus leaves it.
    const os = optional.getByRole("group", { name: /^OpenSubtitles/ });
    await os.getByLabel("API key").fill("os-key");
    await os.getByLabel("Username").fill("me");
    await os.getByLabel("Password").fill("pw");
    await optional.getByLabel("Hardcover", { exact: true }).focus();
    await expect(os.getByText("✗ OpenSubtitles rejected the API key (HTTP 403)")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/apikeys/opensubtitles_api/test")[0]?.body).toEqual({ value: "os-key", username: "me", password: "pw" });

    // A turned-down key is only saved if the admin says so; clearing it carries on without it.
    await page.getByRole("button", { name: "Save and continue" }).click();
    const dialog = page.getByRole("dialog", { name: "Save this OpenSubtitles key anyway?" });
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: "Keep editing" }).click();
    for (const f of ["API key", "Username", "Password"]) await os.getByLabel(f).fill("");
    await page.getByRole("button", { name: "Save and continue" }).click();
    await expect(page.getByRole("heading", { name: "Where's your media?" })).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/apikeys").map((c) => c.path)).toEqual(["/api/v1/apikeys/hardcover"]);
    expect(api.callsTo("POST", "/api/v1/apikeys/hardcover/test")).toHaveLength(1);
  });

  test("Run setup again reopens the wizard and comes back to Settings → System", async ({ page }) => {
    await page.goto("/settings/system");
    await page.getByRole("link", { name: "Run setup again" }).click();
    await expect(page).toHaveURL(/\/setup$/);
    await expect(page.getByRole("heading", { name: "Run setup again" })).toBeVisible();
    // What's saved shows masked; nothing is filled in.
    await expect(page.getByText("✓ saved (…c0de)")).toBeVisible();
    await expect(page.getByLabel("Hardcover", { exact: true })).toHaveValue("");
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
