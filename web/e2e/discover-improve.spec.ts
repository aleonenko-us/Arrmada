import { test, expect } from "./mockApi";
import { justOut, requestableCard } from "./fixtures/discover";

// The newer Discover on a desktop: "Not interested" with Undo and the Hidden titles list,
// the new rows (Just out at home, the Plex watchlist, Request all on a collection), and
// the browse grid's filters kept in the address as removable chips.

test.describe("Not interested", () => {
  test.use({ persona: "requester" });

  test("hides a title, Undo puts it back, and Hidden titles restores it", async ({ page, api }) => {
    await page.goto("/discover");
    await api.quiet();
    const title = requestableCard.title;
    const card = page.getByRole("button", { name: `View details for ${title}` });
    await expect(card.first()).toBeVisible();

    // The corner button only shows on hover.
    await card.first().hover();
    await page.getByRole("button", { name: `Not interested in ${title}` }).first().click();
    await expect(card).toHaveCount(0);
    await expect(page.getByText(`Hidden “${title}” from your Discover`)).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/discover/hidden")).toHaveLength(1);

    // Undo: back on the page, and the server forgets it.
    await page.getByRole("button", { name: "Undo" }).click();
    await expect(card.first()).toBeVisible();
    await expect.poll(() => api.callsTo("DELETE", `/api/v1/discover/hidden/movie/${requestableCard.tmdb_id}`).length).toBe(1);

    // Hide it again, and restore it from the list at the foot of the page.
    await card.first().hover();
    await page.getByRole("button", { name: `Not interested in ${title}` }).first().click();
    await expect(card).toHaveCount(0);
    const link = page.getByRole("button", { name: "Hidden titles (1)" });
    await link.scrollIntoViewIfNeeded();
    await link.click();
    const sheet = page.getByRole("dialog", { name: "Hidden titles" });
    await expect(sheet.getByText(title)).toBeVisible();
    await sheet.getByRole("button", { name: `Restore ${title}` }).click();
    await expect(page.getByText(`“${title}” is back on your Discover`)).toBeVisible();
    await expect(sheet.getByText(title)).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(card.first()).toBeVisible();
  });

  test("the title sheet offers it too", async ({ page, api }) => {
    await page.goto("/discover");
    await api.quiet();
    await page.getByRole("button", { name: `View details for ${requestableCard.title}` }).first().click();
    await page.getByRole("button", { name: `Not interested in ${requestableCard.title}` }).last().click();
    await expect(page.getByRole("button", { name: `View details for ${requestableCard.title}` })).toHaveCount(0);
  });
});

test.describe("new rows", () => {
  test.use({ persona: "requester" });

  test("a requester sees Just out at home and the honest Upcoming note, but no watchlist", async ({ page, api }) => {
    await page.goto("/discover");
    await api.quiet();
    await expect(page.getByRole("heading", { name: "Just out at home" })).toBeVisible();
    await expect(page.getByRole("button", { name: `View details for ${justOut.items[0].title}` })).toBeVisible();
    await expect(page.getByText("Fetched once it’s out at home.").first()).toBeAttached();
    await expect(page.getByRole("heading", { name: "Your Plex watchlist" })).toHaveCount(0);
  });
});

test.describe("staff rows", () => {
  test.use({ persona: "admin" });

  test("the Plex watchlist row and its switch, and Request all on a collection", async ({ page, api }) => {
    await page.goto("/discover");
    await api.quiet();
    await expect(page.getByRole("heading", { name: "Your Plex watchlist" })).toBeVisible();
    await expect(page.getByRole("button", { name: "View details for Watchlisted Comet" })).toBeVisible();
    const auto = page.getByRole("switch", { name: /Request automatically/ });
    await expect(auto).not.toBeChecked();
    await auto.click();
    await expect(auto).toBeChecked();
    expect(api.callsTo("PUT", "/api/v1/discover/plex-watchlist/auto")[0].body).toEqual({ on: true });

    const all = page.getByRole("button", { name: /^Request all \d+ missing films from Harbour Collection$/ });
    await all.scrollIntoViewIfNeeded();
    await all.click();
    await expect(page.getByText(/^Requested \d+ films from Harbour Collection$/)).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/requests").length).toBeGreaterThanOrEqual(2);
  });
});

test.describe("browse filters", () => {
  test.use({ persona: "requester" });

  test("live in the address, show as chips, and survive a title and Back", async ({ page, api }) => {
    await page.goto("/discover/browse?list=popular&media=movie");
    await api.quiet();
    await page.getByRole("combobox", { name: "Minimum rating" }).selectOption("7");
    await page.getByRole("combobox", { name: "Minimum number of votes" }).selectOption("1000");
    await page.getByRole("combobox", { name: "Age rating" }).selectOption("PG-13");
    await page.getByRole("checkbox", { name: "Hide requested" }).check();
    await expect(page).toHaveURL(/rating=7/);
    await expect(page).toHaveURL(/votes=1000/);
    await expect(page).toHaveURL(/cert=PG-13/);
    await expect(page).toHaveURL(/hide_requested=1/);
    await api.quiet();
    const browse = api.callsTo("GET", "/api/v1/discover/browse").at(-1)!.path;
    expect(browse).toContain("votes=1000");
    expect(browse).toContain("cert=PG-13");
    expect(browse).not.toContain("hide_requested");

    // Requested titles are gone from the grid; others stay.
    await expect(page.getByRole("button", { name: "View details for Saltwind" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: `View details for ${requestableCard.title}` })).toBeVisible();

    const chips = page.getByRole("group", { name: "Filters in use" });
    await expect(chips.getByRole("button", { name: "Remove filter: ★ 7+" })).toBeVisible();
    await chips.getByRole("button", { name: "Remove filter: 1,000+ votes" }).click();
    await expect(page).not.toHaveURL(/votes=/);
    await expect(chips.getByRole("button", { name: "Remove filter: 1,000+ votes" })).toHaveCount(0);

    // A title opened from the grid and closed with Back keeps the filters.
    await page.getByRole("button", { name: `View details for ${requestableCard.title}` }).click();
    await api.quiet();
    await page.goBack();
    await expect(page).toHaveURL(/cert=PG-13/);
    await expect(chips.getByRole("button", { name: "Remove filter: Rated up to PG-13" })).toBeVisible();

    // A shared link opens with the same filters.
    await page.reload();
    await api.quiet();
    await expect(page.getByRole("combobox", { name: "Age rating" })).toHaveValue("PG-13");
    await expect(page.getByRole("checkbox", { name: "Hide requested" })).toBeChecked();

    await chips.getByRole("button", { name: "Clear filters" }).click();
    await expect(page.getByRole("group", { name: "Filters in use" })).toHaveCount(0);
  });
});
