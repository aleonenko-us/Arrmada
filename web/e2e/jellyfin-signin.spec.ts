import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { personas } from "./fixtures/users";
import { people as peopleFixture, status } from "./fixtures/system";
import type { MockRoute } from "./fixtures/routes";

// J4: Sign in with Jellyfin on the login page (username + password, and Quick Connect),
// linking Jellyfin from your own account, and the Jellyfin side of People & access. Every
// call is a fixture; no Jellyfin is contacted. Runs at desktop and phone width.

const SHOTS = process.env.J4_SHOTS_DIR;

// installExtra puts fixtures in front of the defaults for one test.
async function installExtra(page: Page, api: MockedApi, extra: MockRoute[]) {
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

async function contrast(page: Page) {
  await page.addStyleTag({ content: "*,*::before,*::after{transition:none!important;animation:none!important}" });
  const res = await new AxeBuilder({ page }).withRules(["color-contrast"]).analyze();
  return res.violations.flatMap((v) => v.nodes.map((n) => `${n.target.join(" ")} — ${n.html.slice(0, 120)}`));
}

test.describe("login page", () => {
  test.use({ persona: "requester" });

  const signedOut = (quickconnect: boolean, plex = true): MockRoute[] => [
    { method: "GET", path: "/api/v1/auth/me", status: 401, body: { status: "error", message: "authentication required" } },
    { method: "GET", path: "/api/v1/status", body: { ...status(personas.requester), authenticated: false, plex_login: plex, jellyfin_login: true, jellyfin_quickconnect: quickconnect } },
  ];

  test("signs in with a Jellyfin username and password", async ({ page, api }) => {
    let tries = 0;
    await installExtra(page, api, [
      ...signedOut(true),
      { method: "POST", path: "/api/v1/auth/jellyfin", body: { user: personas.requester.user } },
    ]);
    // The first try is a wrong password: a 401.
    await page.route("**/api/v1/auth/jellyfin", async (route) => {
      if (tries === 0) {
        tries++;
        api.calls.push({ method: "POST", path: "/api/v1/auth/jellyfin", body: route.request().postDataJSON() });
        return route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ status: "error", message: "Wrong Jellyfin username or password." }) });
      }
      return route.fallback();
    });
    await page.addInitScript(() => { if (!sessionStorage.getItem("e2e-once")) { sessionStorage.setItem("e2e-once", "1"); sessionStorage.setItem("arrmada.next", "/requests"); } });
    await page.goto("/");
    await expect(page.getByRole("button", { name: "Sign in with Plex" })).toBeVisible();
    await page.getByRole("button", { name: "Sign in with Jellyfin" }).click();
    const sheet = page.getByRole("dialog", { name: "Sign in with Jellyfin" });
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole("button", { name: "Use Quick Connect" })).toBeVisible();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/jellyfin-sheet-${page.viewportSize()?.width}.png` });
    await sheet.getByLabel("Jellyfin username").fill("kid");
    await sheet.getByLabel("Password").fill("not-it");
    await sheet.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(sheet.getByRole("alert")).toHaveText("Wrong Jellyfin username or password.");
    await sheet.getByLabel("Password").fill("kid-password");
    await sheet.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page).toHaveURL(/\/requests$/, { timeout: 10_000 });
    const posts = api.callsTo("POST", "/api/v1/auth/jellyfin").filter((c) => c.path === "/api/v1/auth/jellyfin");
    expect(posts.map((c) => c.body)).toEqual([{ username: "kid", password: "not-it" }, { username: "kid", password: "kid-password" }]);
  });

  test("signs in with Quick Connect: shows the code, then finishes once it's approved", async ({ page, api }) => {
    let polls = 0;
    await installExtra(page, api, [
      ...signedOut(true, false),
      { method: "POST", path: "/api/v1/auth/jellyfin/quickconnect", body: { id: "qc-abc", code: "482913" } },
      { method: "GET", path: "/api/v1/auth/jellyfin/quickconnect/qc-abc", respond: () => (++polls < 2 ? { pending: true } : { user: personas.requester.user }) },
    ]);
    await page.addInitScript(() => { if (!sessionStorage.getItem("e2e-once")) { sessionStorage.setItem("e2e-once", "1"); sessionStorage.setItem("arrmada.next", "/requests"); } });
    await page.goto("/");
    // Plex sign-in off: only the Jellyfin button under "or".
    await expect(page.getByRole("button", { name: "Sign in with Plex" })).toHaveCount(0);
    await page.getByRole("button", { name: "Sign in with Jellyfin" }).click();
    const sheet = page.getByRole("dialog", { name: "Sign in with Jellyfin" });
    await sheet.getByRole("button", { name: "Use Quick Connect" }).click();
    await expect(sheet.getByText("482913")).toBeVisible();
    await expect(sheet.getByText("Enter this code in a Jellyfin app under Quick Connect.")).toBeVisible();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/jellyfin-quickconnect-${page.viewportSize()?.width}.png` });
    await expect(page).toHaveURL(/\/requests$/, { timeout: 10_000 });
    expect(polls).toBeGreaterThanOrEqual(2);
  });

  test("Quick Connect stays hidden when it's off, and the sheet reads in both themes", async ({ page, api }) => {
    await installExtra(page, api, signedOut(false));
    for (const theme of ["dark", "light"]) {
      await page.addInitScript((t) => localStorage.setItem("arrmada.theme", t), theme);
      await page.goto("/");
      await expect(page.getByRole("button", { name: "Sign in with Jellyfin" })).toBeVisible();
      if (SHOTS && theme === "dark") await page.screenshot({ path: `${SHOTS}/login-${page.viewportSize()?.width}.png` });
      expect.soft(await contrast(page), `login page (${theme})`).toEqual([]);
      await page.getByRole("button", { name: "Sign in with Jellyfin" }).click();
      const sheet = page.getByRole("dialog", { name: "Sign in with Jellyfin" });
      await expect(sheet.getByLabel("Jellyfin username")).toBeVisible();
      await expect(sheet.getByRole("button", { name: "Use Quick Connect" })).toHaveCount(0);
      expect.soft(await contrast(page), `Jellyfin sheet (${theme})`).toEqual([]);
      // Nothing sideways at phone width.
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    }
  });
});

test.describe("your account", () => {
  test.use({ persona: "requester" });

  test("links Jellyfin with a password and unlinks it after a confirm", async ({ page, api }) => {
    let linked = false;
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/me/jellyfin", respond: () => (linked
        ? { linked: true, jellyfin_username: "kidjf", can_unlink: true, available: true, quickconnect: true }
        : { linked: false, can_unlink: false, available: true, quickconnect: true }) },
      { method: "POST", path: "/api/v1/me/jellyfin/link", respond: () => { linked = true; return { linked: true, jellyfin_username: "kidjf" }; } },
      { method: "DELETE", path: "/api/v1/me/jellyfin/link", respond: () => { linked = false; return { linked: false, can_unlink: false }; } },
    ]);
    await page.goto("/me");
    const servers = page.getByRole("region", { name: "Media servers" });
    await expect(servers.getByText("Plex account:")).toBeVisible();
    await expect(servers.getByText("Jellyfin:")).toBeVisible();
    await servers.getByRole("button", { name: "Link" }).nth(1).click();
    const sheet = page.getByRole("dialog", { name: "Link Jellyfin" });
    await sheet.getByLabel("Jellyfin username").fill("kidjf");
    await sheet.getByLabel("Password").fill("pw-1");
    await sheet.getByRole("button", { name: "Link", exact: true }).click();
    await expect(sheet).toHaveCount(0);
    await expect(servers.getByText("Linked as kidjf")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/me/jellyfin/link")[0].body).toEqual({ username: "kidjf", password: "pw-1" });
    await servers.getByRole("button", { name: "Unlink" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Unlink" }).click();
    await expect(servers.getByRole("button", { name: "Link" }).nth(1)).toBeVisible();
    expect(api.callsTo("DELETE", "/api/v1/me/jellyfin/link").length).toBe(1);
  });

  test("leaves the Jellyfin line out when no Jellyfin is connected", async ({ page }) => {
    await page.goto("/me");
    const servers = page.getByRole("region", { name: "Media servers" });
    await expect(servers.getByText("Plex account:")).toBeVisible();
    await expect(servers.getByText("Jellyfin:")).toHaveCount(0);
  });
});

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("a Jellyfin user held by a duplicate requester moves here by Quick Connect and a merge", async ({ page, api }) => {
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/me/jellyfin", body: { linked: false, can_unlink: false, available: true, quickconnect: true } },
      { method: "POST", path: "/api/v1/me/jellyfin/link/quickconnect", body: { id: "qc-1", code: "111222" } },
      { method: "GET", path: "/api/v1/me/jellyfin/link/quickconnect/qc-1", status: 409, body: { status: "error", message: "This Jellyfin user is already linked to ownerjf.", already_linked_to: "ownerjf", user_id: 9, mergeable: true } },
      { method: "GET", path: "/api/v1/users/1/merge", body: {
        from: "ownerjf", to: "admiral", plex_link: false, jellyfin_link: true, jellyfin_username: "ownerjf", requests: 3, following: 0, notifications: 0,
        push_devices: 0, quota_usage: 0, audiobook_progress: false, audiobook_password: false, signed_in_devices: 1, listening_apps: 0,
      } },
      { method: "POST", path: "/api/v1/users/1/merge", body: { merged: true } },
    ]);
    await page.goto("/me");
    const servers = page.getByRole("region", { name: "Media servers" });
    await servers.getByRole("button", { name: "Link" }).nth(1).click();
    const sheet = page.getByRole("dialog", { name: "Link Jellyfin" });
    await sheet.getByRole("button", { name: "Use Quick Connect" }).click();
    await expect(sheet.getByText("111222")).toBeVisible();
    await expect(servers.getByText("Already linked to ownerjf.")).toBeVisible({ timeout: 10_000 });
    await servers.getByRole("button", { name: "Move link here" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("3 requests, the Jellyfin link (ownerjf)", { exact: false })).toBeVisible();
    await dialog.getByRole("button", { name: "Back up and merge" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/users/1/merge").length).toBe(1);
    expect(api.callsTo("POST", "/api/v1/users/1/merge")[0].body).toEqual({ from_user_id: 9 });
  });

  test("People & access shows both links and unlinks or blocks the Jellyfin one", async ({ page, api }) => {
    const people = peopleFixture.people.map((p) => (p.plex_linked ? { ...p, jellyfin_linked: true, jellyfin_username: "grandadjf" } : p));
    const grandad = people.find((p) => p.jellyfin_linked)!;
    await installExtra(page, api, [
      { method: "GET", path: "/api/v1/people", body: { people } },
      { method: "POST", path: `/api/v1/users/${grandad.id}/block-jellyfin`, body: { blocks: [{ jellyfin_id: "jf-g", name: grandad.username, at: "2026-10-14T00:00:00Z" }] } },
    ]);
    await page.goto("/settings/users");
    const row = page.getByTestId("person").filter({ hasText: grandad.username });
    await expect(row.getByText("Plex · grandadplex")).toBeVisible();
    await expect(row.getByText("Jellyfin · grandadjf")).toBeVisible();
    await row.getByRole("button", { name: "More…" }).click();
    await expect(row.getByText("Linked to Jellyfin as grandadjf.")).toBeVisible();
    await row.getByRole("button", { name: "Unlink Jellyfin" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Unlink" }).click();
    await expect.poll(() => api.callsTo("PUT", `/api/v1/users/${grandad.id}`).map((c) => c.body)).toEqual([{ jellyfin_unlink: true }]);
    await row.getByRole("button", { name: "Block their Jellyfin user" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Block" }).click();
    await expect.poll(() => api.callsTo("POST", `/api/v1/users/${grandad.id}/block-jellyfin`).length).toBe(1);
  });
});
