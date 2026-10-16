import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { systemStatus } from "./fixtures/system";
import type { SystemStatus, UpdateStatus } from "../src/lib/api";

// The version badge at the foot of the staff sidebar: the release tag, or main and the
// short commit; an "Update" pill when the update check found something newer for this install's
// channel; a link to System → Status's Updates card. Runs at desktop width (the sidebar is
// always open) and phone width (it's the drawer behind the menu button).

const v020 = { tag: "v0.2.0", name: "v0.2.0", url: "https://github.com/tristenlammi/Arrmada/releases/tag/v0.2.0", notes: "## What's Changed\n* Version badge", notes_truncated: false, published_at: "2026-10-14T10:00:00Z" };
const v021 = { ...v020, tag: "v0.2.1", name: "v0.2.1", url: "https://github.com/tristenlammi/Arrmada/releases/tag/v0.2.1" };

function releaseBuild(update: Partial<UpdateStatus>): SystemStatus {
  return {
    ...systemStatus, version: "v0.2.0", commit: "c9d20ec",
    update: { ...systemStatus.update!, current: "v0.2.0", channel: "release", latest: v020, available: false, behind: null, ...update },
  };
}

async function serveStatus(page: Page, body: SystemStatus) {
  await page.route((u) => u.pathname === "/api/v1/system/status", (r) => r.fulfill({ json: body }));
}

// openNav opens the sidebar when it's a drawer (phone width); on a desktop it's open.
const phone = (page: Page) => (page.viewportSize()?.width ?? 1440) < 1024;
async function openNav(page: Page) {
  if (!phone(page)) return;
  const menu = page.getByRole("button", { name: /^Open menu/ });
  await menu.click();
  await expect(menu).toHaveAttribute("aria-expanded", "true");
  // Let it finish sliding in.
  await expect.poll(async () => (await page.locator("aside").boundingBox())?.x).toBe(0);
}

const badge = (page: Page) => page.getByTestId("version-badge");

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("a release that's up to date shows its tag, no Update pill", async ({ page }) => {
    await serveStatus(page, releaseBuild({}));
    await page.goto("/movies");
    await openNav(page);
    const link = badge(page).getByRole("link", { name: "Arrmada v0.2.0 — up to date" });
    await expect(link).toBeVisible();
    await expect(link).toHaveText("v0.2.0");
    await expect(link).toHaveAttribute("href", "/settings/status#updates");
    await expect(badge(page).getByTestId("update-pill")).toHaveCount(0);
  });

  test("a release with a newer one out has the Update pill and names it", async ({ page }) => {
    await serveStatus(page, releaseBuild({ available: true, latest: v021 }));
    await page.goto("/movies");
    await openNav(page);
    const link = badge(page).getByRole("link", { name: "Arrmada v0.2.0 — update available (v0.2.1)" });
    await expect(link).toBeVisible();
    await expect(link).toHaveText("v0.2.0Update");
    await expect(link.getByTestId("update-pill")).toBeVisible();
  });

  test("a build of main shows the short commit, and clicking it opens the Updates card", async ({ page }) => {
    // The default fixture: 2026.10.08 at abc1234, three commits behind main.
    await page.goto("/movies");
    await openNav(page);
    const link = badge(page).getByRole("link", { name: "Arrmada main · abc1234 — update available (3 commits behind main)" });
    await expect(link).toHaveText("main · abc1234Update");
    await expect(link.getByTestId("update-pill")).toBeVisible();
    await link.click();
    await expect(page).toHaveURL(/\/settings\/status#updates$/);
    await expect(page.locator("#updates").getByText("3 commits behind main.")).toBeVisible();
    // On a phone the drawer closed behind the link.
    if (phone(page)) {
      await expect(page.getByRole("button", { name: /^Open menu/ })).toHaveAttribute("aria-expanded", "false");
    }
  });
});

// The foot is pinned: the account row and the badge are on screen without scrolling at
// laptop heights and in the phone drawer, and only the nav list scrolls (the sidebar
// itself and the window never do, and nothing scrolls sideways).
async function onScreen(page: Page, name: string, loc: ReturnType<Page["locator"]>) {
  const box = await loc.boundingBox();
  const vp = page.viewportSize()!;
  expect(box, `${name} is laid out`).not.toBeNull();
  expect(box!.y, `${name} top`).toBeGreaterThanOrEqual(0);
  expect(box!.y + box!.height, `${name} bottom`).toBeLessThanOrEqual(vp.height);
  expect(box!.x, `${name} left`).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width, `${name} right`).toBeLessThanOrEqual(vp.width);
}

async function pinnedFoot(page: Page, navScrolls: boolean) {
  const account = page.getByRole("link", { name: /— Me$/ });
  const link = badge(page).getByRole("link");
  await expect(link).toBeVisible();
  await onScreen(page, "account row", account);
  await onScreen(page, "version badge", link);
  const m = await page.evaluate(() => {
    const aside = document.querySelector("aside")!;
    const nav = aside.querySelector("nav")!;
    return {
      aside: aside.scrollHeight - aside.clientHeight,
      asideX: aside.scrollWidth - aside.clientWidth,
      nav: nav.scrollHeight - nav.clientHeight,
      navX: nav.scrollWidth - nav.clientWidth,
      window: document.documentElement.scrollHeight - window.innerHeight,
    };
  });
  expect(m.aside, "the sidebar itself doesn't scroll").toBeLessThanOrEqual(1);
  expect(m.window, "the window doesn't scroll").toBeLessThanOrEqual(1);
  expect(Math.max(m.asideX, m.navX), "nothing scrolls sideways").toBeLessThanOrEqual(0);
  if (navScrolls) {
    expect(m.nav, "the nav list is what scrolls").toBeGreaterThan(0);
    // Its last entry is still reachable, and scrolling it leaves the foot where it was.
    const settings = page.locator("aside nav").getByRole("link", { name: /^Settings/ });
    await settings.scrollIntoViewIfNeeded();
    await onScreen(page, "Settings link", settings);
    await onScreen(page, "version badge after scrolling the nav", link);
  }
}

test.describe("pinned foot", () => {
  test.use({ persona: "admin" });

  for (const size of [{ width: 1440, height: 900 }, { width: 1280, height: 720 }]) {
    test(`account row and badge are on screen at ${size.width}×${size.height}`, async ({ page }) => {
      test.skip(phone(page), "desktop sizes run in the desktop project");
      await page.setViewportSize(size);
      await page.goto("/movies");
      await pinnedFoot(page, size.height < 900);
    });
  }

  test("account row and badge are on screen in the phone drawer", async ({ page }) => {
    test.skip(!phone(page), "the drawer runs in the phone project");
    await page.goto("/movies");
    await openNav(page);
    await pinnedFoot(page, true);
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  // Managers can't open System → Status: the build from the public /status, as text.
  test("sees the build as plain text, without asking for the admin status", async ({ page, api }) => {
    await page.goto("/movies");
    await openNav(page);
    await expect(badge(page)).toHaveText("Arrmada main · e2e0000");
    await expect(badge(page).getByRole("link")).toHaveCount(0);
    expect(api.callsTo("GET", "/api/v1/system/status")).toHaveLength(0);
  });
});
