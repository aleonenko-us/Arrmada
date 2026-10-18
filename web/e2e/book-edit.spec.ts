import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import { LIBRARY } from "./fixtures/booksLibrary";
import type { Book, BookEditField } from "../src/lib/api";

// Edit details: staff type over the details the catalogue got wrong, the catalogue's value
// stays underneath (marked "edited", shown as the hint, Reset to matched / Reset all), and
// an edit that changes what the files would be called offers the per-book rename preview
// instead of renaming anything itself.

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

// The Book property each editable field lives in.
const PROP: Record<BookEditField, keyof Book> = {
  title: "title", subtitle: "subtitle", author: "author", series_name: "series_name", series_position: "series_position",
  description: "description", year: "year", language: "language", subjects: "subjects", isbn: "isbn",
  narrator: "audiobook_narrator", publisher: "publisher",
};
type Val = string | number | string[];
const same = (a: unknown, b: unknown) => JSON.stringify(a ?? "") === JSON.stringify(b ?? "");

// mockBook answers one book's page and applies edits the way the server does: the first
// edit of a field keeps the shown value underneath; typing the catalogue's value back, or
// a reset, puts it back. Calls are recorded.
async function mockBook(page: Page, start: Book, opts: { refuse?: string } = {}) {
  const state = { book: JSON.parse(JSON.stringify(start)) as Book, edits: [] as unknown[], organize: [] as string[], applied: [] as unknown[], rematch: [] as unknown[] };
  const base = `/api/v1/books/${start.id}`;
  await page.route((u) => u.pathname.startsWith(base), (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path === `${base}/series`) return json(route, { name: "", entries: [], gaps: 0 });
    if (path === `${base}/history`) return json(route, { events: [] });
    if (path === `${base}/rematch` && req.method() === "POST") {
      state.rematch.push(req.postDataJSON());
      return json(route, state.book);
    }
    if (path === `${base}/details` && req.method() === "PUT") {
      const body = req.postDataJSON() as { set?: Partial<Record<BookEditField, Val>>; reset?: BookEditField[] };
      state.edits.push(body);
      if (opts.refuse) return json(route, { status: "error", message: opts.refuse }, 400);
      const b = state.book as unknown as Record<string, unknown>;
      const edited = { ...(state.book.edited ?? {}) } as Record<string, Val>;
      const changed: string[] = [];
      for (const k of body.reset ?? []) {
        if (k in edited) { b[PROP[k]] = edited[k]; delete edited[k]; changed.push(k); }
      }
      for (const [k, v] of Object.entries(body.set ?? {}) as [BookEditField, Val][]) {
        const under = k in edited ? edited[k] : b[PROP[k]] as Val;
        if (same(v, under)) { delete edited[k]; } else { edited[k] = under ?? ""; }
        if (!same(v, b[PROP[k]])) changed.push(k);
        b[PROP[k]] = v;
      }
      state.book = { ...state.book, edited };
      const rename = changed.some((k) => ["title", "author", "series_name", "series_position", "year", "narrator"].includes(k));
      return json(route, { book: state.book, changed, rename_available: rename });
    }
    if (path === base) return json(route, state.book);
    return route.fallback();
  });
  await page.route((u) => u.pathname === "/api/v1/books/organize", (route) => {
    const req = route.request();
    if (req.method() === "POST") {
      state.applied.push(req.postDataJSON());
      return json(route, { status: "organizing", total: 1, job_id: 4410 }, 202);
    }
    state.organize.push(new URL(req.url()).search);
    return json(route, {
      counts: { rename: 1, same: 1, conflict: 0, missing: 0 }, total: 1, offset: 0, token: "plan-dune",
      items: [{ media: "book", book_id: start.id, version_id: 0, kind: "audiobook", title: state.book.title, current: "/media/audiobooks/Frank Herbert/Dune/Dune.m4b", proposed: "/media/audiobooks/Frank Herbert/Dune Deluxe/Dune Deluxe.m4b", file_count: 1, status: "ok" }],
    });
  });
  await page.route((u) => u.pathname === "/api/v1/jobs/4410", (route) => json(route, {
    id: 4410, kind: "book.organize", target: "books", trigger: "user:2", status: "succeeded", progress: 1,
    message: "Renamed 1 edition", error: "", result: { total: 1, renamed: 1, skipped: 0 }, created_at: null, started_at: null, finished_at: null,
  }));
  await page.route((u) => u.pathname === "/api/v1/books/lookup", (route) => json(route, {
    source: "openlibrary", results: [{ key: "OL99W", title: "Dune", author: "Frank Herbert", year: 1965 }],
  }));
  return state;
}

async function openEdit(page: Page) {
  await page.getByRole("button", { name: "Fix", exact: true }).click();
  await page.getByRole("menuitem", { name: "Edit details…" }).click();
  const dialog = page.getByRole("dialog", { name: "Edit details" });
  await expect(dialog).toBeVisible();
  return dialog;
}

const dune: Book = { ...LIBRARY[0], description: "Spice and sand.", subjects: ["Science fiction"] };

test.describe("Edit details", () => {
  test.use({ persona: "manager" });

  test("an edit saves over the catalogue's details and offers the rename preview, never renaming itself", async ({ page }) => {
    const state = await mockBook(page, dune);
    await page.goto(`/books/${dune.id}`);
    await expect(page.getByTestId("book-edited")).toHaveCount(0);

    const dialog = await openEdit(page);
    await expect(dialog.getByTestId(/^edited-/)).toHaveCount(0); // nothing edited yet
    await dialog.getByRole("textbox", { name: "Title", exact: true }).fill("Dune Deluxe");
    await dialog.getByRole("textbox", { name: "Publisher", exact: true }).fill("Ace");
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(dialog).toHaveCount(0);
    expect(state.edits).toEqual([{ set: { title: "Dune Deluxe", publisher: "Ace" } }]);

    await expect(page.getByRole("heading", { name: "Dune Deluxe" })).toBeVisible();
    await expect(page.getByTestId("book-edited")).toBeVisible();
    await expect(page.getByText("Ace", { exact: true })).toBeVisible();
    await expect(page.getByText("Files can be renamed to match:")).toBeVisible();
    expect(state.organize).toEqual([]); // nothing planned, nothing moved, until asked

    await page.getByRole("button", { name: "Preview rename" }).click();
    const rename = page.getByRole("dialog", { name: "Rename this book's files" });
    await expect(rename.getByText("→ Frank Herbert/Dune Deluxe/Dune Deluxe.m4b")).toBeVisible();
    expect(state.organize).toEqual(["?offset=0&limit=200&book_id=11"]);
    await rename.getByRole("button", { name: "Rename 1 edition" }).click();
    await page.getByRole("dialog", { name: "Rename 1 edition of this book?" }).getByRole("button", { name: "Rename" }).click();
    await expect(rename.getByText("Renamed 1 edition")).toBeVisible();
    expect(state.applied).toEqual([{ token: "plan-dune", book_id: 11 }]);
    await rename.getByRole("button", { name: "Close" }).click();
    await expect(rename).toHaveCount(0);
    await expect(page.getByText("Files can be renamed to match:")).toHaveCount(0);
  });

  test("edited details are marked with the catalogue's value, and reset one at a time or all", async ({ page }) => {
    const state = await mockBook(page, {
      ...dune, title: "Dune: Deluxe Edition", series_name: "Dune Chronicles", series_position: 1.5,
      edited: { title: "Dune", series_position: 1 },
    });
    await page.goto(`/books/${dune.id}`);
    await expect(page.getByTestId("book-edited")).toBeVisible();

    let dialog = await openEdit(page);
    await expect(dialog.getByTestId("edited-title")).toBeVisible();
    await expect(dialog.getByTestId("edited-series_position")).toBeVisible();
    await expect(dialog.getByTestId("edited-publisher")).toHaveCount(0);
    await expect(dialog.getByText("Matched:", { exact: false }).first()).toContainText("Dune");
    await dialog.getByRole("button", { name: "Reset title to matched" }).click();
    await expect(dialog.getByRole("textbox", { name: "Title", exact: true })).toHaveValue("Dune");
    await expect(dialog.getByTestId("edited-title")).toHaveCount(0);
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(dialog).toHaveCount(0);
    expect(state.edits).toEqual([{ reset: ["title"] }]);
    await expect(page.getByRole("heading", { name: "Dune", exact: true })).toBeVisible();

    dialog = await openEdit(page);
    await expect(dialog.getByTestId("edited-title")).toHaveCount(0);
    await expect(dialog.getByTestId("edited-series_position")).toBeVisible();
    await dialog.getByRole("button", { name: "Reset all" }).click();
    await expect(dialog.getByRole("textbox", { name: "Number in series", exact: true })).toHaveValue("1");
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(dialog).toHaveCount(0);
    expect(state.edits[1]).toEqual({ reset: ["series_position"] });
    await expect(page.getByTestId("book-edited")).toHaveCount(0);
  });

  test("a refused edit says why and keeps the dialog open", async ({ page }) => {
    const state = await mockBook(page, dune, { refuse: "That ISBN's check digit doesn't match — check it for a typo" });
    await page.goto(`/books/${dune.id}`);
    const dialog = await openEdit(page);
    await dialog.getByRole("textbox", { name: "ISBN", exact: true }).fill("978-0-590-35340-4");
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect(dialog.getByRole("alert")).toContainText("check digit");
    await expect(dialog).toBeVisible();
    expect(state.edits).toEqual([{ set: { isbn: "978-0-590-35340-4" } }]);
  });

  test("Change match asks whether to keep the edits, keeping them by default", async ({ page }) => {
    const state = await mockBook(page, { ...dune, title: "Dune Deluxe", edited: { title: "Dune" } });
    await page.goto(`/books/${dune.id}`);
    await page.getByRole("button", { name: "Fix", exact: true }).click();
    await page.getByRole("menuitem", { name: "Change match…" }).click();
    const dialog = page.getByRole("dialog", { name: /Change match/ });
    await expect(dialog.getByRole("radio", { name: /Keep my edits/ })).toBeChecked();
    await dialog.getByRole("radio", { name: /Clear my edits/ }).check();
    await dialog.getByRole("button", { name: "Use this" }).click();
    await expect(dialog).toHaveCount(0);
    expect(state.rematch).toEqual([{ ol_key: "OL99W", title: "Dune", author: "Frank Herbert", year: 1965, cover_url: "", edits: "clear" }]);
  });
});
