import { test, expect } from "./mockApi";
import { personas } from "./fixtures/users";
import { settings, status } from "./fixtures/system";
import { plexConfig } from "./fixtures/modules";
import type { MockRoute } from "./fixtures/routes";

// PLEX-06/07/14/15/17, APP-04: the Plex group of Settings → Media servers, Sign in with Plex's popup and redirect paths,
// linking your own Plex account and merging a duplicate into it. plex.tv is never contacted:
// app.plex.tv answers from a stub, and every PIN call is a fixture.

const PLEX_STUB = "<!doctype html><title>Plex</title><p>Plex sign-in (e2e stub)</p>";

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("Settings → Media servers holds everything about using Plex, and links to the connection", async ({ page }) => {
    await page.goto("/settings/media-servers");
    for (const name of ["Plex server", "Plex watch monitoring", "Plex library updates", "Plex sign-in", "Linked Plex accounts", "Import from Tautulli"]) {
      await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
    }
    // The connection itself is the Plex card on Connections.
    await expect(page.locator("#plex-server").getByRole("link", { name: "Connect your Plex server →" })).toHaveAttribute("href", "/settings/connections#plex");
    await expect(page.locator("#plex-tautulli").getByRole("link", { name: "Open the Tautulli import →" })).toHaveAttribute("href", "/settings/import#tautulli-import");
    // Insights' header links here.
    await page.goto("/insights");
    await page.getByRole("link", { name: "Media server settings →" }).click();
    await expect(page).toHaveURL(/\/settings\/media-servers$/);
  });

  test("watch monitoring saves itself, sending only what changed", async ({ page, api }) => {
    let cfg = { ...plexConfig, url: "http://plex.lan:32400", token_set: true, enabled: true, enabled_set: true, status: "recording" as const, server_name: "Basement" };
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/insights/plex", respond: () => cfg },
      { method: "PUT", path: "/api/v1/insights/plex/monitoring", respond: ({ body }) => {
        const b = body as { enabled?: boolean; poll_seconds?: number };
        cfg = { ...cfg, ...b, status: b.enabled === false ? "off" as const : cfg.status } as typeof cfg;
        return cfg;
      } },
    ]);
    await page.goto("/settings/media-servers");
    const server = page.locator("#plex-server");
    await expect(server.getByText("Connected to Basement")).toBeVisible();
    await expect(server.getByText("Connected · recording what's watched")).toBeVisible();

    const monitoring = page.locator("#plex-monitoring");
    await monitoring.getByRole("switch", { name: /Record what's watched on Plex/ }).click();
    await expect(monitoring.getByText("Saved ✓")).toBeVisible();
    await expect(server.getByText("Connected · watch monitoring is off")).toBeVisible();

    const every = monitoring.getByLabel("Ask Plex every (seconds)");
    await every.fill("90");
    await every.press("Enter");
    await expect(monitoring.getByText("The interval must be between 2 and 60.")).toBeVisible();
    await every.fill("12");
    await every.press("Enter");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/insights/plex/monitoring").length).toBe(2);
    // Never the address or the token: those belong to the connection card (the admin's).
    expect(api.callsTo("PUT", "/api/v1/insights/plex/monitoring").map((c) => c.body)).toEqual([{ enabled: false }, { poll_seconds: 12 }]);
    expect(api.callsTo("PUT", "/api/v1/insights/plex").filter((c) => c.path === "/api/v1/insights/plex")).toHaveLength(0);
  });

  test("linked Plex accounts are listed and can be unlinked after a confirm", async ({ page, api }) => {
    await page.goto("/settings/media-servers");
    const accounts = page.locator("#plex-accounts");
    const row = accounts.getByTestId("plex-account").filter({ hasText: "Grandad" });
    await expect(row.getByText("Plex · grandadplex")).toBeVisible();
    await expect(accounts.getByTestId("plex-account")).toHaveCount(1);
    await row.getByRole("button", { name: "Unlink Grandad's Plex account" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("Unlink Grandad's Plex account (grandadplex)?")).toBeVisible();
    await dialog.getByRole("button", { name: "Unlink" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/users/5").map((c) => c.body)).toEqual([{ plex_unlink: true }]);
    await expect(accounts.getByRole("link", { name: "Settings → People & access" })).toHaveAttribute("href", "/settings/users");
  });

  test("links Plex from the sidebar in a popup that closes itself when done", async ({ page, api }) => {
    await page.context().route(/^https:\/\/app\.plex\.tv\//, (r) => r.fulfill({ contentType: "text/html", body: PLEX_STUB }));
    let polls = 0;
    let linked = false;
    const extra: MockRoute[] = [
      { method: "POST", path: "/api/v1/me/plex/link", body: { id: 5, auth_url: "https://app.plex.tv/auth#?code=X" } },
      { method: "GET", path: "/api/v1/me/plex/link/5", respond: () => (++polls < 2 ? { pending: true } : ((linked = true), { linked: true, plex_username: "ownerplex" })) },
      { method: "GET", path: "/api/v1/me/plex", respond: () => (linked ? { linked: true, plex_username: "ownerplex", can_unlink: true } : { linked: false, can_unlink: false }) },
    ];
    await installExtra(page, api, extra);

    await page.goto("/settings/media-servers");
    const sidebar = page.getByRole("complementary");
    const popupOpened = page.waitForEvent("popup");
    await sidebar.getByRole("button", { name: "Link", exact: true }).click();
    const popup = await popupOpened;
    await expect(popup).toHaveURL(/app\.plex\.tv\/auth/);
    await expect(sidebar.getByText("Linked as ownerplex")).toBeVisible({ timeout: 10_000 });
    await expect.poll(() => popup.isClosed()).toBe(true);
    expect(api.callsTo("POST", "/api/v1/me/plex/link")[0].path).toBe("/api/v1/me/plex/link"); // popup mode: no ?mode=redirect
  });

  test("a Plex account linked to a duplicate requester can be moved here, after a preview", async ({ page, api }) => {
    await page.context().route(/^https:\/\/app\.plex\.tv\//, (r) => r.fulfill({ contentType: "text/html", body: PLEX_STUB }));
    await installExtra(page, api, [
      { method: "POST", path: "/api/v1/me/plex/link", body: { id: 6, auth_url: "https://app.plex.tv/auth#?code=Y" } },
      { method: "GET", path: "/api/v1/me/plex/link/6", status: 409, body: { status: "error", message: "This Plex account is already linked to ownerplex.", already_linked_to: "ownerplex", user_id: 9, mergeable: true } },
      { method: "GET", path: "/api/v1/users/1/merge", body: {
        from: "ownerplex", to: "admiral", plex_link: true, plex_username: "ownerplex", jellyfin_link: false, requests: 12, following: 0, notifications: 4, push_devices: 3,
        quota_usage: 0, audiobook_progress: true, audiobook_password: false, signed_in_devices: 1, listening_apps: 0,
      } },
      { method: "POST", path: "/api/v1/users/1/merge", body: { merged: true } },
    ]);
    await page.goto("/settings/media-servers");
    const sidebar = page.getByRole("complementary");
    const popupOpened = page.waitForEvent("popup");
    await sidebar.getByRole("button", { name: "Link", exact: true }).click();
    await popupOpened;
    await expect(sidebar.getByText("Already linked to ownerplex.")).toBeVisible({ timeout: 10_000 });

    await sidebar.getByRole("button", { name: "Move link here" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("12 requests, 4 inbox messages, 3 phones and browsers getting alerts, audiobook progress, the Plex link (ownerplex)", { exact: false })).toBeVisible();
    await dialog.getByRole("button", { name: "Back up and merge" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/users/1/merge").length).toBe(1);
    expect(api.callsTo("POST", "/api/v1/users/1/merge")[0].body).toEqual({ from_user_id: 9 });
    expect(api.callsTo("GET", "/api/v1/users/1/merge")[0].path).toBe("/api/v1/users/1/merge?from=9");
  });

  test("the staff sign-in policy carries a warning and saves itself", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "PUT", path: "/api/v1/settings", respond: ({ body }) => ({ ...settings, ...(body as object) }) },
    ]);
    await page.goto("/settings/media-servers");
    await page.getByRole("switch", { name: /Let staff sign in with Plex/ }).click();
    await expect(page.getByText(/Anyone who controls a linked Plex account gets that account/)).toBeVisible();
    await expect(page.getByText("Saved ✓")).toBeVisible();
    await expect(page.getByRole("button", { name: "Save settings" })).toHaveCount(0);
    expect(api.callsTo("PUT", "/api/v1/settings").length).toBe(1);
    // Only the key that changed is sent.
    expect(api.callsTo("PUT", "/api/v1/settings")[0].body).toEqual({ plex_signin_staff: true });
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("sees the Plex server, monitoring and library updates but not the admin cards", async ({ page, api }) => {
    await page.goto("/settings/media-servers");
    await expect(page.getByRole("heading", { name: "Plex server" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Plex watch monitoring" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Plex library updates" })).toBeVisible();
    // Sign-in, linked accounts and the Tautulli import are an admin's.
    await expect(page.getByRole("heading", { name: "Plex sign-in" })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Linked Plex accounts" })).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Import from Tautulli" })).toHaveCount(0);
    // The connection is the admin's: no way to it from here.
    const server = page.locator("#plex-server");
    await expect(server.getByText(/An admin (connects|changes) the Plex server\./)).toBeVisible();
    await expect(server.getByRole("link")).toHaveCount(0);
    await api.quiet();
    expect(api.callsTo("GET", "/api/v1/people")).toHaveLength(0);
  });
});

// A requester links Plex from the Me page, which is also where plex.tv sends a
// redirect-mode link back to (/me?plexlink=).
test.describe("requester", () => {
  test.use({ persona: "requester" });

  test("the Me page offers to link Plex", async ({ page }) => {
    await page.goto("/me");
    await expect(page.getByRole("region", { name: "Media servers" }).getByRole("button", { name: "Link", exact: true })).toBeVisible();
  });

  test("finishes a link plex.tv sent back to the Me page, for the PIN this tab started", async ({ page, api }) => {
    let linked = false;
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/me/plex/link/7", respond: () => ((linked = true), { linked: true, plex_username: "kidplex" }) },
      { method: "GET", path: "/api/v1/me/plex", respond: () => (linked ? { linked: true, plex_username: "kidplex", can_unlink: true } : { linked: false, can_unlink: false }) },
    ]);
    await page.addInitScript(() => { if (!sessionStorage.getItem("e2e-once")) { sessionStorage.setItem("e2e-once", "1"); sessionStorage.setItem("arrmada.plexpin.link", "7"); } });
    await page.goto("/me?plexlink=7");
    await expect(page.getByRole("region", { name: "Media servers" }).getByText("Linked as kidplex")).toBeVisible({ timeout: 10_000 });
    await expect(page).toHaveURL(/\/me$/);
    expect(api.callsTo("GET", "/api/v1/me/plex/link/7").length).toBe(1);
  });
});

// Signed out, with Sign in with Plex on. The login page is what plex.tv sends a
// redirect-mode sign-in back to.
test.describe("login page", () => {
  test.use({ persona: "requester" });

  const signedOut = (polls: { n: number }): MockRoute[] => [
    { method: "GET", path: "/api/v1/auth/me", status: 401, body: { status: "error", message: "authentication required" } },
    { method: "GET", path: "/api/v1/status", body: { ...status(personas.requester), authenticated: false, plex_login: true } },
    { method: "GET", path: /^\/api\/v1\/auth\/plex\/pin\/(\d+)$/, respond: () => { polls.n++; return { user: personas.requester.user }; } },
  ];

  test("finishes a sign-in plex.tv sent back here, for the PIN this tab started", async ({ page, api }) => {
    const polls = { n: 0 };
    await installExtra(page, api, signedOut(polls));
    await page.addInitScript(() => { if (!sessionStorage.getItem("e2e-once")) { sessionStorage.setItem("e2e-once", "1"); sessionStorage.setItem("arrmada.plexpin.login", "7"); sessionStorage.setItem("arrmada.next", "/requests"); } });
    await page.goto("/?plexpin=7");
    // Signed in: the app reloads where it was going (this fixture's /me still says no
    // session, so the login form shows there).
    await expect(page).toHaveURL(/\/requests$/, { timeout: 10_000 });
    expect(polls.n).toBeGreaterThan(0);
    expect(api.callsTo("GET", "/api/v1/auth/plex/pin/7").length).toBeGreaterThan(0);
  });

  test("ignores a PIN in a link this tab didn't start", async ({ page, api }) => {
    const polls = { n: 0 };
    await installExtra(page, api, signedOut(polls));
    await page.goto("/?plexpin=99");
    await expect(page.getByRole("button", { name: "Sign in with Plex" })).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
    expect(polls.n).toBe(0);
  });

  test("goes by redirect when the popup is blocked", async ({ page, api }) => {
    await installExtra(page, api, [
      ...signedOut({ n: 0 }),
      { method: "POST", path: "/api/v1/auth/plex/pin", body: { id: 8, auth_url: "https://app.plex.tv/auth#?code=Z&forwardUrl=x" } },
    ]);
    await page.context().route(/^https:\/\/app\.plex\.tv\//, (r) => r.fulfill({ contentType: "text/html", body: PLEX_STUB }));
    await page.addInitScript(() => { window.open = () => null; });
    await page.goto("/");
    await page.getByRole("button", { name: "Sign in with Plex" }).click();
    await expect(page).toHaveURL(/app\.plex\.tv\/auth/);
    expect(api.callsTo("POST", "/api/v1/auth/plex/pin")[0].path).toBe("/api/v1/auth/plex/pin?mode=redirect");
  });
});

// installExtra puts fixtures in front of the defaults for one test (the default table is
// installed per test by mockApi; a later page.route takes precedence).
async function installExtra(page: import("@playwright/test").Page, api: import("./mockApi").MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method()) continue;
      const m = typeof r.path === "string" ? (url.pathname === r.path ? [] : null) : url.pathname.match(r.path);
      if (!m) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      const out = r.respond ? r.respond({ url, params: m.slice(1), body }) : r.body;
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out ?? {}) });
    }
    return route.fallback();
  });
}
