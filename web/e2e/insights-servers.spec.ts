import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import * as ins from "./fixtures/insights";
import type { InsightsServerStatus } from "../src/lib/api";

// Insights with Plex and Jellyfin both connected (J2): server chips in the address narrow
// every tab, each stream, play and person names its server, Jellyfin's configured hardware
// is a plain "HW (vaapi)" (never a fallback), and Reliability says Jellyfin reports no
// buffering. With only Plex, none of it shows.

const SHOTS = process.env.J2_SHOTS_DIR;

async function serveBoth(page: Page, jellyfin: InsightsServerStatus["status"] = "recording") {
  const table = ins.routes(() => ins.config("recording"), { jellyfin });
  const puts: unknown[] = [];
  // seen: every Insights read this answered (path + query); it wins over the default mock.
  const seen: string[] = [];
  await page.route((u) => u.pathname.startsWith("/api/v1/insights/") || u.pathname === "/api/v1/jellyfin/monitoring", async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    if (req.method() === "PUT" && url.pathname === "/api/v1/jellyfin/monitoring") {
      puts.push(req.postDataJSON());
      return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
    }
    seen.push(url.pathname + url.search);
    const r = table.find((t) => t.method === req.method() && t.path === url.pathname);
    if (!r) return route.fallback();
    const out = r.respond ? r.respond({ url, params: [], body: undefined }) : r.body;
    return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out) });
  });
  return { puts, seen };
}

test.describe("two media servers", () => {
  test.use({ persona: "admin" });

  test("Activity lists both servers' streams with badges, and the chips narrow it", async ({ page }) => {
    const { seen } = await serveBoth(page);
    await page.goto("/insights");
    const chips = page.getByRole("button", { name: "Jellyfin", exact: true });
    await expect(page.getByRole("button", { name: "All", exact: true })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByText("3 streams active")).toBeVisible();
    const jf = page.getByRole("button", { name: /Northern Lights/ });
    await expect(jf.getByText("Jellyfin", { exact: true })).toBeVisible();
    await expect(jf.getByText("HW (vaapi)")).toBeVisible();
    await expect(jf.getByText(/fell back/)).toHaveCount(0);
    await expect(page.getByRole("button", { name: /The Cartographer/ }).getByText("Plex", { exact: true })).toBeVisible();
    // Both monitoring badges, each with its server.
    await expect(page.getByText("Plex: ", { exact: false }).first()).toBeAttached();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/insights-activity-both.png`, fullPage: true });

    await chips.click();
    await expect(page).toHaveURL(/server=jellyfin/);
    await expect(page.getByText("1 stream active")).toBeVisible();
    await expect(page.getByRole("button", { name: /The Cartographer/ })).toHaveCount(0);
    expect(seen.some((p) => p.startsWith("/api/v1/insights/activity?server=jellyfin"))).toBeTruthy();

    // The deep dive: the configured hardware, no fallback chip.
    await page.getByRole("button", { name: /Northern Lights/ }).click();
    const dive = page.getByRole("dialog", { name: /Northern Lights/ });
    await expect(dive.getByText("HW (vaapi)", { exact: true })).toBeVisible();
    await expect(dive.getByText("Fell back to CPU")).toHaveCount(0);
  });

  test("History names each play's server; the filter reaches the API and the export", async ({ page }) => {
    const { seen } = await serveBoth(page);
    await page.goto("/insights?tab=history");
    const robin = page.getByRole("row", { name: /Northern Lights/ });
    await expect(robin.getByText("Jellyfin", { exact: true })).toBeVisible();
    await expect(page.getByRole("row", { name: /Harbour Lights/ }).getByText("Plex", { exact: true })).toBeVisible();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/insights-history-both.png`, fullPage: true });

    await page.getByRole("button", { name: "Plex", exact: true }).click();
    await expect(page).toHaveURL(/server=plex/);
    await expect(page.getByRole("row", { name: /Northern Lights/ })).toHaveCount(0);
    expect(seen.some((p) => p.startsWith("/api/v1/insights/history?") && p.includes("server=plex"))).toBeTruthy();
    expect(await page.getByRole("link", { name: "Export CSV" }).getAttribute("href")).toContain("server=plex");
    // A reload keeps the filter (it lives in the address).
    await page.reload();
    await expect(page.getByRole("button", { name: "Plex", exact: true })).toHaveAttribute("aria-pressed", "true");
  });

  test("People has one row per server identity, and a Jellyfin person's page says it has no stall data", async ({ page }) => {
    await serveBoth(page);
    await page.goto("/insights?tab=people");
    const robin = page.getByRole("row", { name: /Robin/ });
    await expect(robin.getByText("Jellyfin", { exact: true })).toBeVisible();
    await expect(page.getByRole("row", { name: /Jesse/ }).getByText("Plex", { exact: true })).toBeVisible();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/insights-people-both.png`, fullPage: true });
    await robin.getByRole("link", { name: "Robin" }).click();
    await expect(page).toHaveURL(new RegExp(`/insights/people/${ins.JF_USER}$`));
    await expect(page.getByText("Jellyfin doesn't report buffering")).toBeVisible();
  });

  test("Reliability leaves Jellyfin out and says so", async ({ page }) => {
    await serveBoth(page);
    await page.goto("/insights?tab=reliability");
    await expect(page.getByRole("note")).toContainText("Jellyfin doesn't report buffering, so its 1 play in this window isn't counted here.");
    await expect(page.getByText("Stall rate")).toBeVisible();
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/insights-reliability-both.png`, fullPage: true });
    await page.getByRole("button", { name: "Jellyfin", exact: true }).click();
    await expect(page.getByText("Stall rate")).toHaveCount(0);
    await expect(page.getByRole("note")).toContainText("Jellyfin doesn't report buffering");
  });

  test("Jellyfin monitoring off: the banner names it and Turn on saves Jellyfin's switch", async ({ page }) => {
    const { puts } = await serveBoth(page, "off");
    await page.goto("/insights?tab=history");
    const banner = page.getByRole("status").filter({ hasText: "Monitoring is off on Jellyfin — nothing new is recorded." });
    await expect(banner).toBeVisible();
    await banner.getByRole("button", { name: "Turn on" }).click();
    await expect.poll(() => puts.length).toBe(1);
    expect(puts[0]).toEqual({ monitoring_enabled: true });
  });

  test("on a phone the chips and badges fit", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await serveBoth(page);
    await page.goto("/insights?tab=history");
    await expect(page.getByRole("button", { name: "Jellyfin", exact: true })).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    if (SHOTS) await page.screenshot({ path: `${SHOTS}/insights-history-phone.png`, fullPage: true });
  });
});

test.describe("Plex only", () => {
  test.use({ persona: "admin" });

  test("no server chips and no badges", async ({ page }) => {
    const table = ins.routes(() => ins.config("recording"));
    await page.route((u) => u.pathname.startsWith("/api/v1/insights/"), async (route: Route) => {
      const req = route.request();
      const url = new URL(req.url());
      const r = table.find((t) => t.method === req.method() && t.path === url.pathname);
      if (!r) return route.fallback();
      const out = r.respond ? r.respond({ url, params: [], body: undefined }) : r.body;
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out) });
    });
    await page.goto("/insights?tab=history");
    await expect(page.getByRole("cell", { name: /The Cartographer/ }).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Jellyfin", exact: true })).toHaveCount(0);
    await expect(page.getByText("Plex", { exact: true })).toHaveCount(0);
  });
});
