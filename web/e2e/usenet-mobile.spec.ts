import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { mockUsenet, SAB_ID } from "./fixtures/usenet";

// Usenet U4 at phone width: the SABnzbd card, its add form and the queue's usenet rows fit a
// 375 px screen without scrolling sideways.

const SHOTS = process.env.USENET_SHOTS;
async function shot(page: Page, name: string) {
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: false });
}

async function noSidewaysScroll(page: Page) {
  const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
  expect(w.doc, "document width").toBeLessThanOrEqual(w.viewport);
}

test.describe("admin, phone", () => {
  test.use({ persona: "admin" });
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("arrmada.theme", "dark"));
  });

  test("the add form's kind picker and the SABnzbd card fit the screen", async ({ page, api }) => {
    await mockUsenet(page, api);
    await page.goto("/settings/connections");
    await page.getByRole("button", { name: "+ Add client" }).click();
    const form = page.getByRole("form", { name: "Add a download client" });
    await form.getByRole("radio", { name: /SABnzbd/ }).check();
    await expect(form.getByLabel("API key")).toBeVisible();
    await noSidewaysScroll(page);
    await page.getByRole("button", { name: "Cancel" }).click();

    const card = page.locator(`#download_client-${SAB_ID}`);
    await card.getByRole("button", { name: "Edit" }).click();
    const facts = card.getByRole("region", { name: "SABnzbd" });
    await expect(facts.getByRole("button", { name: "Add missing categories" })).toBeVisible();
    await facts.evaluate((el) => el.scrollIntoView({ block: "start" }));
    await noSidewaysScroll(page);
    await shot(page, "usenet-card-phone-dark");
  });

  test("usenet rows in the queue fit, with Retry on the failed one", async ({ page, api }) => {
    await mockUsenet(page, api);
    await page.goto("/activity/queue");
    const failed = page.getByTestId("transfer-card").filter({ hasText: "Glasshouse" });
    await expect(failed.getByRole("button", { name: "Retry" })).toBeVisible();
    await expect(failed.getByTestId("usenet-chip")).toBeVisible();
    await noSidewaysScroll(page);
    await shot(page, "usenet-queue-phone-dark");
  });
});
