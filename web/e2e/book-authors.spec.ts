import type { Page, Route } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { Book, BookAuthor, BookDiscoverCard } from "../src/lib/api";
import type { LibraryAuthor } from "../src/lib/bookAuthorApi";

// BOOK-20: author pages by id. An author with no stored catalogue author asks "Which
// author is this?" instead of guessing, and the pick persists; old /books/author/<name>
// links go to the id page, or ask which author when the name could be more than one;
// Monitor / 'Add new books automatically' persist; everything fits a 375px phone.

const book = (id: number, title: string, author: string, author_id?: number, has_file = false): Book => ({
  id, ol_key: `OL${id}W`, title, author, author_id, year: 2010, monitored: true, quality_profile: "",
  has_file, want_ebook: true, want_audiobook: false, search_misses: 0,
});

const LIBRARY: Book[] = [
  book(501, "The Final Empire", "Brandon Sanderson", 7, true),
  book(502, "Elantris", "Sanderson, Brandon", 7),
  book(503, "A Book by Doe", "Doe"),
];

// Two catalogue authors share the name: the page must not pick one by itself.
const CANDIDATES: BookAuthor[] = [
  { key: "hc:a:1", name: "Brandon Sanderson", work_count: 120, top_work: "Mistborn" },
  { key: "hc:a:2", name: "Brandon Sanderson", work_count: 2, top_work: "A Cookbook" },
];

const WORKS: BookDiscoverCard[] = [
  { key: "hc:10", title: "The Way of Kings", author: "Brandon Sanderson", year: 2010, in_library: false, has_file: false, requested: false },
];

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

// mockAuthors answers the author endpoints from a little in-memory store, so a PUT is
// still there after a reload.
async function mockAuthors(page: Page) {
  const authors: Record<number, LibraryAuthor> = {
    7: { id: 7, name: "Brandon Sanderson", catalogue_key: "", monitored: false, monitor_new: false, quality_profile: "", books: 2, owned: 1, wanted: 1, names: ["Brandon Sanderson", "Sanderson, Brandon"] },
    8: { id: 8, name: "J. Doe", catalogue_key: "", monitored: false, monitor_new: false, quality_profile: "", books: 1, owned: 0, wanted: 1 },
    9: { id: 9, name: "K. Doe", catalogue_key: "", monitored: false, monitor_new: false, quality_profile: "", books: 1, owned: 0, wanted: 1 },
  };
  const puts: Array<{ id: number; body: Partial<LibraryAuthor> }> = [];
  await page.route((u) => u.pathname === "/api/v1/books", (route) => json(route, { books: LIBRARY, metadata_available: true, upgradable: 0 }));
  await page.route((u) => u.pathname.startsWith("/api/v1/book-authors"), async (route) => {
    const url = new URL(route.request().url());
    const id = Number(url.pathname.split("/").pop());
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON() as Partial<LibraryAuthor>;
      puts.push({ id, body });
      authors[id] = { ...authors[id], ...body };
      return json(route, authors[id]);
    }
    if (url.pathname === "/api/v1/book-authors") {
      const name = url.searchParams.get("name") ?? "";
      const byName: Record<string, number[]> = { "Brandon Sanderson": [7], "Sanderson, Brandon": [7], Doe: [8, 9] };
      return json(route, { authors: (name ? byName[name] ?? [] : [7, 8, 9]).map((i) => authors[i]) });
    }
    return authors[id] ? json(route, authors[id]) : json(route, { message: "author not found" }, 404);
  });
  await page.route((u) => u.pathname === "/api/v1/books/discover/authors", (route) => json(route, { authors: CANDIDATES }));
  await page.route((u) => u.pathname.startsWith("/api/v1/books/discover/authors/"), (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/works")) return json(route, { author_key: "hc:a:1", books: WORKS });
    return json(route, { key: "hc:a:1", name: "Brandon Sanderson", work_count: 120, bio: "Writes long books." });
  });
  await page.route((u) => u.pathname === "/api/v1/quality/profiles", (route) => json(route, {
    profiles: [
      { key: "book-ebook", name: "Ebook", media_type: "book", built_in: true, is_default: true, summary: "" },
      { key: "book-audio", name: "Audiobook", media_type: "book", built_in: true, is_default: false, summary: "" },
    ],
    formats: [],
  }));
  return puts;
}

async function open(page: Page, path: string, api: MockedApi) {
  await page.goto(path);
  await expect(page.getByRole("main")).toBeVisible();
  await api.quiet();
}

const fitsScreen = async (page: Page) => {
  const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, viewport: document.documentElement.clientWidth }));
  expect(w.doc, "document width").toBeLessThanOrEqual(w.viewport);
};

test.describe("author pages", () => {
  test.use({ persona: "admin" });

  test("an author with no stored catalogue author asks which one, and the pick persists", async ({ page, api }) => {
    const puts = await mockAuthors(page);
    await open(page, "/books/authors/7", api);

    await expect(page.getByRole("heading", { name: "Brandon Sanderson", level: 1 }).first()).toBeVisible();
    // Both namesakes are offered; no catalogue is shown as theirs until someone picks.
    const picker = page.getByRole("region", { name: "Which author is this?" });
    await expect(picker).toBeVisible();
    await expect(picker.getByRole("button", { name: /Mistborn/ })).toBeVisible();
    await expect(picker.getByRole("button", { name: /A Cookbook/ })).toBeVisible();
    await expect(page.getByRole("heading", { name: /More by/ })).toHaveCount(0);
    // The owned list is the books linked to this author, under either spelling.
    await expect(page.getByRole("link", { name: /The Final Empire/ })).toBeVisible();
    await expect(page.getByRole("link", { name: /Elantris/ })).toBeVisible();
    await fitsScreen(page);

    await picker.getByRole("button", { name: /Mistborn/ }).click();
    await expect(picker).toHaveCount(0);
    expect(puts).toEqual([{ id: 7, body: { catalogue_key: "hc:a:1" } }]);
    await expect(page.getByRole("heading", { name: /More by Brandon Sanderson/ })).toBeVisible();
    await expect(page.getByTitle("The Way of Kings")).toBeVisible();

    // A fresh visit uses the stored author straight away.
    await open(page, "/books/authors/7", api);
    await expect(page.getByRole("region", { name: "Which author is this?" })).toHaveCount(0);
    await expect(page.getByTitle("The Way of Kings")).toBeVisible();
    await fitsScreen(page);
  });

  test("Monitor and 'Add new books automatically' persist across a reload", async ({ page, api }) => {
    const puts = await mockAuthors(page);
    await open(page, "/books/authors/7", api);
    const monitor = page.getByRole("switch", { name: "Monitor" });
    const auto = page.getByRole("switch", { name: "Add new books automatically" });
    await expect(monitor).toHaveAttribute("aria-checked", "false");
    await monitor.click();
    await expect(monitor).toHaveAttribute("aria-checked", "true");
    await auto.click();
    await expect(auto).toHaveAttribute("aria-checked", "true");
    expect(puts.map((p) => p.body)).toEqual([{ monitored: true }, { monitor_new: true }]);

    await open(page, "/books/authors/7", api);
    await expect(page.getByRole("switch", { name: "Monitor" })).toHaveAttribute("aria-checked", "true");
    await expect(page.getByRole("switch", { name: "Add new books automatically" })).toHaveAttribute("aria-checked", "true");
  });

  test("an old by-name link goes to the author's page", async ({ page, api }) => {
    await mockAuthors(page);
    await open(page, `/books/author/${encodeURIComponent("Sanderson, Brandon")}`, api);
    await expect(page).toHaveURL(/\/books\/authors\/7$/);
    await expect(page.getByRole("heading", { name: "Brandon Sanderson", level: 1 }).first()).toBeVisible();
  });

  test("an old link whose name could be two authors asks which", async ({ page, api }) => {
    await mockAuthors(page);
    await open(page, "/books/author/Doe", api);
    const choose = page.getByRole("region", { name: "Which author is this?" });
    await expect(choose.getByRole("link", { name: /J\. Doe/ })).toBeVisible();
    await expect(choose.getByRole("link", { name: /K\. Doe/ })).toBeVisible();
    await expect(page).toHaveURL(/\/books\/author\/Doe$/);
    await fitsScreen(page);
    await choose.getByRole("link", { name: /K\. Doe/ }).click();
    await expect(page).toHaveURL(/\/books\/authors\/9$/);
  });

  test("the library groups by author, spellings together, and links tiles by id", async ({ page, api }) => {
    await mockAuthors(page);
    await open(page, "/books", api);
    // One Sanderson tile for both spellings, linked by id; the unlinked name keeps the
    // by-name link.
    const tile = page.locator('a[href="/books/authors/7"]');
    await expect(tile).toHaveCount(1);
    await expect(tile).toContainText("2 books");
    await expect(page.locator('a[href="/books/author/Doe"]')).toHaveCount(1);
    await fitsScreen(page);
  });
});
