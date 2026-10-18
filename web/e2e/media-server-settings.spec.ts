import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { jellyfinConnected } from "./fixtures/media";
import { jellyfinScan } from "./fixtures/modules";
import type { MockRoute } from "./fixtures/routes";
import type { JellyfinConfig } from "../src/lib/jellyfinApi";

// J5b: Settings → Media servers (it was Settings → Plex): a Plex group and a Jellyfin
// group, each with its server card, watch monitoring, library updates and — for admins —
// sign-in and linked accounts. Jellyfin's group also holds the Watch links address and
// Disconnect. Every old /settings/plex address lands on the same card here.

const NOW_S = Math.floor(Date.now() / 1000);

// serve answers the given endpoints before the default fixtures, recording each call.
async function serve(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method()) continue;
      if (typeof r.path === "string" ? url.pathname !== r.path : !r.path.test(url.pathname)) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      const out = r.respond ? r.respond({ url, params: [], body }) : r.body;
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out ?? {}) });
    }
    return route.fallback();
  });
}

// A connected Jellyfin ("Attic"), recording, whose Movies folder resolves by a mapping.
function connectedJellyfin() {
  let cfg: JellyfinConfig = { ...jellyfinConnected };
  let scan = {
    ...jellyfinScan, configured: true,
    roots: [
      { kind: "movie" as const, arrmada_root: "/movies", sections: ["Films"], plex_path: "/media/movies", how: "mapped" as const },
      { kind: "show" as const, arrmada_root: "/tv", sections: ["Shows"], plex_path: "/tv", how: "direct" as const },
    ],
    path_map: [{ from: "/movies", to: "/media/movies" }],
  };
  const routes: MockRoute[] = [
    { method: "GET", path: "/api/v1/jellyfin", respond: () => cfg },
    { method: "PUT", path: "/api/v1/jellyfin", respond: ({ body }) => {
      const b = body as { url: string; public_url: string };
      cfg = { ...cfg, url: b.url, public_url: b.public_url === b.url ? "" : b.public_url };
      return cfg;
    } },
    { method: "DELETE", path: "/api/v1/jellyfin", respond: () => {
      cfg = { ...cfg, url: "", public_url: "", key_set: false, server_name: "", server_id: "", version: "" };
      return cfg;
    } },
    { method: "PUT", path: "/api/v1/jellyfin/monitoring", respond: ({ body }) => (cfg = { ...cfg, ...(body as Partial<JellyfinConfig>) }) },
    { method: "GET", path: "/api/v1/jellyfin/scan", respond: () => scan },
    { method: "PUT", path: "/api/v1/jellyfin/scan", respond: ({ body }) => (scan = { ...scan, ...(body as object) }) },
    { method: "GET", path: "/api/v1/insights/servers", respond: () => ({ servers: [
      { server: "plex", configured: false, monitoring: false, status: "unconfigured", poll_seconds: 30 },
      { server: "jellyfin", name: "Attic", configured: !!cfg.url, monitoring: cfg.monitoring_enabled, status: !cfg.url ? "unconfigured" : cfg.monitoring_enabled ? "recording" : "off", poll_seconds: cfg.poll_seconds, last_poll_at: NOW_S - 5 },
    ] }) },
  ];
  return routes;
}

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("has a Plex group and a Jellyfin group, each with its own cards", async ({ page, api }) => {
    await page.goto("/settings/media-servers");
    await expect(page.getByRole("navigation", { name: "Settings sections" }).getByRole("link", { name: "Media servers" })).toHaveAttribute("aria-current", "page");
    const plex = page.getByRole("region", { name: "Plex" });
    const jf = page.getByRole("region", { name: "Jellyfin" });
    for (const name of ["Plex server", "Plex watch monitoring", "Plex library updates", "Plex sign-in", "Linked Plex accounts", "Import from Tautulli"]) {
      await expect(plex.getByRole("heading", { name, exact: true })).toBeVisible();
    }
    for (const name of ["Jellyfin server", "Jellyfin Watch links", "Jellyfin watch monitoring", "Jellyfin library updates", "Jellyfin sign-in", "Linked Jellyfin accounts"]) {
      await expect(jf.getByRole("heading", { name, exact: true })).toBeVisible();
    }
    // Nothing connected: each server card sends you to its own card on Connections.
    await expect(plex.getByRole("link", { name: "Connect your Plex server →" })).toHaveAttribute("href", "/settings/connections#plex");
    await expect(jf.getByRole("link", { name: "Connect your Jellyfin server →" })).toHaveAttribute("href", "/settings/connections#jellyfin");
    await expect(jf.getByRole("button", { name: "Disconnect Jellyfin" })).toHaveCount(0);
    await expect(jf.getByText("Connect your Jellyfin server first; the address starts as its URL.")).toBeVisible();
    // Library updates read Jellyfin's own scan settings.
    await expect(jf.locator("#jellyfin-library-updates").getByRole("switch", { name: /Tell Jellyfin to scan after changes/ })).toBeVisible();
    await api.quiet();
    expect(api.callsTo("GET", "/api/v1/jellyfin/scan").length).toBeGreaterThan(0);
  });

  test("a connected Jellyfin: status, Watch links address, monitoring and library updates save themselves", async ({ page, api }) => {
    await serve(page, api, connectedJellyfin());
    await page.goto("/settings/media-servers");
    const server = page.locator("#jellyfin-server");
    await expect(server.getByText("Connected to Attic")).toBeVisible();
    await expect(server.getByText(/Connected · recording what's watched · last answer/)).toBeVisible();
    await expect(server.getByRole("link", { name: "Change the connection →" })).toHaveAttribute("href", "/settings/connections#jellyfin");

    // The Watch links address: prefilled, saved on Enter, warned about when it's home-only.
    const links = page.locator("#jellyfin-watch-links");
    const address = links.getByLabel("Public address for Watch links");
    await expect(address).toHaveValue("https://jellyfin.example.com");
    await expect(links.getByText(/only works on your home network/)).toHaveCount(0);
    await address.fill("http://192.168.1.20:8096");
    await expect(links.getByText(/only works on your home network/)).toBeVisible();
    await address.press("Enter");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/jellyfin").map((c) => c.body)).toEqual([{ url: "http://jellyfin:8096", public_url: "http://192.168.1.20:8096" }]);

    // Monitoring: the switch and the interval each send only their own field.
    const monitoring = page.locator("#jellyfin-monitoring");
    await monitoring.getByRole("switch", { name: /Record what's watched on Jellyfin/ }).click();
    await expect(monitoring.getByText("Saved ✓")).toBeVisible();
    await expect(server.getByText("Connected · watch monitoring is off")).toBeVisible();
    const every = monitoring.getByLabel("Ask Jellyfin every (seconds)");
    await every.fill("90");
    await every.press("Enter");
    await expect(monitoring.getByText("The interval must be between 2 and 60.")).toBeVisible();
    await every.fill("15");
    await every.press("Enter");
    await expect.poll(() => api.callsTo("PUT", "/api/v1/jellyfin/monitoring").map((c) => c.body)).toEqual([{ monitoring_enabled: false }, { poll_seconds: 15 }]);

    // Library updates: where each folder's scans go, and the mappings saved with Save.
    const updates = page.locator("#jellyfin-library-updates");
    await expect(updates.getByText("/media/movies", { exact: true })).toBeVisible();
    await expect(updates.getByText(/library Films \(your mapping\)/)).toBeVisible();
    await updates.getByRole("button", { name: "Add mapping" }).click();
    await updates.getByLabel("Arrmada folder").nth(1).fill("/tv");
    await updates.getByLabel("Jellyfin folder").nth(1).fill("/media/tv");
    await updates.getByRole("button", { name: "Save mappings" }).click();
    await expect(updates.getByText("Saved.")).toBeVisible();
    expect(api.callsTo("PUT", "/api/v1/jellyfin/scan").map((c) => c.body)).toEqual([
      { enabled: true, path_map: [{ from: "/movies", to: "/media/movies" }, { from: "/tv", to: "/media/tv" }] },
    ]);
    // Plex's scan settings weren't touched.
    expect(api.callsTo("PUT", "/api/v1/insights/plex/scan")).toHaveLength(0);
  });

  test("Disconnect asks first, then forgets the server", async ({ page, api }) => {
    await serve(page, api, connectedJellyfin());
    await page.goto("/settings/media-servers");
    const server = page.locator("#jellyfin-server");
    await expect(server.getByText("Connected to Attic")).toBeVisible();

    await server.getByRole("button", { name: "Disconnect Jellyfin" }).click();
    let dialog = page.getByRole("dialog", { name: "Disconnect Attic?" });
    await expect(dialog.getByText(/forgets its address and API key/)).toBeVisible();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toHaveCount(0);
    expect(api.callsTo("DELETE", "/api/v1/jellyfin")).toHaveLength(0);

    await server.getByRole("button", { name: "Disconnect Jellyfin" }).click();
    dialog = page.getByRole("dialog", { name: "Disconnect Attic?" });
    await dialog.getByRole("button", { name: "Disconnect" }).click();
    await expect(page.getByText("Jellyfin disconnected")).toBeVisible();
    expect(api.callsTo("DELETE", "/api/v1/jellyfin")).toHaveLength(1);
    await expect(server.getByRole("link", { name: "Connect your Jellyfin server →" })).toBeVisible();
    await expect(server.getByRole("button", { name: "Disconnect Jellyfin" })).toHaveCount(0);
  });

  test("every old /settings/plex address lands on the same card here", async ({ page }) => {
    await page.goto("/settings/plex");
    await expect(page).toHaveURL(/\/settings\/media-servers$/);
    await expect(page.getByRole("heading", { name: "Plex server", exact: true })).toBeVisible();
    for (const anchor of ["plex-monitoring", "plex-library-updates", "plex-sign-in", "plex-accounts", "plex-tautulli", "jellyfin-sign-in"]) {
      await page.goto(`/settings/plex#${anchor}`);
      await expect(page).toHaveURL(new RegExp(`/settings/media-servers#${anchor}$`));
      await expect(page.locator(`#${anchor}`)).toBeInViewport();
    }
    // The connection card moved to Connections (CFG-16), and still opens there.
    await page.goto("/settings/plex#plex-connection");
    await expect(page).toHaveURL(/\/settings\/connections#plex$/);
  });

  test("the settings search finds Jellyfin's cards", async ({ page }) => {
    await page.goto("/settings/library");
    const search = page.getByRole("searchbox", { name: "Search settings" });
    await search.fill("jellyfin path mapping");
    await page.getByRole("button", { name: /Jellyfin library updates/ }).click();
    await expect(page).toHaveURL(/\/settings\/media-servers#jellyfin-library-updates$/);
    await expect(page.locator("#jellyfin-library-updates")).toBeInViewport();
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("sees both servers' monitoring and library updates, not the admin cards", async ({ page, api }) => {
    await serve(page, api, connectedJellyfin());
    await page.goto("/settings/media-servers");
    await expect(page.getByRole("heading", { name: "Jellyfin server" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Jellyfin watch monitoring" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Jellyfin library updates" })).toBeVisible();
    // The address is shown, not edited: saving it goes through the admin-only connection.
    const links = page.locator("#jellyfin-watch-links");
    await expect(links.getByText("https://jellyfin.example.com")).toBeVisible();
    await expect(links.getByRole("textbox")).toHaveCount(0);
    for (const name of ["Plex sign-in", "Jellyfin sign-in", "Linked Plex accounts", "Linked Jellyfin accounts", "Import from Tautulli"]) {
      await expect(page.getByRole("heading", { name })).toHaveCount(0);
    }
    await expect(page.getByRole("button", { name: "Disconnect Jellyfin" })).toHaveCount(0);
    // The connection is the admin's: no way to it from here.
    await expect(page.locator("#jellyfin-server").getByText("An admin changes the Jellyfin server.")).toBeVisible();
    await expect(page.locator("#jellyfin-server").getByRole("link")).toHaveCount(0);
    await api.quiet();
    expect(api.callsTo("GET", "/api/v1/people")).toHaveLength(0);
  });
});
