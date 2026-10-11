import { test, expect } from "./mockApi";

// FE-13: the requester shell's avatar menu is the kit's Menu — it works from the keyboard.
test.describe("account menu", () => {
  test.use({ persona: "requester" });

  test("opens with Enter, moves with the arrows, closes with Escape", async ({ page }) => {
    await page.goto("/discover");
    const trigger = page.getByRole("button", { name: "Account menu" });
    await trigger.focus();
    await page.keyboard.press("Enter");
    const menu = page.getByRole("menu", { name: "Account menu" });
    await expect(menu).toBeVisible();
    await expect(menu.getByRole("menuitem", { name: "Me & notifications" })).toBeFocused();
    await page.keyboard.press("ArrowDown");
    await expect(menu.getByRole("menuitem", { name: "Password & devices" })).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();
    await expect(trigger).toBeFocused();

    // A pick goes where it says.
    await trigger.click();
    await menu.getByRole("menuitem", { name: "Me & notifications" }).click();
    await expect(page).toHaveURL(/\/me$/);
  });
});
