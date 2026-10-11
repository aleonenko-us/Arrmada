import { test, expect } from "./mockApi";

// SER-17 on a phone: every series card's actions are reachable without hover, the cards
// say where a show airs and when, and the grid's sort is remembered across a reload.
test.describe("series library on a phone", () => {
  test.use({ persona: "manager" });

  test("card menus without hover, network and next airing, a remembered sort", async ({ page, api }) => {
    await page.goto("/series");
    const titles = page.locator("main a[href^='/series/'] .truncate.font-semibold");
    await expect(titles.first()).toBeVisible();

    // Shown, not hover-revealed: a touch screen has no hover.
    const trigger = page.getByRole("button", { name: "Actions for Saltwind" });
    await expect(trigger).toBeVisible();
    await expect(trigger.locator("xpath=..").locator("xpath=..")).toHaveCSS("opacity", "1");
    await trigger.click();
    await expect(page.getByRole("menuitem", { name: "Search missing" })).toBeVisible();
    await page.getByRole("menuitem", { name: "Search missing" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/series/2/search").length).toBe(1);

    // Network and next airing for shows still airing; year and seasons for the ended one.
    await expect(page.getByText(/^Coastal · Next /)).toBeVisible();
    await expect(page.getByText("2015 · 4 sns")).toBeVisible();

    await page.getByRole("combobox", { name: "Sort" }).selectOption("next");
    await expect(titles).toHaveText(["Saltwind", "Harbour Lights", "The Lighthouse Keepers"]);
    await page.reload();
    await expect(page.getByRole("combobox", { name: "Sort" })).toHaveValue("next");
    await expect(titles).toHaveText(["Saltwind", "Harbour Lights", "The Lighthouse Keepers"]);
  });
});
