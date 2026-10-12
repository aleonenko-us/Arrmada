import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { qualityProfiles } from "./fixtures/media";

// QUAL-25: the custom-format editor in the profile builder's Advanced area, at phone
// width — several conditions, NOT, match any, a pattern checked inline, and nothing
// pushing the page sideways.

const vocabulary = {
  source: [{ value: "Remux", label: "Remux" }, { value: "BluRay", label: "BluRay (encode)" }, { value: "WEB-DL", label: "WEB-DL" }],
  codec: [{ value: "x265", label: "HEVC (x265, H.265)" }, { value: "x264", label: "H.264 (x264, AVC)" }],
};

type Format = { name: string; conditions: { type: string; value: string }[] };

// The builder's live calls, answered here; everything else falls through to the fixtures.
async function mockBuilder(page: Page, api: MockedApi) {
  await page.route((u) => u.pathname.startsWith("/api/v1/quality/") || u.pathname === "/api/v1/library/fit/preview", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    let body: unknown;
    try { body = req.postDataJSON(); } catch { body = undefined; }
    const json = (b: unknown) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(b) });
    switch (`${req.method()} ${url.pathname}`) {
      case "GET /api/v1/quality/profiles":
        api.calls.push({ method: "GET", path: url.pathname, body });
        return json({ ...qualityProfiles, vocabulary });
      case "POST /api/v1/quality/preview":
        return json({ profile: "", decision: { winner: null, eligible: [], rejected: [] } });
      case "POST /api/v1/library/fit/preview":
        return json({ scope: "library", counts: { titles: 0, files: 0, fits: 0, over: 0, under: 0, mismatch: 0 } });
      case "POST /api/v1/quality/formats/check": {
        api.calls.push({ method: "POST", path: url.pathname, body });
        const formats = (body as { custom_formats?: Format[] })?.custom_formats ?? [];
        const errors = formats.flatMap((f, index) => f.conditions.flatMap((c, condition) =>
          c.type === "release_title" && c.value.split("(").length !== c.value.split(")").length
            ? [{ format: f.name, index, condition, message: `condition ${condition + 1}'s release-name pattern isn't valid: missing closing )` }]
            : []));
        return json({ errors, warnings: [], matches: formats.map(() => 1), samples: 7 });
      }
    }
    return route.fallback();
  });
}

const sideways = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);

test.describe("custom-format editor at 375px", () => {
  test.use({ persona: "admin" });

  test("adds a format with several conditions without scrolling sideways", async ({ page, api }) => {
    await mockBuilder(page, api);
    await page.goto("/quality");
    await page.getByRole("button", { name: "+ New profile" }).click();
    await page.getByRole("button", { name: /Start from scratch/ }).click();
    await page.getByRole("button", { name: /^Advanced/ }).click();
    await page.getByRole("button", { name: "+ New custom format" }).click();

    await page.getByLabel("Format name").fill("Tier 1 remux groups with a long name");
    await page.getByRole("button", { name: "Match any" }).click();
    await page.getByLabel("Condition 1 type").selectOption("release_title");
    await page.getByLabel("Condition 1 value").fill("(FraMeSToR|EPSiLON");
    await expect(page.getByText(/pattern isn't valid/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Done" })).toBeDisabled();
    await page.getByLabel("Condition 1 value").fill("\\b(FraMeSToR|EPSiLON|playBD|BLURANiUM)\\b");
    await expect(page.getByText(/pattern isn't valid/)).toHaveCount(0);

    await page.getByRole("button", { name: "+ Add condition" }).click();
    await page.getByLabel("Condition 2 value").selectOption("Remux");
    await page.getByRole("button", { name: "Condition 2: NOT" }).click();
    expect(await sideways(page)).toBeLessThanOrEqual(0);

    await page.getByRole("button", { name: "Done" }).click();
    await expect(page.getByText("Tier 1 remux groups with a long name")).toBeVisible();
    await expect(page.getByText("Matches 1 of the 7 sample releases")).toBeVisible();
    expect(await sideways(page)).toBeLessThanOrEqual(0);

    // Editing changes it in place.
    await page.getByRole("button", { name: "Edit", exact: true }).click();
    await page.getByRole("button", { name: "Match all" }).click();
    await page.getByRole("button", { name: "Done" }).click();
    await expect(page.getByText(/AND NOT Source Remux/)).toBeVisible();
    expect(await sideways(page)).toBeLessThanOrEqual(0);
  });
});
