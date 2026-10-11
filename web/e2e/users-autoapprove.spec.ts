import { test, expect } from "./mockApi";

// REQ-09: auto-approve is per media type. The people list flags a Plex sign-in that still
// approves whole shows; each type saves on its own from the row, and the Plex sign-in
// default lives in Settings → Plex.
test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("tightens a Plex user to movies only and sets the sign-in default", async ({ page, api }) => {
    await page.goto("/settings/users");
    await expect(page.getByText("Approves whole shows")).toBeVisible();

    const checks = page.getByRole("group", { name: "Auto-approve Grandad's requests for" });
    await expect(checks.getByRole("checkbox", { name: "Series" })).toBeChecked();
    await checks.getByRole("checkbox", { name: "Series" }).uncheck();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/users/5").length).toBe(1);
    await checks.getByRole("checkbox", { name: "Books" }).uncheck();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/users/5").length).toBe(2);
    expect(api.callsTo("PUT", "/api/v1/users/5").map((c) => c.body)).toEqual([
      { auto_approve_movie: true, auto_approve_series: false, auto_approve_book: true },
      { auto_approve_movie: true, auto_approve_series: false, auto_approve_book: false },
    ]);
    await expect(page.getByRole("button", { name: "Save changes" })).toHaveCount(0);

    // The Plex sign-in default (in Settings → Plex) starts at movies only.
    await page.getByRole("link", { name: "Settings → Plex" }).click();
    await expect(page).toHaveURL(/\/settings\/plex#plex-sign-in$/);
    const plex = page.getByRole("group", { name: "New Plex sign-ins auto-approve" });
    await expect(plex.getByRole("checkbox", { name: "Movies" })).toBeChecked();
    await expect(plex.getByRole("checkbox", { name: "Series" })).not.toBeChecked();
  });
});
