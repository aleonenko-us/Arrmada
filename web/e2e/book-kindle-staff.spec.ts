import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import { LIBRARY } from "./fixtures/booksLibrary";
import type { Book } from "../src/lib/api";

// Send to Kindle for staff: the ebook edition on the book page has the same button My shelf
// has — the first-time sheet explaining Amazon's approved-senders list, then the send to
// the signed-in person's own Kindle — disabled with the reason for a format or size Kindle
// email won't take. Staff keep their Kindle address on the Me page, and the inbox's link
// for a send result (/shelf, a requester page) lands staff on Books.

const epub = { path: "/books/dune.epub", format: "EPUB", size_bytes: 2_000_000, file_count: 1, kindle: true };
const BOOKS: Record<number, Book> = {
  11: { ...LIBRARY[0], audiobook: undefined, ebook: epub },
  12: { ...LIBRARY[1], ebook: { ...epub, path: "/books/anathem.azw3", format: "AZW3", kindle: false } },
  14: { ...LIBRARY[1], id: 14, title: "The Baroque Cycle", ebook: { ...epub, path: "/books/baroque.pdf", format: "PDF", size_bytes: 80_000_000 } },
};

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

// mockBooks answers the book pages above, and records sends and address saves.
async function mockBooks(page: Page) {
  const state = { sent: [] as string[], saved: [] as string[] };
  await page.route((u) => /^\/api\/v1\/books\/\d+(\/series|\/history)?$/.test(u.pathname), (route) => {
    const [, id, rest] = new URL(route.request().url()).pathname.match(/^\/api\/v1\/books\/(\d+)(\/series|\/history)?$/) ?? [];
    if (rest === "/series") return json(route, { name: "", entries: [], gaps: 0 });
    if (rest === "/history") return json(route, { events: [] });
    const b = BOOKS[Number(id)];
    return b ? json(route, b) : json(route, { message: "book not found" }, 404);
  });
  await page.route((u) => u.pathname === "/api/v1/me/kindle" || u.pathname.endsWith("/send-to-kindle"), (route) => {
    const req = route.request();
    if (req.method() === "PUT") {
      const addr = (req.postDataJSON() as { kindle_email: string }).kindle_email;
      state.saved.push(addr);
      return json(route, { kindle_email: addr, from_address: "books@example.com", smtp_configured: true });
    }
    if (req.method() === "POST") {
      state.sent.push(new URL(req.url()).pathname);
      return json(route, { status: "sending", existing: false }, 202);
    }
    return route.fallback();
  });
  return state;
}

for (const persona of ["admin", "manager"] as const) {
  test.describe(`Send to Kindle as ${persona}`, () => {
    test.use({ persona });

    test("the ebook edition sends to my Kindle, asking for the address the first time", async ({ page }) => {
      const state = await mockBooks(page);
      await page.goto("/books/11");
      const send = page.getByRole("button", { name: "Send to Kindle" });
      await expect(send).toBeEnabled();
      expect(state.sent).toEqual([]); // nothing goes out by looking

      await send.click();
      const setup = page.getByRole("dialog", { name: "Send to Kindle" });
      await expect(setup).toContainText("Approved Personal Document E-mail List");
      await expect(setup).toContainText("books@example.com");
      expect(state.sent).toEqual([]);
      await setup.getByPlaceholder("name_42@kindle.com").fill("captain_7@kindle.com");
      await setup.getByRole("button", { name: /Save and send/ }).click();
      await expect.poll(() => state.sent).toEqual(["/api/v1/me/books/11/send-to-kindle"]);
      expect(state.saved).toEqual(["captain_7@kindle.com"]);
      await expect(page.getByText(/Sending “Dune”/)).toBeVisible();
    });

    test("formats and sizes Kindle email won't take are disabled with the reason", async ({ page }) => {
      const state = await mockBooks(page);
      await page.goto("/books/12");
      await expect(page.getByRole("button", { name: "Send to Kindle" })).toBeDisabled();
      await expect(page.getByText("This ebook is AZW3 — Kindle email takes EPUB or PDF")).toBeVisible();

      await page.goto("/books/14");
      await expect(page.getByRole("button", { name: "Send to Kindle" })).toBeDisabled();
      await expect(page.getByText("This ebook is over 50 MB, more than Kindle email takes")).toBeVisible();
      expect(state.sent).toEqual([]);
    });

    test("the Kindle address lives on the Me page, and a send's inbox link lands on Books", async ({ page }) => {
      await page.goto("/me");
      const card = page.getByRole("region", { name: "Send to Kindle" });
      await expect(card).toContainText("Approved Personal Document E-mail List");
      await expect(card.getByPlaceholder("name_42@kindle.com")).toBeVisible();

      await page.goto("/shelf");
      await expect(page).toHaveURL(/\/books$/);
    });
  });
}
