import type { Page, Route } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { dashboard } from "./fixtures/media";
import { status } from "./fixtures/system";
import { personas } from "./fixtures/users";

// MUS-24: Music on the Dashboard and the Calendar. With Music on, staff see an Albums tile
// and a Music card (counts, new releases, recent imports) on the Dashboard, and album
// releases on the Calendar that open the album page; requesters never see albums; with
// Music off the Dashboard has no music. (Runs in the phone project: the file name ends in
// calendar.spec.ts. Each block sets its own width.)

async function open(page: Page, path: string, api: MockedApi) {
  await page.goto(path);
  await expect(page.getByRole("main")).toBeVisible();
  await api.quiet();
}

async function noSidewaysScroll(page: Page) {
  const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
  expect(w.doc).toBeLessThanOrEqual(w.viewport);
}

const json = (route: Route, body: unknown) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });

for (const [width, height] of [[1280, 900], [375, 812]] as const) {
  test.describe(`staff at ${width}px`, () => {
    test.use({ persona: "admin", viewport: { width, height } });

    test("the Dashboard shows the Albums tile and the Music card", async ({ page, api }) => {
      await open(page, "/", api);
      const tile = page.getByRole("link").filter({ hasText: "2 missing · 4 artists" });
      await expect(tile).toHaveAttribute("href", "/music");
      await expect(tile).toContainText("Albums");

      await expect(page.getByText("Tracks", { exact: true })).toBeVisible();
      await expect(page.getByText("112", { exact: true })).toBeVisible();
      await expect(page.getByText("3.2 GB", { exact: true })).toBeVisible();
      // Missing and Upcoming each open the Wanted tab that lists them.
      await expect(page.getByRole("link", { name: "2", exact: true })).toHaveAttribute("href", "/activity/wanted?show=searching");
      await expect(page.getByRole("link", { name: "1", exact: true })).toHaveAttribute("href", "/activity/wanted?show=upcoming");

      const releases = page.getByRole("region", { name: "New releases · 2 in 30 days" });
      const soon = releases.getByRole("link").filter({ hasText: "Low Tide Sessions" });
      await expect(soon).toHaveAttribute("href", "/music/album/51");
      await expect(soon).toContainText("The Quiet Harbours · EP · 2026-10-17");
      await expect(soon).toContainText("Wanted");
      await expect(releases.getByRole("link").filter({ hasText: "Harbour Demos" })).toContainText("Not wanted");

      const imports = page.getByRole("region", { name: "Recent imports" });
      await expect(imports.getByRole("link", { name: "The Quiet Harbours" })).toHaveAttribute("href", "/music/5");
      await noSidewaysScroll(page);
    });

    test("the Calendar shows album releases that open the album page", async ({ page, api }) => {
      const view = width >= 640 ? "month" : "agenda";
      await open(page, `/calendar?view=${view}`, api);
      if (view === "month") {
        await expect(page.getByRole("heading", { name: "October 2026" })).toBeVisible();
        await expect(page.getByText("Albums", { exact: true })).toBeVisible(); // the legend
        await expect(page.getByRole("link", { name: "Low Tide Sessions — The Quiet Harbours · EP" })).toHaveAttribute("href", "/music/album/51");
        await expect(page.getByRole("link", { name: "Breakwater Live — Ana Lys · Album" })).toHaveAttribute("href", "/music/album/52");
      } else {
        const soon = page.getByRole("link").filter({ hasText: "Low Tide Sessions" });
        await expect(soon).toHaveAttribute("href", "/music/album/51");
        await expect(soon).toContainText("The Quiet Harbours · EP");
        await expect(soon).toContainText("Upcoming");
        const out = page.getByRole("region", { name: "Yesterday" }).getByRole("link").filter({ hasText: "Breakwater Live" });
        await expect(out).toContainText("Downloaded");
      }
      await noSidewaysScroll(page);
    });
  });
}

test.describe("requester at 375px", () => {
  test.use({ persona: "requester", viewport: { width: 375, height: 812 } });

  test("never sees albums on the Calendar", async ({ page, api }) => {
    await open(page, "/calendar", api);
    await page.getByRole("group", { name: "Show" }).getByRole("button", { name: "Everything" }).click();
    await expect(page.getByRole("link").filter({ hasText: "Lanterns Over" })).toBeVisible();
    await expect(page.getByText("Low Tide Sessions")).toHaveCount(0);
    await expect(page.getByText("Breakwater Live")).toHaveCount(0);
    await page.getByRole("button", { name: "Month", exact: true }).click();
    await expect(page.getByText("Albums", { exact: true })).toHaveCount(0); // no legend entry
  });
});

test.describe("staff with Music off", () => {
  test.use({ persona: "admin", viewport: { width: 1280, height: 900 } });

  test("the Dashboard and the Calendar legend have no music", async ({ page, api }) => {
    // The server sends no music while the module is off.
    await page.route((u) => u.pathname === "/api/v1/status", (route) => json(route, { ...status(personas.admin), music_enabled: false }));
    await page.route((u) => u.pathname === "/api/v1/dashboard", (route) => json(route, { ...dashboard, music: undefined }));
    await open(page, "/", api);
    await expect(page.getByRole("link").filter({ hasText: "missing · 4 artists" })).toHaveCount(0);
    await expect(page.getByRole("region", { name: /New releases/ })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "Music →" })).toHaveCount(0);
    // The other tiles are all still there.
    await expect(page.getByRole("link").filter({ hasText: "3 missing" })).toHaveAttribute("href", "/movies");

    await open(page, "/calendar?view=month", api);
    await expect(page.getByText("Movies", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("Albums", { exact: true })).toHaveCount(0);
  });
});
