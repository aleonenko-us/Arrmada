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

  // FE-05: a requester switches the theme from the avatar menu; it survives a reload
  // (stamped before first paint) and the browser chrome colour follows it.
  test("switches the theme in place and remembers it", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    await page.goto("/discover");
    const html = page.locator("html");
    await expect(html).not.toHaveAttribute("data-theme", /./);
    await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute("content", "#1f1812");
    await page.getByRole("button", { name: "Account menu" }).click();
    const menu = page.getByRole("menu", { name: "Account menu" });
    await menu.getByRole("menuitem", { name: "Theme: System" }).click();
    await expect(html).toHaveAttribute("data-theme", "light");
    // The menu stays open, showing the new choice.
    await expect(menu.getByRole("menuitem", { name: "Theme: Light" })).toBeVisible();
    await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute("content", "#fbf8f1");

    await page.reload();
    await expect(html).toHaveAttribute("data-theme", "light");
    await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute("content", "#fbf8f1");
  });
});
