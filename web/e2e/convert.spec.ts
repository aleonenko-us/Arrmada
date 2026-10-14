import { test, expect } from "./mockApi";
import { convertSettings, convertStatus } from "./fixtures/modules";

// Phase 11 Convert: the time forecast on the Overview and in the hours setting (CONV-22).
test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("Up next shows each file's time, and the Overview the backlog and what a restart loses", async ({ page }) => {
    await page.route("**/api/v1/convert/status", (route) => route.fulfill({
      json: {
        ...convertStatus, state: "working", message: "Converting 1 file", auto: true, window: "01:00–07:00", remaining: 3,
        up_next: [{ key: "movie:1", title: "Dune (2021)", kind: "movie", video: true, saving: 40 * 1024 ** 3, size: 80 * 1024 ** 3,
          tracks: "", reason: "video", codec: "hevc", current: "H264", eta_sec: 24 * 3600, nights: 4 }],
        backlog_sec: 270 * 6 * 3600, backlog_nights: 270, in_flight_secs: 20 * 3600,
      },
    }));
    await page.goto("/convert");
    await expect(page.getByText("~24 h · ~4 nights")).toBeVisible();
    await expect(page.getByText("Backlog ≈ 9 months of nights at 01:00–07:00")).toBeVisible();
    await expect(page.getByText("A restart or update now loses ~20 h of encoding")).toBeVisible();
  });

  test("the hours setting shows the backlog at the hours being edited", async ({ page }) => {
    await page.route("**/api/v1/convert/status", (route) => route.fulfill({ json: { ...convertStatus, backlog_sec: 24 * 3600 } }));
    await page.route("**/api/v1/convert/settings", (route) => route.request().method() === "GET"
      ? route.fallback()
      : route.fulfill({ json: { ...convertSettings, ...(route.request().postDataJSON() as object) } }));
    await page.goto("/settings/convert");
    await expect(page.getByText("At these hours: Backlog ≈ 4 nights at 01:00–07:00")).toBeVisible();
    await page.getByLabel("End", { exact: true }).fill("09:00");
    await expect(page.getByText("At these hours: Backlog ≈ 3 nights at 01:00–09:00")).toBeVisible();
  });

  // CONV-25: the devices panel, and turning AV1 on when a device had AV1 transcoded.
  test("Your devices shows what each player did, and AV1 asks first when one transcoded it", async ({ page }) => {
    const use = (state: "yes" | "no" | "unknown") => ({ state, direct: state === "yes" ? 3 : 0, transcoded: state === "no" ? 2 : 0 });
    await page.route("**/api/v1/convert/devices", (route) => route.fulfill({
      json: {
        monitoring: true, since: 0, devices: [
          { product: "Plex for Roku", platform: "Roku", player: "Bedroom Roku", last_seen: Math.floor(Date.now() / 1000) - 3600, plays: 5,
            hevc: use("yes"), hevc10: use("no"), av1: use("no") },
          { product: "Plex for Android (TV)", platform: "Android", player: "Shield", last_seen: Math.floor(Date.now() / 1000) - 7200, plays: 9,
            hevc: use("yes"), hevc10: use("yes"), av1: use("unknown") },
        ],
      },
    }));
    await page.route("**/api/v1/convert/settings", (route) => route.request().method() === "GET"
      ? route.fallback()
      : route.fulfill({ json: { ...convertSettings, ...(route.request().postDataJSON() as object) } }));
    await page.goto("/settings/convert");
    const panel = page.getByRole("region", { name: "Your devices" });
    await expect(panel.getByText("Bedroom Roku (Plex for Roku)")).toBeVisible();
    await expect(panel.getByLabel("Bedroom Roku (Plex for Roku): HEVC 10-bit was transcoded (2×)")).toHaveText("✗");
    await expect(panel.getByLabel("Shield (Plex for Android (TV)): played HEVC 10-bit as it was (3×)")).toHaveText("✓");
    await expect(panel.getByLabel("Shield (Plex for Android (TV)): no AV1 played yet")).toHaveText("?");
    // No claim that HEVC plays everywhere.
    await expect(page.getByText(/plays everywhere/)).toHaveCount(0);

    // Turning AV1 on names the device that transcoded it; Cancel saves nothing.
    await page.getByRole("checkbox", { name: /My devices can play AV1/ }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(/Plex transcoded AV1 video for Bedroom Roku \(Plex for Roku\)/)).toBeVisible();
    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByRole("checkbox", { name: /My devices can play AV1/ })).not.toBeChecked();
    // Allow anyway saves it.
    await page.getByRole("checkbox", { name: /My devices can play AV1/ }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Allow AV1 anyway" }).click();
    await expect(page.getByRole("checkbox", { name: /My devices can play AV1/ })).toBeChecked();
  });

  // CONV-26: 10-bit output for 8-bit sources is a stated choice, with the devices' evidence.
  test("the 8-bit sources choice says which applies, names the devices, and saves", async ({ page }) => {
    const use = (state: "yes" | "no" | "unknown") => ({ state, direct: state === "yes" ? 1 : 0, transcoded: state === "no" ? 1 : 0 });
    await page.route("**/api/v1/convert/devices", (route) => route.fulfill({
      json: { monitoring: true, since: 0, devices: [{ product: "Plex Web", platform: "Chrome", player: "Chrome", last_seen: Math.floor(Date.now() / 1000), plays: 2,
        hevc: use("yes"), hevc10: use("no"), av1: use("unknown") }] },
    }));
    const puts: unknown[] = [];
    await page.route("**/api/v1/convert/settings", (route) => {
      if (route.request().method() === "GET") return route.fallback();
      const body = route.request().postDataJSON() as object;
      puts.push(body);
      return route.fulfill({ json: { ...convertSettings, ...body } });
    });
    await page.goto("/settings/convert");
    const group = page.getByRole("radiogroup", { name: "8-bit sources" });
    await expect(group.getByText("Now: 8-bit sources stay 8-bit; 10-bit and HDR files are always 10-bit.")).toBeVisible();
    await expect(group.getByText(/Plex transcoded HEVC 10-bit video for Chrome \(Plex Web\)/)).toBeVisible();
    await group.getByRole("radio", { name: /Always 10-bit/ }).check();
    await expect(group.getByText("Now: every re-encode is 10-bit (Main10).")).toBeVisible();
    await expect.poll(() => puts).toEqual([{ ten_bit: "always" }]);
  });

  // CONV-27: resumable pieced encoding is a switch under Advanced, off until it's proven.
  test("encoding in pieces is off by default, says what it does, and saves", async ({ page }) => {
    const puts: unknown[] = [];
    await page.route("**/api/v1/convert/settings", (route) => {
      if (route.request().method() === "GET") return route.fallback();
      const body = route.request().postDataJSON() as object;
      puts.push(body);
      return route.fulfill({ json: { ...convertSettings, ...body } });
    });
    await page.goto("/settings/convert");
    await page.getByText("Advanced", { exact: true }).click();
    const pieces = page.getByRole("checkbox", { name: /Encode long films in pieces/ });
    await expect(pieces).not.toBeChecked();
    await expect(page.getByText(/a restart or update during the encode loses at most one piece/)).toBeVisible();
    await expect(page.getByText(/Off by default while it's being proven/)).toBeVisible();
    await pieces.click();
    await expect(pieces).toBeChecked();
    await expect.poll(() => puts).toEqual([{ resumable: true }]);
  });

  // CONV-23: "done" with files in Problems says so and links there; a group's "Try these
  // again" is one request however many files it holds.
  test("done with problems links to them, and a group retries in one call", async ({ page }) => {
    await page.route("**/api/v1/convert/status", (route) => route.fulfill({
      json: { ...convertStatus, state: "done", auto: true, remaining: 0, problems: 3,
        message: "Everything convertible is done — 3 files couldn't be converted (see Problems)" },
    }));
    const seeding = (id: number) => ({ key: `movie:${id}`, kind: "hardlinked", reason: "still seeding", permanent: false, updated_at: "",
      retry_after: 0, attempts: 1, media_kind: "movie", movie_id: id, season: 0, episode: 0, title: `Film ${id}` });
    await page.route("**/api/v1/convert/skips", (route) => route.fulfill({ json: { items: [seeding(1), seeding(2), seeding(3)] } }));
    await page.route("**/api/v1/convert/blocklist", (route) => route.fulfill({ json: { items: [] } }));
    const clears: string[] = [];
    await page.route("**/api/v1/convert/skips/clear**", (route) => {
      clears.push(new URL(route.request().url()).search);
      return route.fulfill({ json: { cleared: true, count: 3 } });
    });
    await page.goto("/convert");
    await expect(page.getByText("Everything convertible is done — 3 files couldn't be converted (see Problems)")).toBeVisible();
    await page.getByRole("button", { name: "Open Problems" }).click();
    await page.getByRole("button", { name: "Try these again" }).click();
    await expect.poll(() => clears).toEqual(["?kind=hardlinked"]);
  });
});
