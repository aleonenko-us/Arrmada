import { test, expect } from "./mockApi";

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

  test("only the visible tab polls: leaving the Queue stops its queue reads", async ({ page, api }) => {
    await page.goto("/activity/queue");
    await expect.poll(() => api.callsTo("GET", "/api/v1/downloads").length).toBeGreaterThan(0);
    await page.getByRole("tablist", { name: "Activity" }).getByRole("tab", { name: /^Blocklist/ }).click();
    await api.quiet();
    const after = api.callsTo("GET", "/api/v1/downloads").length;
    // The queue polls every 3 s; a hidden Queue would have asked again by now.
    await page.waitForTimeout(3_500);
    expect(api.callsTo("GET", "/api/v1/downloads")).toHaveLength(after);
    expect(api.callsTo("GET", "/api/v1/wanted")).toHaveLength(0);
  });
});
