import type { WebSocketRoute } from "@playwright/test";
import { test, expect } from "./mockApi";

// ACQ-18: Activity → Wanted says why each wanted title isn't downloading — the last
// search, the run of empty ones and the next automatic try — and Search now reports what
// it found.
test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("Searching explains each wanted title, and Search now reports back", async ({ page, api }) => {
    await page.goto("/activity/wanted");
    const lines = page.getByTestId("wanted-line");
    await expect(lines.first()).toHaveText(
      "Last search 60 min ago — Found 12 releases — 9 for other titles, 3 over your bitrate ceiling · 3 empty searches in a row, mostly for other titles · next automatic search in 60 min",
    );
    await expect(lines.nth(1)).toHaveText("Waiting for the first search");
    await expect(page.getByText("Ada Fenwick")).toBeVisible();

    await page.getByRole("button", { name: "Search now" }).first().click();
    await expect(lines.first()).toHaveText("Found 12 releases — 12 for other titles");
    expect(api.callsTo("POST", "/api/v1/wanted/movie/2/search")).toHaveLength(1);
  });

  // ACQ-32: Wanted isn't polled — it reads again when a search finishes (one read for a
  // burst), with only a two-minute backstop.
  test("Wanted reads on events, not on a timer", async ({ page, api }) => {
    const sockets: WebSocketRoute[] = [];
    await page.routeWebSocket("**/api/v1/ws", (ws) => { sockets.push(ws); });
    await page.goto("/activity/wanted");
    await expect(page.getByTestId("wanted-line").first()).toBeVisible();
    await expect.poll(() => sockets.length).toBe(1);
    await api.quiet(1000);
    const reads = () => api.callsTo("GET", "/api/v1/wanted").length;
    const before = reads();
    await page.waitForTimeout(2_500);
    expect(reads()).toBe(before);
    // A sweep finishing three searches in a row: one read, a moment later.
    for (let i = 0; i < 3; i++) sockets[0].send(JSON.stringify({ topic: "search.finished", data: { media_type: "movie", media_id: i } }));
    await expect.poll(reads, { timeout: 8000 }).toBe(before + 1);
    await page.waitForTimeout(2_000);
    expect(reads()).toBe(before + 1);
  });
});
