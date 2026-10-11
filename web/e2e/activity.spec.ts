import type { WebSocketRoute } from "@playwright/test";
import { test, expect } from "./mockApi";
import { historyEvents } from "./fixtures/media";

// ACQ-26: one Activity page — Queue, Needs you, Wanted, History and Blocklist tabs — in
// place of four pages. The old addresses open the matching tab, the tab is the address
// (so Back works), and only the tab on screen polls.
test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("the sidebar's one Activity entry opens the Queue, with all five tabs", async ({ page }) => {
    await page.goto("/");
    await page.getByRole("complementary").locator('a[href="/activity"]').click();
    await expect(page).toHaveURL(/\/activity\/queue$/);
    const tabs = page.getByRole("tablist", { name: "Activity" }).getByRole("tab");
    await expect(tabs).toHaveText([/^Queue/, /^Needs you/, /^Wanted/, /^History/, /^Blocklist/]);
    await expect(page.getByText("Activity / Queue")).toBeVisible();
  });

  for (const [from, to] of [
    ["/downloads", /\/activity\/queue$/],
    ["/downloads?tab=seeding", /\/activity\/queue\?show=seeding$/],
    ["/downloads?tab=searching", /\/activity\/wanted$/],
    ["/review", /\/activity\/needs-you$/],
    ["/history", /\/activity\/history$/],
    ["/blocklist", /\/activity\/blocklist$/],
    ["/activity", /\/activity\/queue$/],
    ["/activity/nonsense", /\/activity\/queue$/],
  ] as const) {
    test(`${from} opens the matching tab`, async ({ page }) => {
      await page.goto(from);
      await expect(page).toHaveURL(to);
      await expect(page.getByRole("heading", { level: 1, name: "Activity" })).toBeVisible();
    });
  }

  test("switching tabs keeps the address in step, and Back steps back through them", async ({ page }) => {
    await page.goto("/activity/queue");
    const bar = page.getByRole("tablist", { name: "Activity" });
    await bar.getByRole("tab", { name: /^Wanted/ }).click();
    await expect(page).toHaveURL(/\/activity\/wanted$/);
    await page.getByRole("button", { name: /^Upcoming/ }).click();
    await expect(page).toHaveURL(/\/activity\/wanted\?show=upcoming$/);
    await bar.getByRole("tab", { name: /^Blocklist/ }).click();
    await expect(page).toHaveURL(/\/activity\/blocklist$/);
    await page.goBack();
    await expect(page).toHaveURL(/\/activity\/wanted\?show=upcoming$/);
    await expect(bar.getByRole("tab", { name: /^Wanted/ })).toHaveAttribute("aria-selected", "true");
    await page.goBack();
    await page.goBack();
    await expect(page).toHaveURL(/\/activity\/queue$/);
  });

  // ACQ-28: a finished download that isn't in the library sits under Finishing with the
  // reason, never among the healthy seeds.
  test("Finishing lists held and retrying downloads with readable reasons", async ({ page }) => {
    await page.goto("/activity/queue");
    const views = page.getByRole("group", { name: "Queue views" });
    await expect(views.getByRole("button", { name: /^Finishing/ })).toContainText("2");
    await views.getByRole("button", { name: /^Finishing/ }).click();
    await expect(page).toHaveURL(/\/activity\/queue\?show=finishing$/);
    const why = page.getByTestId("stage-why");
    await expect(why).toHaveText([
      "Held for review: it doesn't look like Aliens",
      "Import failed 3× (permission denied) — next try in 8 min",
    ]);
    await expect(page.getByText("Held", { exact: true })).toBeVisible();
    await expect(page.getByText("Import failed", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "Decide →" })).toHaveAttribute("href", "/activity/needs-you");
    // Seeding has only the imported one.
    await views.getByRole("button", { name: /^Seeding/ }).click();
    await expect(page.getByText("Alien.1979.1080p.BluRay-FIXTURE")).toHaveCount(0);
    await expect(page.getByText("Harbour.Lights.S02E04.1080p.WEB.H264-FIXTURE")).toBeVisible();
  });

  test("only the visible tab reads: leaving the Queue stops its queue reads", async ({ page, api }) => {
    // Take over the shared socket so the spec can push queue events down it.
    const sockets: WebSocketRoute[] = [];
    await page.routeWebSocket("**/api/v1/ws", (ws) => { sockets.push(ws); });
    const send = (topic: string) => { for (const ws of sockets) ws.send(JSON.stringify({ topic, data: {} })); };
    await page.goto("/activity/queue");
    await expect.poll(() => api.callsTo("GET", "/api/v1/downloads").length).toBeGreaterThan(0);
    await expect.poll(() => sockets.length).toBe(1);
    await api.quiet(1000);
    // On the Queue, the server saying the queue moved is one read.
    const before = api.callsTo("GET", "/api/v1/downloads").length;
    send("queue.changed");
    await expect.poll(() => api.callsTo("GET", "/api/v1/downloads").length, { timeout: 4000 }).toBe(before + 1);

    await page.getByRole("tablist", { name: "Activity" }).getByRole("tab", { name: /^Blocklist/ }).click();
    await api.quiet();
    const after = api.callsTo("GET", "/api/v1/downloads").length;
    // Unmounted, the Queue neither hears the event nor runs its backstop.
    send("queue.changed");
    await page.waitForTimeout(2_000);
    expect(api.callsTo("GET", "/api/v1/downloads")).toHaveLength(after);
    expect(api.callsTo("GET", "/api/v1/wanted")).toHaveLength(0);
    // Still the one socket for the whole tab.
    expect(sockets).toHaveLength(1);
  });
});

// ACQ-27: History is a dated, filterable log across every kind of title, each row linking
// to its title, that doesn't poll.
test.describe("History", () => {
  test.use({ persona: "manager", timezoneId: "UTC" });

  test("groups events by day, links each title, and leaves bookkeeping out", async ({ page }) => {
    await page.goto("/activity/history");
    await expect(page.getByRole("region", { name: "Today" }).getByRole("listitem")).toHaveCount(3);
    await expect(page.getByRole("region", { name: "Yesterday" }).getByRole("listitem")).toHaveCount(1);
    await expect(page.getByRole("link", { name: "Low Tide" })).toHaveAttribute("href", "/music/2");
    await expect(page.getByRole("link", { name: "The Long Quiet" })).toHaveAttribute("href", "/books/7");
    // "Added to library" is bookkeeping: only under Other.
    await expect(page.getByText("Added to library")).toHaveCount(0);
    await page.getByRole("group", { name: "Event" }).getByRole("button", { name: "Other" }).click();
    await expect(page.getByText("Added to library")).toBeVisible();
  });

  test("filters live in the address: Series + Imported shows only series imports", async ({ page, api }) => {
    await page.goto("/activity/history");
    await page.getByRole("group", { name: "Kind" }).getByRole("button", { name: "Series" }).click();
    await page.getByRole("group", { name: "Event" }).getByRole("button", { name: "Imported" }).click();
    await expect(page).toHaveURL(/\/activity\/history\?kind=series&event=imported$/);
    const rows = page.locator("#activity-panel-history").getByRole("listitem");
    await expect(rows).toHaveCount(1);
    await expect(rows.first().getByRole("link", { name: "Harbour Lights" })).toHaveAttribute("href", "/series/1");
    expect(api.callsTo("GET", "/api/v1/history?kind=series&event=imported")).not.toHaveLength(0);
    // Back undoes the last filter.
    await page.goBack();
    await expect(page).toHaveURL(/\/activity\/history\?kind=series$/);
    await expect(rows).toHaveCount(2);
  });

  test("Load more pages back, and nothing polls while idle", async ({ page, api }) => {
    const shown = historyEvents.filter((e) => e.category !== "other");
    const asked: string[] = [];
    // Pages of three, so there is a second page to load.
    await page.route("**/api/v1/history?*", (route) => {
      const url = new URL(route.request().url());
      asked.push(url.search);
      const before = url.searchParams.get("before");
      const events = before ? shown.slice(3) : shown.slice(0, 3);
      return route.fulfill({ json: { events, next_before: before ? "" : "cursor-1" } });
    });
    await page.goto("/activity/history");
    const rows = page.locator("#activity-panel-history").getByRole("listitem");
    await expect(rows).toHaveCount(3);
    await page.getByRole("button", { name: "Load more" }).click();
    await expect(rows).toHaveCount(shown.length);
    await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0);
    expect(asked[asked.length - 1]).toContain("before=cursor-1");

    await api.quiet();
    const seen = asked.length;
    await page.waitForTimeout(3_500);
    expect(asked.slice(seen)).toEqual([]);
  });

  test("Files imported shows the old list with when each file landed", async ({ page }) => {
    await page.goto("/activity/history?view=imports");
    await expect(page.getByText("/media/movies/The Cartographer (2024)/The Cartographer (2024).mkv")).toBeVisible();
  });
});
