import type { Page, WebSocketRoute } from "@playwright/test";
import { test, expect } from "./mockApi";
import { activity, activityTwoClients, STALLED_QUEUE_HASH } from "./fixtures/media";
import { NOW } from "./fixtures/clock";

// Activity → Queue: a Stalled pill beside Downloading, Finishing and Seeding, counting the
// downloads the server calls stalled (no progress for half an hour, by the stall
// fail-over's own clock) — a moment with no peer sending isn't one. With more than one
// download client, each row names its client and the queue can be narrowed to one.

async function liveSocket(page: Page): Promise<(topic: string, data?: unknown) => void> {
  const sockets: WebSocketRoute[] = [];
  await page.routeWebSocket("**/api/v1/ws", (ws) => { sockets.push(ws); });
  return (topic, data = {}) => { for (const ws of sockets) ws.send(JSON.stringify({ topic, data })); };
}

const card = (page: Page, text: string) => page.getByTestId("transfer-card").filter({ hasText: text });

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("the Stalled pill counts stalled downloads and narrows the list to them", async ({ page, api }) => {
    await page.goto("/activity/queue");
    const views = page.getByRole("group", { name: "Queue views" });
    await expect(views.getByRole("button")).toHaveText([/^Downloading\s*3$/, /^Stalled\s*1$/, /^Finishing\s*2$/, /^Seeding\s*2$/]);

    // Marked on the row in the Downloading list: how long, and its seeds.
    await expect(card(page, "Old.Film.2019").getByTestId("stalled-chip")).toHaveText("Stalled 2h · 0 seeds");
    // qBittorrent's stalledDL for a few minutes is only waiting for peers.
    const quiet = card(page, "Quiet.Bay");
    await expect(quiet.getByTestId("stalled-chip")).toHaveCount(0);
    await expect(quiet.getByText("Waiting for peers · 2 seeds · no data for 4m")).toBeVisible();

    await views.getByRole("button", { name: /^Stalled/ }).click();
    await expect(page).toHaveURL(/\/activity\/queue\?show=stalled$/);
    await expect(views.getByRole("button", { name: /^Stalled/ })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByTestId("transfer-card")).toHaveCount(1);
    const stalled = card(page, "Old.Film.2019");
    await expect(stalled.getByRole("link", { name: "Old Film" })).toHaveAttribute("href", "/movies/5");
    await expect(stalled.getByText("No progress for 2h · trying another release in 4h")).toBeVisible();
    // The type pills count what this view shows.
    await expect(page.getByRole("button", { name: /^All\s*1$/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Series\s*0$/ })).toBeVisible();

    // The row's own actions apply: another release now (kept until one is grabbed), Block, Delete.
    await expect(stalled.getByRole("button", { name: "Block" })).toBeVisible();
    await expect(stalled.getByRole("button", { name: "Delete" })).toBeVisible();
    await stalled.getByRole("button", { name: "Try another release" }).click();
    await expect(page.getByText(/Grabbed Old\.Film\.1080p\.WEB-DL-NEW instead/)).toBeVisible();
    expect(api.callsTo("POST", `/api/v1/queue/${STALLED_QUEUE_HASH}/failover`)).toHaveLength(1);

    // Linked straight to, the view opens on its own.
    await page.goto("/activity/queue?show=stalled");
    await expect(page.getByTestId("transfer-card")).toHaveCount(1);
  });

  test("a download that isn't moving yet, or has moved, isn't offered Try another release", async ({ page }) => {
    await page.goto("/activity/queue");
    await expect(card(page, "Quiet.Bay").getByRole("button", { name: "Block" })).toBeVisible();
    await expect(card(page, "Quiet.Bay").getByRole("button", { name: "Try another release" })).toHaveCount(0);
    await expect(card(page, "Driftwood").getByRole("button", { name: "Try another release" })).toHaveCount(0);
  });

  test("the Stalled count follows the queue live", async ({ page, api }) => {
    const send = await liveSocket(page);
    await page.goto("/activity/queue");
    const pill = page.getByRole("group", { name: "Queue views" }).getByRole("button", { name: /^Stalled/ });
    await expect(pill).toHaveText(/^Stalled\s*1$/);
    await api.quiet(1000);

    // Its bar moves: that's progress, so it isn't stalled any more — without a read.
    const reads = () => api.callsTo("GET", "/api/v1/downloads").length;
    const before = reads();
    send("queue.progress", [{ hash: STALLED_QUEUE_HASH, progress: 0.25, eta_seconds: 600, state: "downloading" }]);
    await expect(pill).toHaveText(/^Stalled\s*0$/);
    await expect(card(page, "Old.Film.2019").getByTestId("stalled-chip")).toHaveCount(0);
    expect(reads()).toBe(before);

    // The server now calls Quiet Bay stalled too: the next read after queue.changed says so.
    await page.route("**/api/v1/downloads", (route) => route.fulfill({
      json: {
        ...activity,
        downloads: activity.downloads.map((d) => (d.name.startsWith("Quiet.Bay") || d.hash === STALLED_QUEUE_HASH
          ? { ...d, stalled: true, stalled_since: NOW / 1000 - 3600, stage: "stalled" as const } : d)),
      },
    }));
    send("queue.changed", { added: [], removed: [], changed: [STALLED_QUEUE_HASH] });
    await expect(pill).toHaveText(/^Stalled\s*2$/);
  });

  test("with one download client, rows don't name it and there's no client filter", async ({ page }) => {
    await page.goto("/activity/queue");
    await expect(card(page, "Driftwood")).toBeVisible();
    await expect(page.getByTestId("client-chip")).toHaveCount(0);
    await expect(page.getByRole("combobox", { name: "Download client" })).toHaveCount(0);
  });

  test("with two download clients, each row names its client and the queue narrows to one", async ({ page }) => {
    await page.route("**/api/v1/downloads", (route) => route.fulfill({ json: activityTwoClients }));
    await page.goto("/activity/queue");
    await expect(card(page, "Driftwood").getByTestId("client-chip")).toHaveText("qBittorrent");
    await expect(card(page, "Old.Film.2019").getByTestId("client-chip")).toHaveText("qBittorrent 2");

    const views = page.getByRole("group", { name: "Queue views" });
    const filter = page.getByRole("combobox", { name: "Download client" });
    await expect(filter.locator("option")).toHaveText(["All clients", "qBittorrent (5)", "qBittorrent 2 (2)"]);
    await filter.selectOption({ label: "qBittorrent 2 (2)" });
    await expect(page).toHaveURL(/[?&]client=2(&|$)/);
    await expect(views.getByRole("button")).toHaveText([/^Downloading\s*1$/, /^Stalled\s*1$/, /^Finishing\s*0$/, /^Seeding\s*1$/]);
    await expect(page.getByTestId("transfer-card")).toHaveCount(1);
    await expect(card(page, "Old.Film.2019")).toBeVisible();

    // Finishing and Seeding cards name their client too.
    await filter.selectOption({ label: "All clients" });
    await views.getByRole("button", { name: /^Seeding/ }).click();
    await expect(card(page, "Harbour.Lights").getByTestId("client-chip")).toHaveText("qBittorrent 2");
    await views.getByRole("button", { name: /^Finishing/ }).click();
    await expect(card(page, "Heat.1995").getByTestId("client-chip")).toHaveText("qBittorrent");
  });
});
