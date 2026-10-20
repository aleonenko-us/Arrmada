import { test, expect } from "./mockApi";
import { attention } from "./fixtures/system";

// The file check's findings in Needs you: each says what's wrong and what was done, with
// Find a better copy and Ignore, beside the "Replace bad copies automatically" switch. A
// few are listed one by one on the Dashboard card.
const link = "/activity/needs-you#file-checks";
const items = [
  { key: "filecheck:4", kind: "filecheck" as const, level: "warning" as const, id: 4, link, since: Date.now() - 3_600_000,
    title: "Resident Evil (2026): probably a cam — blocklisted it and looking for a better copy",
    detail: "Probably a cam — grabbed 3 weeks before it was out at home" },
  { key: "filecheck:5", kind: "filecheck" as const, level: "error" as const, id: 5, link, since: Date.now() - 86_400_000,
    title: "The Matrix (1999): the file has no sound — automatic replacement gave up after 3 tries" },
  { key: "filecheck:6", kind: "filecheck" as const, level: "warning" as const, id: 6, link, since: Date.now() - 7_200_000,
    title: "Heat (1995): audio is in Russian only" },
];
const withFiles = {
  ...attention,
  counts: { ...attention.counts, file_checks: 3, total: attention.counts.total + 3 },
  groups: [...attention.groups, { kind: "filecheck" as const, level: "error" as const, count: 3, title: "3 files look wrong inside", link }],
  items: [...attention.items, ...items],
};

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("Needs you lists files that look wrong, with what to do about each", async ({ page, api }) => {
    await page.route("**/api/v1/attention", (route) => route.fulfill({ json: withFiles }));
    await page.goto("/activity/needs-you#file-checks");
    const section = page.getByRole("region", { name: /Files that look wrong/ });
    await expect(section.getByText("Resident Evil (2026): probably a cam — blocklisted it and looking for a better copy")).toBeVisible();
    await expect(section.getByText("Probably a cam — grabbed 3 weeks before it was out at home")).toBeVisible();
    await expect(section.getByRole("switch", { name: "Replace bad copies automatically" })).toHaveAttribute("aria-checked", "true");
    await section.screenshot({ path: "test-results/file-checks-needs-you.png" });

    await section.getByRole("button", { name: "Find a better copy" }).first().click();
    await expect(page.getByText("Looking for a better copy — this file stays until one arrives.")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/file-checks/4/replace")).toHaveLength(1);

    await section.getByRole("switch", { name: "Replace bad copies automatically" }).click();
    await expect(section.getByRole("switch", { name: "Replace bad copies automatically" })).toHaveAttribute("aria-checked", "false");
  });

  test("the Dashboard card lists a few of them one by one", async ({ page }) => {
    await page.route("**/api/v1/attention", (route) => route.fulfill({ json: withFiles }));
    await page.goto("/");
    const card = page.getByRole("region", { name: /Needs you/ });
    await expect(card.getByText("Heat (1995): audio is in Russian only")).toBeVisible();
    await expect(card.getByText("The Matrix (1999): the file has no sound — automatic replacement gave up after 3 tries")).toBeVisible();
    await card.screenshot({ path: "test-results/file-checks-dashboard.png" });
  });
});
