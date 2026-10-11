import { test, expect } from "./mockApi";

// MOV-18: on a phone (no hover) a movie can be deleted from the grid and from its own
// page — through a ⋯ menu that's always visible, never a hover-only control. (Below 640 px
// the library shows the grid only; the table's row menu is pinned on desktop.)
test.describe("manager on a phone", () => {
  test.use({ persona: "manager" });

  const removeFromDialog = async (page: import("@playwright/test").Page, title: string) => {
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(`Remove “${title}”?`)).toBeVisible();
    await dialog.getByRole("button", { name: "Remove", exact: true }).click();
  };

  test("Delete is reachable from the grid", async ({ page, api }) => {
    await page.goto("/movies");
    const trigger = page.getByRole("button", { name: "Actions for Anchor Point" });
    await expect(trigger).toBeVisible();
    await trigger.click();
    await page.getByRole("menuitem", { name: "Remove…" }).click();
    await removeFromDialog(page, "Anchor Point");
    await expect.poll(() => api.callsTo("DELETE", "/api/v1/movies/2").length).toBe(1);
  });

  test("Delete is reachable from the movie's page, which then goes back to Movies", async ({ page, api }) => {
    await page.goto("/movies/2");
    // One primary action for a monitored film with no file: Search.
    await expect(page.getByRole("button", { name: "Search", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "More actions" }).click();
    const item = page.getByRole("menuitem", { name: "Delete movie…" });
    await expect(item).toBeInViewport();
    await item.click();
    await removeFromDialog(page, "Anchor Point");
    await expect.poll(() => api.callsTo("DELETE", "/api/v1/movies/2").length).toBe(1);
    await expect(page).toHaveURL(/\/movies$/);
  });
});
