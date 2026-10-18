import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import { settings } from "./fixtures/system";
import { LIBRARY } from "./fixtures/booksLibrary";
import type { Book } from "../src/lib/api";

// KEPUB for Kobo: an EPUB gets a "Download for Kobo (KEPUB)" beside its download, on My
// shelf's book sheet and on the staff book page, pointing at the converting endpoint; other
// formats don't. Settings → Book server has the switch, on by default, saving itself.

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

test.describe("My shelf", () => {
  test.use({ persona: "requester" });

  test("an EPUB offers the Kobo download; an AZW3 doesn't", async ({ page }) => {
    await page.goto("/shelf?book=1");
    const sheet = page.getByRole("dialog", { name: "A Field Guide to Tides" });
    await expect(sheet.getByRole("link", { name: "Download EPUB" })).toBeVisible();
    const kobo = sheet.getByRole("link", { name: "Download for Kobo (KEPUB)" });
    await expect(kobo).toHaveAttribute("href", "/api/v1/books/1/ebook?format=kepub");
    await expect(kobo).toHaveAttribute("download", "");

    await page.goto("/shelf?book=3");
    const azw3 = page.getByRole("dialog", { name: "Tide Tables" });
    await expect(azw3.getByRole("link", { name: "Download AZW3" })).toBeVisible();
    await expect(azw3.getByRole("link", { name: "Download for Kobo (KEPUB)" })).toHaveCount(0);
  });
});

const epub = { path: "/books/dune.epub", format: "EPUB", size_bytes: 2_000_000, file_count: 1, kindle: true, kepub: true };
const BOOKS: Record<number, Book> = {
  11: { ...LIBRARY[0], audiobook: undefined, ebook: epub },
  12: { ...LIBRARY[1], ebook: { ...epub, path: "/books/anathem.azw3", format: "AZW3", kindle: false, kepub: false } },
};

async function mockBooks(page: Page) {
  await page.route((u) => /^\/api\/v1\/books\/\d+(\/series|\/history)?$/.test(u.pathname), (route) => {
    const [, id, rest] = new URL(route.request().url()).pathname.match(/^\/api\/v1\/books\/(\d+)(\/series|\/history)?$/) ?? [];
    if (rest === "/series") return json(route, { name: "", entries: [], gaps: 0 });
    if (rest === "/history") return json(route, { events: [] });
    const b = BOOKS[Number(id)];
    return b ? json(route, b) : json(route, { message: "book not found" }, 404);
  });
}

test.describe("the staff book page", () => {
  test.use({ persona: "manager" });

  test("the EPUB edition offers the Kobo download; an AZW3 doesn't", async ({ page }) => {
    await mockBooks(page);
    await page.goto("/books/11");
    await expect(page.getByRole("link", { name: "Download for Kobo (KEPUB)" })).toHaveAttribute("href", "/api/v1/books/11/ebook?format=kepub");
    await page.goto("/books/12");
    await expect(page.getByRole("button", { name: "Send to Kindle" })).toBeVisible(); // the edition panel is up
    await expect(page.getByRole("link", { name: "Download for Kobo (KEPUB)" })).toHaveCount(0);
  });
});

test.describe("Settings → Book server", () => {
  test.use({ persona: "admin" });

  test("Convert EPUB to KEPUB for Kobo is on by default and saves itself", async ({ page }) => {
    const puts: unknown[] = [];
    await page.route((u) => u.pathname === "/api/v1/audioserver" || u.pathname === "/api/v1/settings", async (route) => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      if (path === "/api/v1/audioserver" && req.method() === "GET") {
        return json(route, { enabled: true, running: true, error: "", host_port: "13378", public_url: "", items: 12, items_ready: 12, trace_until: 0, users: [], devices: [] });
      }
      if (path === "/api/v1/settings" && req.method() === "PUT") {
        const body = req.postDataJSON() as object;
        puts.push(body);
        return json(route, { ...settings, ...body });
      }
      return route.fallback();
    });
    await page.goto("/settings/audiobook-server#kobo-kepub");
    const toggle = page.getByRole("switch", { name: "Convert EPUB to KEPUB for Kobo" });
    await expect(toggle).toBeChecked();
    await expect(page.getByText(/the library file is never changed/)).toBeVisible();
    expect(puts).toEqual([]);

    await toggle.click();
    await expect(page.getByText("Saved ✓")).toBeVisible();
    expect(puts).toEqual([{ books_kepub: false }]);
    await expect(toggle).not.toBeChecked();
  });

  test("the settings search finds it", async ({ page }) => {
    await page.route((u) => u.pathname === "/api/v1/audioserver", (route) =>
      json(route, { enabled: true, running: true, error: "", host_port: "13378", public_url: "", items: 12, items_ready: 12, trace_until: 0, users: [], devices: [] }));
    await page.goto("/settings/library");
    await page.getByRole("searchbox", { name: "Search settings" }).fill("kobo");
    await page.getByRole("button", { name: /Convert EPUB to KEPUB for Kobo/ }).click();
    await expect(page).toHaveURL(/\/settings\/audiobook-server#kobo-kepub$/);
    await expect(page.getByRole("switch", { name: "Convert EPUB to KEPUB for Kobo" })).toBeVisible();
  });
});
