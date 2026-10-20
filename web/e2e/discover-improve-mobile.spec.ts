import { test, expect } from "./mockApi";
import { justOut, requestableCard } from "./fixtures/discover";

// The newer Discover on a phone: no hover, so "Not interested" lives in the title sheet;
// the browse filters fold behind a "Filters" button and nothing scrolls sideways.

test.use({ persona: "requester" });

async function noSidewaysScroll(page: import("@playwright/test").Page) {
  const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(over).toBeLessThanOrEqual(0);
}

test("Not interested from the title sheet, with Undo",async ({ page, api }) => {
  await page.goto("/discover");
  await api.quiet();
  await expect(page.getByRole("heading", { name: "Just out at home" })).toBeVisible();
  await expect(page.getByRole("button", { name: `View details for ${justOut.items[0].title}` })).toBeVisible();
  // No hover on a phone: the corner button isn't there.
  await expect(page.getByRole("button", { name: `Not interested in ${requestableCard.title}` })).toHaveCount(0);

  await page.getByRole("button", { name: `View details for ${requestableCard.title}` }).first().click();
  await page.getByRole("button", { name: `Not interested in ${requestableCard.title}` }).click();
  await expect(page.getByRole("button", { name: `View details for ${requestableCard.title}` })).toHaveCount(0);
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(page.getByRole("button", { name: `View details for ${requestableCard.title}` }).first()).toBeVisible();
  await noSidewaysScroll(page);
});

test("browse filters fold behind Filters and fit the screen", async ({ page, api }) => {
  await page.goto("/discover/browse?list=popular&media=movie");
  await api.quiet();
  const toggle = page.getByRole("button", { name: /^Filters/ });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("combobox", { name: "Minimum rating" })).toBeHidden();
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await page.getByRole("combobox", { name: "Minimum rating" }).selectOption("8");
  await page.getByRole("combobox", { name: "Age rating" }).selectOption("PG");
  await expect(page).toHaveURL(/rating=8/);
  await expect(page).toHaveURL(/cert=PG(&|$)/);
  await expect(page.getByRole("button", { name: "Filters (2)" })).toBeVisible();
  await noSidewaysScroll(page);

  // The chips stay when the panel folds.
  await page.getByRole("button", { name: "Filters (2)" }).click();
  const chips = page.getByRole("group", { name: "Filters in use" });
  await chips.getByRole("button", { name: "Remove filter: ★ 8+" }).click();
  await expect(page).not.toHaveURL(/rating=/);
  await expect(page.getByRole("button", { name: "Filters (1)" })).toBeVisible();
  await noSidewaysScroll(page);
});
