import { test, expect } from "./mockApi";

// CFG-26: System → Status names the build with its commit, the database size, the last
// backup and each drive once; the Dashboard's System card shows the version with its
// commit and (for admins) the last backup, and no Auth stat.

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("Status shows the build, database, last backup and drives", async ({ page }) => {
    await page.goto("/settings/status");
    const card = page.locator("#status");
    await expect(card.getByText("2026.10.08 (abc1234)")).toBeVisible();
    await expect(card.getByText("180 MB")).toBeVisible();
    await expect(card.getByRole("link", { name: "3 h ago" })).toBeVisible();
    // The array holding Movies, TV and Books is one drive, not three.
    await expect(card.getByText("Movies · TV · Books & audiobooks")).toBeVisible();
    await expect(card.getByRole("progressbar")).toHaveCount(3);
  });

  // CFG-29: how far behind main this build is, the newest release's notes, and the switch.
  test("Updates says how far behind main the build is and can be switched off", async ({ page, api }) => {
    await page.goto("/settings/status");
    const card = page.locator("#updates");
    await expect(card.getByText("3 commits behind main.")).toBeVisible();
    await card.getByText("Newest release: v1.3.0").click();
    await expect(card.getByText("• Faster imports by @someone")).toBeVisible();
    await expect(card.getByRole("link", { name: "v1.3.0 on GitHub →" })).toHaveAttribute("href", "https://github.com/tristenlammi/Arrmada/releases/tag/v1.3.0");
    await card.getByRole("switch", { name: "Check for updates" }).click();
    await expect(card.getByText("The update check is off.")).toBeVisible();
    await expect(card.getByRole("button", { name: "Check now" })).toBeDisabled();
    expect(api.callsTo("PUT", "/api/v1/system/update-check")).toHaveLength(1);
    expect(api.callsTo("POST", "/api/v1/system/update-check")).toHaveLength(0);
  });

  test("the Dashboard's System card has the commit and the last backup, no Auth", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByText("e2e (e2e0000)")).toBeVisible();
    await expect(page.getByText("Last backup", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "3 h ago" })).toBeVisible();
    await expect(page.getByText("Auth", { exact: true })).toHaveCount(0);
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("the Dashboard doesn't ask for the admin-only status", async ({ page, api }) => {
    await page.goto("/");
    await expect(page.getByText("Realtime")).toBeVisible();
    await expect(page.getByText("Last backup", { exact: true })).toHaveCount(0);
    expect(api.callsTo("GET", "/api/v1/system/status")).toHaveLength(0);
  });
});
