import type { Page, WebSocketRoute } from "@playwright/test";
import { test, expect } from "./mockApi";

// FE-27: pages refresh when the server says something moved, over one websocket per tab,
// instead of polling blind — and a hidden tab asks for nothing.

// Takes over the mocked socket so the spec can push events down it. Registered after the
// fixture's silent one, so it wins.
async function liveSocket(page: Page): Promise<{ send: (topic: string, data?: unknown) => void; opened: () => number }> {
  const sockets: WebSocketRoute[] = [];
  await page.routeWebSocket("**/api/v1/ws", (ws) => { sockets.push(ws); });
  return {
    send: (topic, data = {}) => { for (const ws of sockets) ws.send(JSON.stringify({ topic, data })); },
    opened: () => sockets.length,
  };
}

async function setHidden(page: Page, hidden: boolean) {
  await page.evaluate((h) => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => (h ? "hidden" : "visible") });
    Object.defineProperty(document, "hidden", { configurable: true, get: () => h });
    document.dispatchEvent(new Event("visibilitychange"));
  }, hidden);
}

test.describe("live updates", () => {
  test.use({ persona: "admin" });

  test("the Movies grid refreshes on a websocket event, without polling", async ({ page, api }) => {
    test.setTimeout(40_000);
    const live = await liveSocket(page);
    await page.goto("/movies");
    await expect.poll(() => live.opened()).toBe(1);
    await api.quiet(1000); // settled: every first load is in
    const movies = () => api.callsTo("GET", "/api/v1/movies").filter((c) => c.path === "/api/v1/movies").length;
    const before = movies();
    expect(before).toBeGreaterThan(0);

    // Nothing happening: no request.
    await page.waitForTimeout(4000);
    expect(movies()).toBe(before);

    // The server says the library changed: one refetch, for a burst.
    live.send("library.scanned");
    live.send("library.scanned");
    await expect.poll(movies, { timeout: 4000 }).toBe(before + 1);
    await page.waitForTimeout(1500);
    expect(movies()).toBe(before + 1);

    // Hidden: the event waits for the tab to be shown.
    await setHidden(page, true);
    live.send("library.scanned");
    await page.waitForTimeout(2000);
    expect(movies()).toBe(before + 1);
    await setHidden(page, false);
    await expect.poll(movies, { timeout: 4000 }).toBe(before + 2);

    // Every listener on the page shares the one socket.
    expect(live.opened()).toBe(1);
  });

  // FE-28: the sidebar's Needs-you pills follow attention.changed on the same socket.
  test("the sidebar pills refresh on attention.changed", async ({ page, api }) => {
    const live = await liveSocket(page);
    await page.goto("/movies");
    await expect.poll(() => live.opened()).toBe(1);
    await api.quiet(1000); // settled: every first load is in
    const reads = () => api.callsTo("GET", "/api/v1/attention").length;
    expect(reads()).toBe(1);
    await page.route("**/api/v1/attention", (route) => route.fulfill({
      json: { at: null, stale: false, counts: { requests: 5, reviews: 0, downloads: 0, imports: 0, searches: 0, health: 0, health_errors: 0, total: 5 }, groups: [], items: [] },
    }));
    live.send("attention.changed", { total: 5 });
    await expect(page.getByRole("complementary").locator('a[href="/requests"]')).toContainText("5");
    await expect(page).toHaveTitle(/^\(5\) /);
  });

  test("a hidden Downloads tab doesn't poll", async ({ page, api }) => {
    test.setTimeout(40_000);
    await liveSocket(page);
    await page.goto("/downloads");
    await api.quiet(1000); // settled: every first load is in
    const reads = () => api.callsTo("GET", "/api/v1/downloads").length;
    await setHidden(page, true);
    const before = reads();
    // Longer than the queue's 10 s backstop poll.
    await page.waitForTimeout(12_000);
    expect(reads()).toBe(before);
    // Shown again: it catches up at once.
    await setHidden(page, false);
    await expect.poll(reads, { timeout: 3000 }).toBe(before + 1);
  });

  test("download bars move from queue.progress without asking the server", async ({ page, api }) => {
    const live = await liveSocket(page);
    await page.goto("/downloads");
    await expect.poll(() => live.opened()).toBe(1);
    await api.quiet(1000); // settled: every first load is in
    const reads = () => api.callsTo("GET", "/api/v1/downloads").length;
    const before = reads();
    await expect(page.getByText("42%")).toBeVisible(); // the fixture's Driftwood download
    live.send("queue.progress", [{ hash: "E2E0000000000000000000000000000000000001", progress: 0.77, eta_seconds: 60, state: "downloading" }]);
    await expect(page.getByText("77%")).toBeVisible();
    // The bar moved from the event alone.
    expect(reads()).toBe(before);
  });
});

// REQ-17: a requester's request views follow their own request events (the server sends
// a requester only user.<their id>.* topics) instead of polling.
test.describe("live request feed", () => {
  test.use({ persona: "requester" });

  test("the Discover strip refreshes on the requester's request event, without polling", async ({ page, api }) => {
    test.setTimeout(40_000);
    const live = await liveSocket(page);
    await page.goto("/discover");
    await expect.poll(() => live.opened()).toBe(1);
    await api.quiet(1000); // settled: every first load is in
    const lists = () => api.callsTo("GET", "/api/v1/requests").length;
    const before = lists();
    expect(before).toBeGreaterThan(0);
    await page.waitForTimeout(4000);
    expect(lists()).toBe(before);
    live.send("user.3.request.updated", { id: 1, status: "approved", media_type: "movie" });
    await expect.poll(lists, { timeout: 4000 }).toBeGreaterThan(before);
    // One socket for the bell and every strip.
    expect(live.opened()).toBe(1);
  });
});
