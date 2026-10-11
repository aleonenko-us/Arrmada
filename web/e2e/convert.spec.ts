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
    await page.goto("/settings/tools#convert");
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
    await page.goto("/settings/tools#convert");
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
});
