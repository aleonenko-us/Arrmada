import { test, expect } from "./mockApi";
import { seriesDetails } from "./fixtures/seriesDetail";
import * as media from "./fixtures/media";

// GH-52 / GH-53: a show whose searching is off (its monitored switch) used to read
// "Unmonitored" on every wanted episode while each episode's menu offered "Stop monitoring",
// and picking a monitoring preset changed nothing anyone could see. Now the page says
// "Searching off" and why, a preset turns searching on and the menu ticks it, and the
// Series list counts and filters the shows whose searching is off.

test.describe("a show whose searching is off", () => {
  test.skip(({ isMobile }) => isMobile, "desktop project only");

  test("says Searching off, not Unmonitored, and a preset turns searching on and is ticked", async ({ page }) => {
    let on = false;
    let preset = "";
    const puts: unknown[] = [];
    await page.route(/\/api\/v1\/series\/1$/, (route) =>
      route.fulfill({ json: { ...seriesDetails[1], monitored: on, monitor_preset: preset } }));
    await page.route(/\/api\/v1\/series\/1\/monitor$/, async (route) => {
      const body = route.request().postDataJSON() as { preset?: string; monitored?: boolean };
      puts.push(body);
      const was = on;
      if (body.preset) { preset = body.preset; on = true; }
      if (body.monitored !== undefined) on = body.monitored;
      await route.fulfill({ json: { monitored: on, monitor_new_seasons: true, monitor_preset: preset, resumed: on && !was } });
    });

    await page.goto("/series/1?season=2");
    const banner = page.getByTestId("searching-off-banner");
    await expect(banner).toContainText("Searching is off for this show");
    await expect(banner).toContainText("3 wanted episodes wait for it.");

    // S02E06 is monitored and wanted: the show's switch is what holds it back.
    const e6 = page.locator("[data-episode='S02E06']");
    const chip6 = e6.getByText("Searching off", { exact: true });
    await expect(chip6).toBeVisible();
    await expect(chip6).toHaveAttribute("title", /Searching is off for this show — nothing is searched/);
    await expect(e6.getByRole("button", { name: "S02E06 is monitored — stop monitoring" })).toHaveAttribute("title", /^Wanted — searched for once you turn the show's searching on/);
    // S02E08 isn't monitored: that's still Unmonitored, and its dot says so.
    const e8 = page.locator("[data-episode='S02E08']");
    await expect(e8.getByText("Unmonitored", { exact: true })).toBeVisible();
    await expect(e8.getByRole("button", { name: "Monitor S02E08" })).toHaveAttribute("title", /^Not wanted/);
    // The season stays Monitored, with Searching off beside it.
    const season = page.getByRole("region", { name: "Season 2" });
    await expect(season.getByRole("button", { name: "Monitored", exact: true })).toHaveAttribute("title", /searched for once you turn the show's searching on/);
    await expect(season.getByText("Searching off", { exact: true }).first()).toBeVisible();

    // Monitor: Missing episodes turns searching on in the same request.
    const menuButton = page.getByRole("button", { name: "Searching: Off", exact: true });
    await menuButton.click();
    const menu = page.getByRole("menu", { name: "Searching: Off" });
    await expect(menu).toContainText("Searching off — nothing is searched");
    await menu.getByRole("menuitem", { name: /^Monitor: Missing episodes/ }).click();
    await expect.poll(() => puts).toEqual([{ preset: "missing" }]);
    await expect(page.getByText("Monitoring missing episodes — searching turned on.")).toBeVisible();

    await expect(page.getByRole("button", { name: "Searching: On", exact: true })).toBeVisible();
    await expect(banner).toHaveCount(0);
    await expect(e6.getByText("Wanted", { exact: true })).toBeVisible();
    await expect(e8.getByText("Unmonitored", { exact: true })).toBeVisible();

    // The menu remembers the preset.
    await page.getByRole("button", { name: "Searching: On", exact: true }).click();
    const after = page.getByRole("menu", { name: "Searching: On" });
    await expect(after.getByRole("menuitem", { name: /^Monitor: Missing episodes.*\(on\)$/ })).toBeVisible();
    await expect(after.getByRole("menuitem", { name: /^Monitor: All episodes.*\(on\)$/ })).toHaveCount(0);
  });

  test("a show changed by hand since its preset shows Custom", async ({ page }) => {
    await page.route(/\/api\/v1\/series\/1$/, (route) => route.fulfill({ json: { ...seriesDetails[1], monitor_preset: "custom" } }));
    await page.goto("/series/1");
    await expect(page.getByTestId("searching-off-banner")).toHaveCount(0);
    await page.getByRole("button", { name: "Searching: On", exact: true }).click();
    await expect(page.getByRole("menuitem", { name: /^Monitor: Custom \(changed by hand\).*\(on\)$/ })).toBeVisible();
  });

  test("Turn searching on in the banner turns it on", async ({ page }) => {
    let on = false;
    const puts: unknown[] = [];
    await page.route(/\/api\/v1\/series\/1$/, (route) => route.fulfill({ json: { ...seriesDetails[1], monitored: on } }));
    await page.route(/\/api\/v1\/series\/1\/monitor$/, async (route) => {
      puts.push(route.request().postDataJSON());
      on = true;
      await route.fulfill({ json: { monitored: true, monitor_new_seasons: true, resumed: true } });
    });
    await page.goto("/series/1");
    await page.getByTestId("searching-off-banner").getByRole("button", { name: "Turn searching on" }).click();
    await expect.poll(() => puts).toEqual([{ monitored: true }]);
    await expect(page.getByTestId("searching-off-banner")).toHaveCount(0);
  });
});

test.describe("the Series list", () => {
  test.skip(({ isMobile }) => isMobile, "desktop project only");
  test.use({ persona: "manager" });

  test("counts and filters shows whose searching is off, and points at the bulk switch", async ({ page, api }) => {
    const off = media.series.series.map((s) => ({ ...s, monitored: false }));
    await page.route((u) => u.pathname === "/api/v1/series", (route) => route.fulfill({ json: { ...media.series, series: off } }));
    await page.goto("/series");
    await expect(page.getByRole("button", { name: /^Searching off 3$/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Searching on 0$/ })).toBeVisible();
    const hint = page.getByTestId("searching-off-hint");
    await expect(hint).toContainText("Every show here has searching off");

    await hint.getByRole("button", { name: "show them" }).click();
    await expect(page).toHaveURL(/filter=searching-off/);
    await expect(page.getByRole("button", { name: /^Searching off 3$/ })).toHaveAttribute("aria-pressed", "true");

    await page.getByRole("button", { name: "Select", exact: true }).click();
    await page.getByRole("button", { name: /^Select all/ }).click();
    await page.getByRole("button", { name: "Searching on", exact: true }).click();
    const gate = () => api.callsTo("PUT", "/api/v1/series/").filter((c) => c.path.endsWith("/monitor"));
    await expect.poll(() => gate().length).toBe(3);
    expect(gate().map((c) => c.body)).toEqual([{ monitored: true }, { monitored: true }, { monitored: true }]);
  });

  test("a library with a few shows off shows no hint, and the pills add up", async ({ page }) => {
    const some = media.series.series.map((s, i) => ({ ...s, monitored: i !== 0 }));
    await page.route((u) => u.pathname === "/api/v1/series", (route) => route.fulfill({ json: { ...media.series, series: some } }));
    await page.goto("/series");
    await expect(page.getByRole("button", { name: /^Searching off 1$/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Searching on 2$/ })).toBeVisible();
    await expect(page.getByTestId("searching-off-hint")).toHaveCount(0);
  });
});
