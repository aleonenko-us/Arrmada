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
    await page.getByRole("switch", { name: "Only during set hours" }).click();
    await expect(page.getByText("At these hours: Backlog ≈ 4 nights at 01:00–07:00")).toBeVisible();
    await page.getByLabel("End", { exact: true }).fill("09:00");
    await expect(page.getByText("At these hours: Backlog ≈ 3 nights at 01:00–09:00")).toBeVisible();
  });
});
