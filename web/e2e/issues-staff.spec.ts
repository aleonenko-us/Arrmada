import type { Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import { NOW } from "./fixtures/clock";

// REQ-23: staff open a reported problem from Requests → Issues and fix it in one click.
// The replace asks first; each action calls its endpoint and the issue shows Fixing.

const at = Math.floor(NOW / 1000) - 3600;
const issue = {
  id: 7, media_type: "series", tmdb_id: 1015, library_id: 21, title: "The Lighthouse Keepers", season: 1, episode: 4,
  category: "audio", status: "open", created_at: at, updated_at: at, reporter_count: 2, note: "no English audio",
  reported_by_name: "sam", reporters: [{ user_id: 3, name: "sam", note: "no English audio", at }, { user_id: 4, name: "alex", at: at + 60 }],
};

test.describe("staff on a desktop", () => {
  test.use({ persona: "admin" });

  test("blocklists the copy from an issue, after a confirm", async ({ page, api }) => {
    const fixes: unknown[] = [];
    let current: Record<string, unknown> = { ...issue };
    await page.route((u) => u.pathname === "/api/v1/issues", (route: Route) =>
      route.fulfill({ json: { issues: [current], total: 1, counts: { open: 1, closed: 0 } } }));
    await page.route((u) => u.pathname === "/api/v1/issues/7/fix", (route: Route) => {
      fixes.push(route.request().postDataJSON());
      current = { ...issue, status: "fixing", action: "replace", action_note: "Blocklisted this episode's copy; searching for another.", action_at: at + 120, action_file: "/tv/Keepers/S01E04.mkv" };
      return route.fulfill({ json: current });
    });

    await page.goto("/requests?tab=issues");
    await api.quiet();
    await expect(page.getByRole("tab", { name: /Issues/ })).toHaveAttribute("aria-selected", "true");
    await page.getByRole("button", { name: "Open the problem with The Lighthouse Keepers S01E04" }).click();
    const sheet = page.getByRole("dialog", { name: "Problem with The Lighthouse Keepers" });
    await expect(sheet.getByText("no English audio")).toBeVisible();

    // Cancelling the confirm does nothing.
    await sheet.getByRole("button", { name: "Blocklist this copy & find another" }).click();
    await page.getByRole("button", { name: "Cancel" }).click();
    expect(fixes).toHaveLength(0);

    await sheet.getByRole("button", { name: "Blocklist this copy & find another" }).click();
    await page.getByRole("button", { name: "Blocklist and search" }).click();
    await expect(sheet.getByText("Fixing", { exact: false }).first()).toBeVisible();
    await expect(sheet.getByText(/searching for another/)).toBeVisible();
    expect(fixes).toEqual([{ action: "replace" }]);
  });
});
