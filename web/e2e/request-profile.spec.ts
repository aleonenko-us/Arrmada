import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { newShow, requestableCard } from "./fixtures/discover";

// Issue #50: staff pick the quality profile a Discover request goes with, from a split
// button on the title ("＋ Request · HD 1080p" and a chevron listing the profiles). The
// default is picked to start and badged; the pick is checked and is what's sent. A show's
// pick lives on the season picker's request button. Requesters see the plain Request, and
// their Discover never asks for the profiles.

const movieSheet = (page: Page) => page.getByRole("dialog", { name: requestableCard.title });
const showSheet = (page: Page) => page.getByRole("dialog", { name: newShow.title });

async function fitsScreen(page: Page) {
  const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
  expect(w.doc, "document width with the sheet open").toBeLessThanOrEqual(w.viewport);
}

test.describe("staff on a desktop", () => {
  test.skip(({ isMobile }) => isMobile, "desktop project only");
  test.use({ persona: "admin" });

  test("pick HD-720p from the split button and the request carries it", async ({ page, api }) => {
    await page.goto(`/discover/movie/${requestableCard.tmdb_id}`);
    await api.quiet();
    const sheet = movieSheet(page);
    const main = sheet.getByRole("button", { name: "＋ Request with HD 1080p" });
    await expect(main).toBeVisible();
    await expect(main).toHaveAttribute("title", "＋ Request · HD 1080p");
    await expect(main.getByText("HD 1080p", { exact: true })).toBeVisible();

    const chevron = sheet.getByRole("button", { name: "Quality profile: HD 1080p" });
    await expect(chevron).toHaveAttribute("aria-haspopup", "menu");
    await chevron.click();
    const menu = sheet.getByRole("menu");
    await expect(menu).toBeVisible();
    await expect(menu.getByText("Quality profile", { exact: true })).toBeVisible();
    // Every movie profile, the default badged and (as the pick) checked.
    await expect(menu.getByRole("menuitem")).toHaveText([/HD 1080p/, /Ultra HD/, /HD-720p/]);
    const hd = menu.getByRole("menuitem", { name: /HD 1080p/ });
    await expect(hd).toContainText("Default");
    await expect(hd).toContainText("✓");
    await expect(hd).toHaveAccessibleName(/selected/);
    await expect(menu.getByRole("menuitem", { name: /HD-720p/ })).not.toContainText("✓");
    // The keyboard lands in the list.
    await expect(hd).toBeFocused();

    await page.keyboard.press("ArrowDown");
    await expect(menu.getByRole("menuitem", { name: /Ultra HD/ })).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(menu.getByRole("menuitem", { name: /HD-720p/ })).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(menu).toBeHidden();
    await expect(sheet.getByRole("button", { name: "＋ Request with HD-720p" })).toBeVisible();

    // Reopened, the pick is checked; Escape closes just the list, not the title.
    await sheet.getByRole("button", { name: "Quality profile: HD-720p" }).click();
    await expect(sheet.getByRole("menuitem", { name: /HD-720p/ })).toHaveAccessibleName(/selected/);
    await expect(sheet.getByRole("menuitem", { name: /HD-720p/ })).toContainText("✓");
    await page.keyboard.press("Escape");
    await expect(sheet.getByRole("menu")).toBeHidden();
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole("button", { name: "Quality profile: HD-720p" })).toBeFocused();

    expect(api.callsTo("POST", "/api/v1/requests"), "picking files nothing").toEqual([]);
    await sheet.getByRole("button", { name: "＋ Request with HD-720p" }).click();
    await expect(page.getByText(`Requested “${requestableCard.title}” — searching now`)).toBeVisible();
    const posted = api.callsTo("POST", "/api/v1/requests");
    expect(posted).toHaveLength(1);
    expect(posted[0].body).toMatchObject({ media_type: "movie", tmdb_id: requestableCard.tmdb_id, quality_profile: "hd-720p" });
    expect(api.callsTo("GET", "/api/v1/quality/profiles?media=movie").length).toBeGreaterThan(0);
  });

  test("a show's pick sits on the season picker's request button", async ({ page, api }) => {
    await page.goto(`/discover/series/${newShow.tmdb_id}`);
    await api.quiet();
    const sheet = showSheet(page);
    // The show's Request only opens the picker: it files nothing, so it has no profile.
    await sheet.getByRole("button", { name: "＋ Request", exact: true }).click();
    const picker = sheet.getByRole("region", { name: "Choose seasons" });
    await expect(picker).toBeVisible();
    await expect(picker.getByRole("button", { name: "Request all seasons with HD 1080p" })).toBeVisible();
    await picker.getByRole("button", { name: "Quality profile: HD 1080p" }).click();
    await expect(picker.getByRole("menuitem")).toHaveText([/HD 1080p/, /Ultra HD/, /HD-720p/]);
    await picker.getByRole("menuitem", { name: /HD-720p/ }).click();
    await picker.getByRole("button", { name: "Request all seasons with HD-720p" }).click();

    await api.quiet();
    const posted = api.callsTo("POST", "/api/v1/requests");
    expect(posted).toHaveLength(1);
    expect(posted[0].body).toMatchObject({ media_type: "series", tmdb_id: newShow.tmdb_id, quality_profile: "tv-720p" });
    expect(api.callsTo("GET", "/api/v1/quality/profiles?media=series").length).toBeGreaterThan(0);
  });
});

test.describe("a manager who leaves the pick alone", () => {
  test.skip(({ isMobile }) => isMobile, "desktop project only");
  test.use({ persona: "manager" });

  // Untouched, nothing is sent: a new request gets the server's default (the one the button
  // names), and following one already waiting doesn't overwrite another staff member's pick.
  test("sends no profile: the server's default, the one the button names", async ({ page, api }) => {
    await page.goto(`/discover/movie/${requestableCard.tmdb_id}`);
    await api.quiet();
    await movieSheet(page).getByRole("button", { name: "＋ Request with HD 1080p" }).click();
    await api.quiet();
    const posted = api.callsTo("POST", "/api/v1/requests");
    expect(posted).toHaveLength(1);
    expect(posted[0].body).not.toHaveProperty("quality_profile");
  });

  test("choosing the default again sends it", async ({ page, api }) => {
    await page.goto(`/discover/movie/${requestableCard.tmdb_id}`);
    await api.quiet();
    const sheet = movieSheet(page);
    await sheet.getByRole("button", { name: "Quality profile: HD 1080p" }).click();
    await sheet.getByRole("menuitem", { name: /HD 1080p/ }).click();
    await sheet.getByRole("button", { name: "＋ Request with HD 1080p" }).click();
    await api.quiet();
    expect(api.callsTo("POST", "/api/v1/requests")[0].body).toMatchObject({ quality_profile: "hd-1080p" });
  });
});

test.describe("a requester", () => {
  test.use({ persona: "requester" });

  test("sees the plain Request, with no profile picker and no profiles call", async ({ page, api }) => {
    await page.goto(`/discover/movie/${requestableCard.tmdb_id}`);
    await api.quiet();
    const sheet = movieSheet(page);
    const plain = sheet.getByRole("button", { name: "＋ Request", exact: true });
    await expect(plain).toBeVisible();
    await expect(sheet.locator('[aria-haspopup="menu"]')).toHaveCount(0);
    await expect(sheet.getByText("Quality profile")).toHaveCount(0);
    await fitsScreen(page);

    await plain.click();
    await expect(page.getByText(`Requested “${requestableCard.title}” — waiting for approval`)).toBeVisible();
    const posted = api.callsTo("POST", "/api/v1/requests");
    expect(posted).toHaveLength(1);
    expect(posted[0].body).not.toHaveProperty("quality_profile");
    expect(api.callsTo("GET", "/api/v1/quality/profiles"), "a requester never asks for the profiles").toEqual([]);
  });

  test("a show's season picker keeps its plain request button", async ({ page, api }) => {
    await page.goto(`/discover/series/${newShow.tmdb_id}`);
    await api.quiet();
    const sheet = showSheet(page);
    await sheet.getByRole("button", { name: "＋ Request", exact: true }).click();
    const picker = sheet.getByRole("region", { name: "Choose seasons" });
    await expect(picker.getByRole("button", { name: "Request all seasons", exact: true })).toBeVisible();
    await expect(sheet.locator('[aria-haspopup="menu"]')).toHaveCount(0);
    expect(api.callsTo("GET", "/api/v1/quality/profiles")).toEqual([]);
  });
});

test.describe("staff on a phone", () => {
  test.skip(({ isMobile }) => !isMobile, "phone project only");
  test.use({ persona: "admin" });

  test("the split button and its list fit a 375px screen", async ({ page, api }) => {
    await page.goto(`/discover/movie/${requestableCard.tmdb_id}`);
    await api.quiet();
    const sheet = movieSheet(page);
    const group = sheet.getByRole("group", { name: "Request and quality profile" });
    await expect(group).toBeVisible();
    await fitsScreen(page);
    const g = await group.boundingBox();
    expect(g && g.x >= 0 && g.x + g.width <= 375, `split button inside the screen: ${JSON.stringify(g)}`).toBeTruthy();

    // The profile is a whole second line under the label, not cut short.
    const name = sheet.getByRole("button", { name: "＋ Request with HD 1080p" }).getByText("HD 1080p", { exact: true });
    await expect(name).toBeVisible();
    expect(await name.evaluate((el) => el.scrollWidth <= el.clientWidth), "the profile name isn't truncated").toBe(true);

    await sheet.getByRole("button", { name: "Quality profile: HD 1080p" }).tap();
    const menu = sheet.getByRole("menu");
    await expect(menu).toBeVisible();
    const m = await menu.boundingBox();
    expect(m && m.x >= 0 && m.x + m.width <= 375, `list inside the screen: ${JSON.stringify(m)}`).toBeTruthy();
    await fitsScreen(page);
    await menu.getByRole("menuitem", { name: /HD-720p/ }).tap();
    await sheet.getByRole("button", { name: "＋ Request with HD-720p" }).tap();

    await api.quiet();
    const posted = api.callsTo("POST", "/api/v1/requests");
    expect(posted).toHaveLength(1);
    expect(posted[0].body).toMatchObject({ media_type: "movie", quality_profile: "hd-720p" });
  });
});
