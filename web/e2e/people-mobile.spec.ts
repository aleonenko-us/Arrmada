import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";

// CFG-18: People & access works at phone width — each person's row stacks instead of
// pushing the page sideways.

// The layout viewport, not innerWidth: a phone zooms out to fit an overflowing page.
const overflow = (page: Page) => page.evaluate(() => {
  const main = document.querySelector("main");
  return {
    doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    main: main ? main.scrollWidth - main.clientWidth : 0,
  };
});

test.describe("admin on a phone", () => {
  test.use({ persona: "admin" });

  test("People & access fits 375 px", async ({ page, api }) => {
    await page.goto("/settings/users");
    const grandad = page.getByTestId("person").filter({ hasText: "Grandad" });
    await expect(grandad.getByText("Signed in on 2 browsers")).toBeVisible();
    await grandad.getByRole("button", { name: "More…" }).tap();
    await expect(grandad.getByRole("button", { name: "Set password" })).toBeVisible();
    await api.quiet();
    expect(await overflow(page)).toEqual({ doc: 0, main: 0 });
  });
});
