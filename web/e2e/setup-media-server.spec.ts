import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import { freshKeys, settings, setup as configuredSetup } from "./fixtures/system";
import { jellyfinSave, jellyfinTest } from "./fixtures/media";

// J5b: the setup wizard's third step is "Media server" — Plex, Jellyfin, Both or Later.
// Each chosen server shows its own connection editor and its own "Let family sign in"
// checkbox; Jellyfin's address and key are tested before they're saved. Runs at 1440 px
// (desktop) and 375 px (phone): nothing on the step scrolls sideways.

const blank = { movies: "", tv: "", ebooks: "", audiobooks: "", music: "", downloads: "" };
const fresh = {
  ...configuredSetup, needed: true, complete: false, tmdb_configured: false, libraries_chosen: false, keys: freshKeys,
  library: blank, running: blank, suggestions: {},
  plex_connected: false, plex_server: "", plex_family: { enabled: false, auto_approve_types: "" },
  jellyfin_connected: false, jellyfin_server: "", jellyfin_family: { enabled: false, auto_approve_types: "" },
  indexers_enabled: 0, prowlarr_available: false,
};

const overflow = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

// serve answers the given endpoints before the default fixtures, recording each call.
async function serve(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method() || url.pathname !== r.path) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      const out = r.respond ? r.respond({ url, params: [], body }) : r.body;
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out ?? {}) });
    }
    return route.fallback();
  });
}

// toMediaStep opens a fresh install's wizard and walks Keys and Folders without saving.
async function toMediaStep(page: Page, api: MockedApi, extra: MockRoute[] = []) {
  await serve(page, api, [
    ...extra,
    { method: "GET", path: "/api/v1/setup", body: fresh },
    { method: "PUT", path: "/api/v1/system/library", body: blank },
    { method: "PUT", path: "/api/v1/settings", respond: ({ body }) => ({ ...settings, ...(body as object) }) },
  ]);
  await page.goto("/");
  await page.getByRole("button", { name: "Continue without a key" }).click();
  await page.getByRole("button", { name: "Save and continue" }).click();
  await page.getByRole("dialog", { name: "Continue without library folders?" }).getByRole("button", { name: "Continue without them" }).click();
  await expect(page.getByRole("heading", { name: "Connect your media server" })).toBeVisible();
}

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("the step offers Plex, Jellyfin, Both and Later, each showing only its own editors", async ({ page, api }) => {
    await toMediaStep(page, api);
    await expect(page.getByRole("list", { name: "Setup steps" })).toContainText("3. Media server");
    const choice = page.getByRole("group", { name: "Which media server do you use?" });
    for (const name of ["Plex", "Jellyfin", "Both", "Later"]) {
      await expect(choice.getByRole("radio", { name: new RegExp(`^${name}`) })).not.toBeChecked();
    }
    // Nothing picked yet: no editor, and the step can be skipped.
    await expect(page.getByRole("checkbox", { name: /Let family sign in/ })).toHaveCount(0);
    await expect(page.getByText("Skip this step")).toBeVisible();
    await api.quiet();
    expect(await overflow(page)).toBeLessThanOrEqual(0);

    await choice.getByText("Plex", { exact: true }).click();
    await expect(page.getByRole("button", { name: "Sign in with Plex" })).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Let family sign in with Plex" })).not.toBeChecked();
    await expect(page.getByRole("form", { name: "Jellyfin connection" })).toHaveCount(0);
    await expect(page.getByText("Skip this step")).toHaveCount(0);

    await choice.getByText("Jellyfin", { exact: true }).click();
    const jf = page.getByRole("form", { name: "Jellyfin connection" });
    await expect(jf.getByLabel("Server URL")).toBeVisible();
    await expect(jf.getByLabel("API key")).toBeVisible();
    await expect(jf.getByLabel("Public address for Watch links")).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Let family sign in with Jellyfin" })).not.toBeChecked();
    await expect(page.getByRole("button", { name: "Sign in with Plex" })).toHaveCount(0);
    // The wizard covers the rest itself: no link off to Settings from inside the step.
    await expect(jf.getByRole("link", { name: "Settings → Media servers →" })).toHaveCount(0);

    await choice.getByText("Both", { exact: true }).click();
    await expect(page.getByRole("checkbox", { name: "Let family sign in with Plex" })).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Let family sign in with Jellyfin" })).toBeVisible();
    await page.getByRole("checkbox", { name: "Let family sign in with Jellyfin" }).check();
    await expect(page.getByRole("group", { name: "Auto-approve their requests (Jellyfin)" })).toBeVisible();
    await api.quiet();
    expect(await overflow(page)).toBeLessThanOrEqual(0);

    await choice.getByText("Later", { exact: true }).click();
    await expect(page.getByText(/Nothing to do here for now/)).toBeVisible();
    await expect(page.getByRole("checkbox", { name: /Let family sign in/ })).toHaveCount(0);
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Where releases come from" })).toBeVisible();
    // Later saves nothing.
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0);
    expect(api.callsTo("PUT", "/api/v1/jellyfin")).toHaveLength(0);
  });

  test("Jellyfin: the address and key are tested before they're saved, and its own family choices are saved", async ({ page, api }) => {
    await toMediaStep(page, api, [
      { method: "POST", path: "/api/v1/jellyfin/test", respond: ({ body }) => jellyfinTest(body as { url?: string }) },
      { method: "PUT", path: "/api/v1/jellyfin", respond: ({ body }) => jellyfinSave(body as Parameters<typeof jellyfinSave>[0]) },
    ]);
    await page.getByRole("group", { name: "Which media server do you use?" }).getByText("Jellyfin", { exact: true }).click();
    const jf = page.getByRole("form", { name: "Jellyfin connection" });

    // An address that doesn't answer: asked before saving, and nothing saved on Keep editing.
    await jf.getByLabel("Server URL").fill("http://unreachable:8096");
    await jf.getByLabel("API key").fill("jf-secret-key");
    await jf.getByRole("button", { name: "Save" }).click();
    const ask = page.getByRole("dialog", { name: "Save it anyway?" });
    await expect(ask.getByText(/connection refused/)).toBeVisible();
    await ask.getByRole("button", { name: "Keep editing" }).click();
    expect(api.callsTo("PUT", "/api/v1/jellyfin")).toHaveLength(0);

    // One that answers: tested, then saved; the key field empties and never shows the key.
    await jf.getByLabel("Server URL").fill("http://jellyfin:8096");
    await jf.getByRole("button", { name: "Save" }).click();
    await expect(jf.getByText("✓ Connected to Attic · Jellyfin 10.10.3")).toBeVisible();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/jellyfin").length).toBe(1);
    expect(api.callsTo("POST", "/api/v1/jellyfin/test").map((c) => c.body)).toEqual([
      { url: "http://unreachable:8096", api_key: "jf-secret-key" },
      { url: "http://jellyfin:8096", api_key: "jf-secret-key" },
    ]);
    expect(api.callsTo("PUT", "/api/v1/jellyfin")[0].body).toMatchObject({ url: "http://jellyfin:8096", api_key: "jf-secret-key" });
    await expect(jf.getByLabel("API key")).toHaveValue("");
    await expect(page.getByText("jf-secret-key")).toHaveCount(0);

    // Its own "Let family sign in", with the types ticked.
    await page.getByRole("checkbox", { name: "Let family sign in with Jellyfin" }).check();
    await page.getByRole("group", { name: "Auto-approve their requests (Jellyfin)" }).getByRole("checkbox", { name: "Movies" }).check();
    await page.getByRole("button", { name: "Save and continue" }).click();
    await expect(page.getByRole("heading", { name: "Where releases come from" })).toBeVisible();
    // Only Jellyfin's switches: Plex wasn't chosen, so its stay as they were.
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([
      { jellyfin_login_enabled: true, jellyfin_login_auto_approve_types: "movie" },
    ]);
  });

  test("Both saves each server's family choices", async ({ page, api }) => {
    await toMediaStep(page, api);
    await page.getByRole("group", { name: "Which media server do you use?" }).getByText("Both", { exact: true }).click();
    await page.getByRole("checkbox", { name: "Let family sign in with Plex" }).check();
    await page.getByRole("button", { name: "Save and continue" }).click();
    await expect(page.getByRole("heading", { name: "Where releases come from" })).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([
      { plex_login_enabled: true, plex_login_auto_approve_types: "" },
      { jellyfin_login_enabled: false, jellyfin_login_auto_approve_types: "" },
    ]);
  });

  test("finishing later names a media server among what's left", async ({ page, api }) => {
    await serve(page, api, [{ method: "GET", path: "/api/v1/setup", body: fresh }]);
    await page.goto("/");
    await page.getByRole("button", { name: "Finish setup later" }).click();
    const dialog = page.getByRole("dialog", { name: "Finish setup later?" });
    await expect(dialog.getByText(/The TMDB key, library folders, a media server and indexers aren't set up yet/)).toBeVisible();
    await dialog.getByRole("button", { name: "Keep going" }).click();
  });
});
