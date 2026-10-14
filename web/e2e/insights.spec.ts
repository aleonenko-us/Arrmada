import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import * as ins from "./fixtures/insights";
import { NOW } from "./fixtures/clock";
import type { PlexConfig } from "../src/lib/api";

// Insights (PLEX-08/09/18/19): the header badge says what monitoring is really doing, the
// recorded-plays tabs say when nothing new is being recorded, database tabs never hide
// behind Plex errors, and every tab fits a phone.

// serveInsights answers the Insights endpoints for a saved server. It is registered after
// the default table, so it wins for these paths. PUT /insights/plex records the body and
// flips the served config to what was saved.
async function serveInsights(page: Page, start: PlexConfig, opts: { plexDown?: boolean; empty?: boolean } = {}) {
  let cfg = start;
  const puts: unknown[] = [];
  const table = ins.routes(() => cfg, opts);
  await page.route((u) => u.pathname.startsWith("/api/v1/insights/"), async (route: Route) => {
    const req = route.request();
    const url = new URL(req.url());
    if (req.method() === "PUT" && url.pathname === "/api/v1/insights/plex") {
      const body = req.postDataJSON() as Partial<PlexConfig>;
      puts.push(body);
      cfg = { ...cfg, ...body, status: body.enabled ? "recording" : "off" };
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(cfg) });
    }
    const r = table.find((t) => t.method === req.method() && t.path === url.pathname);
    if (!r) return route.fallback();
    const out = r.respond ? r.respond({ url, params: [], body: undefined }) : r.body;
    return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out) });
  });
  return { puts, set: (c: PlexConfig) => { cfg = c; } };
}

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("the badge has four states, and the unreachable one explains itself", async ({ page }) => {
    const srv = await serveInsights(page, ins.config("unconfigured"));
    for (const [status, label] of [
      ["unconfigured", "Not connected"],
      ["off", "Connected · not recording"],
      ["recording", "Recording"],
      ["unreachable", "Plex unreachable"],
    ] as const) {
      srv.set(ins.config(status, status === "unreachable" ? { last_error: "connection refused" } : {}));
      await page.goto("/insights?tab=history");
      const badge = page.getByText(label, { exact: true });
      await expect(badge).toBeVisible();
      if (status === "unreachable") await expect(badge).toHaveAttribute("title", "connection refused");
    }
  });

  test("monitoring off: the recorded tabs carry a banner whose Turn on saves the switch", async ({ page }) => {
    const { puts } = await serveInsights(page, ins.config("off"));
    await page.goto("/insights?tab=history");
    const banner = page.getByRole("status").filter({ hasText: "Monitoring is off — nothing new is recorded." });
    await expect(banner).toBeVisible();
    // The data is still there under the banner.
    await expect(page.getByRole("cell", { name: /The Cartographer/ })).toBeVisible();
    await banner.getByRole("button", { name: "Turn on" }).click();
    await expect.poll(() => puts.length).toBe(1);
    // Only the switch is sent: the saved address stays as it is.
    expect(puts[0]).toEqual({ enabled: true });
    await expect(banner).toHaveCount(0);
    await expect(page.getByText("Recording", { exact: true })).toBeVisible();
  });

  test("Activity has no monitoring banner (it asks Plex directly)", async ({ page }) => {
    await serveInsights(page, ins.config("off"));
    await page.goto("/insights");
    await expect(page.getByText("2 streams active")).toBeVisible();
    await expect(page.getByText("Monitoring is off — nothing new is recorded.")).toHaveCount(0);
  });

  test("HW badges say what Plex really uses, and a CPU fallback is flagged", async ({ page }) => {
    await serveInsights(page, ins.config("recording", { hw_since: Math.floor(NOW / 1000) }));
    await page.goto("/insights");
    const gpu = page.getByRole("button", { name: /The Cartographer/ });
    await expect(gpu.getByText("HW dec+enc")).toBeVisible();
    const cpu = page.getByRole("button", { name: /Harbour Lights/ });
    await expect(cpu.getByText("CPU (fell back)")).toBeVisible();
    await cpu.click();
    // The stream's deep dive is the kit's dialog: named by the title, Escape closes it.
    const dive = page.getByRole("dialog", { name: /Harbour Lights/ });
    await expect(dive.getByText("Fell back to CPU", { exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dive).toHaveCount(0);

    // Recorded plays from before the change say their HW flag was only a request.
    await page.goto("/insights?tab=history");
    await page.getByRole("cell", { name: /The Cartographer/ }).click();
    const play = page.getByRole("dialog", { name: /The Cartographer/ });
    await expect(play.getByText("HW requested")).toHaveAttribute("title", /show HW as requested/);
    await play.getByRole("button", { name: "Close" }).click();
    await expect(play).toHaveCount(0);
  });

  test("without a server, imported plays still show on History, People and Graphs", async ({ page }) => {
    await serveInsights(page, ins.config("unconfigured"));
    await page.goto("/insights?tab=history");
    await expect(page.getByRole("cell", { name: /The Cartographer/ })).toBeVisible();
    await expect(page.getByRole("link", { name: "Connect Plex or Jellyfin →" })).toHaveAttribute("href", "/settings/connections#media-servers");
    await page.getByRole("tab", { name: "People" }).click();
    await expect(page.getByRole("cell", { name: "Jesse" })).toBeVisible();
    await page.getByRole("tab", { name: "Graphs" }).click();
    await expect(page.getByText("Daily plays by media type")).toBeVisible();
    await expect(page.getByText(/coming soon/i)).toHaveCount(0);
  });

  test("with nothing recorded and no server, a tab says what it will show and links to setup", async ({ page }) => {
    await serveInsights(page, ins.config("unconfigured"), { empty: true });
    await page.goto("/insights?tab=reliability");
    await expect(page.getByText("The buffering view", { exact: false })).toBeVisible();
    await expect(page.getByRole("link", { name: "Connect Plex or Jellyfin →" })).toBeVisible();
    await expect(page.getByText(/coming soon/i)).toHaveCount(0);
  });

  test("Plex down: Activity shows the error and keeps the watch statistics", async ({ page }) => {
    await serveInsights(page, ins.config("unreachable", { last_error: "connection refused" }), { plexDown: true });
    await page.goto("/insights");
    await expect(page.getByText(/Couldn’t reach Plex/)).toBeVisible();
    await expect(page.getByText("Most watched movies")).toBeVisible();
    await expect(page.getByText(/Plex unreachable — library counts and recently added/)).toBeVisible();
  });

  test("a name in History opens that person's page, whose All plays filters History to them", async ({ page }) => {
    await serveInsights(page, ins.config("recording"));
    await page.goto("/insights?tab=history");
    await page.getByRole("link", { name: "Jesse" }).first().click();
    await expect(page).toHaveURL(/\/insights\/people\/7$/);
    await expect(page.getByRole("heading", { name: "Jesse" })).toBeVisible();
    await expect(page.getByText("Where they watch from")).toBeVisible();
    await expect(page.getByRole("cell", { name: "192.168.1.20" })).toBeVisible();
    await page.getByRole("link", { name: "All plays →" }).click();
    await expect(page).toHaveURL(/tab=history/);
    await expect(page).toHaveURL(/user=7/);
    await expect(page.getByRole("combobox", { name: "Person" })).toHaveValue("7");
    await page.goBack();
    await expect(page.getByRole("heading", { name: "Jesse" })).toBeVisible();
  });

  test("History filters live in the address and the export link carries them", async ({ page }) => {
    await serveInsights(page, ins.config("recording"));
    await page.goto("/insights?tab=history");
    await page.getByRole("button", { name: "Transcode" }).click();
    await page.getByRole("button", { name: "7d" }).click();
    await page.getByRole("switch", { name: "Group resumed plays" }).click();
    await expect(page).toHaveURL(/decision=transcode/);
    await expect(page).toHaveURL(/range=7d/);
    await expect(page).toHaveURL(/group=1/);
    const href = await page.getByRole("link", { name: "Export CSV" }).getAttribute("href");
    expect(href).toMatch(/^\/api\/v1\/insights\/history\.csv\?/);
    expect(href).toContain("decision=transcode");
    expect(href).toContain("group=1");
    expect(href).toMatch(/from=\d+/);
    await page.reload();
    await expect(page.getByRole("switch", { name: "Group resumed plays" })).toHaveAttribute("aria-checked", "true");
    await expect(page.getByText("Watched", { exact: true }).first()).toBeVisible();
  });

  test("Library lists hot spots linked to the title and to Convert, and says when never watched can't be listed", async ({ page }) => {
    await serveInsights(page, ins.config("recording"));
    await page.goto("/insights?tab=library");
    await expect(page.getByText("HEVC 4K → H264 1080p")).toBeVisible();
    await expect(page.getByText(/Needs Plex's library list/)).toBeVisible();
    await expect(page.getByRole("link", { name: "Find in Convert" })).toHaveAttribute("href", /^\/convert\?tab=library&media=movies&q=/);
    await expect(page.getByRole("link", { name: "Open" })).toHaveAttribute("href", "/movies/41");
    // Nothing on the tab starts a conversion.
    await expect(page.getByRole("button", { name: /convert/i })).toHaveCount(0);
  });

  test("Convert says pausing needs monitoring when it is off", async ({ page }) => {
    await page.goto("/convert?tab=settings");
    await expect(page.getByText(/Needs watch monitoring turned on for your media server/)).toBeVisible();
    await expect(page.getByRole("link", { name: "Settings → Media servers" })).toHaveAttribute("href", "/settings/media-servers");
  });
});
