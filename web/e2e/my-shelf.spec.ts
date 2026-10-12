import { test, expect } from "./mockApi";

// My shelf at 375px (APP-19, BOOK-24): New this week, the requests with their real
// stages, series in reading order; a tapped book opens its sheet (the blurb, Download,
// Send to Kindle, the rest of its series) that Back closes; the first Send to Kindle
// explains Amazon's approved-senders list and asks for the address before sending.

test.describe("My shelf at 375px", () => {
  test.use({ persona: "requester" });

  test("rows, the book sheet, and Back", async ({ page, api }) => {
    await page.goto("/shelf");
    const fresh = page.getByRole("region", { name: "New this week" });
    await expect(fresh.getByRole("button")).toHaveCount(1);
    await expect(fresh).toContainText("A Field Guide to Tides");

    const series = page.getByRole("region", { name: "Coastlines" });
    await expect(series.getByRole("button")).toHaveText([/A Field Guide to Tides/, /Tide Tables/]);

    const reqs = page.getByRole("region", { name: "Your requests" });
    await expect(reqs).toContainText("Not found yet");
    await expect(reqs).toContainText("40%");
    await expect(reqs.getByRole("link", { name: /Charts of the Lesser Sounds/ })).toHaveAttribute("href", "/requests?id=42");

    await fresh.getByRole("button", { name: /A Field Guide to Tides/ }).tap();
    await expect(page).toHaveURL(/\/shelf\?book=1$/);
    const sheet = page.getByRole("dialog", { name: "A Field Guide to Tides" });
    await expect(sheet).toContainText("A walk along the shore");
    await expect(sheet).toContainText("Coastlines #1");
    await expect(sheet.getByRole("link", { name: "Download EPUB" })).toBeVisible();
    await expect(sheet.getByRole("button", { name: "Send to Kindle" })).toBeEnabled();
    await expect(sheet.getByRole("region", { name: "More in Coastlines" })).toContainText("Tide Tables");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    // Into the next book of the series, then Back closes the sheet altogether.
    await sheet.getByRole("button", { name: /Tide Tables/ }).tap();
    const next = page.getByRole("dialog", { name: "Tide Tables" });
    await expect(next.getByRole("button", { name: "Send to Kindle" })).toBeDisabled();
    await expect(next).toContainText("AZW3 — Kindle email takes EPUB or PDF");
    await page.goBack();
    await expect(next).toBeHidden();
    await expect(page).toHaveURL(/\/shelf$/);
    expect(api.callsTo("POST", "/api/v1/me/books").length).toBe(0); // nothing sent by looking
  });

  test("the first Send to Kindle asks for the address, then sends", async ({ page }) => {
    const sent: string[] = [];
    let saved = "";
    await page.route((u) => u.pathname === "/api/v1/me/kindle" || u.pathname.endsWith("/send-to-kindle"), async (route) => {
      const req = route.request();
      if (req.method() === "PUT") {
        saved = (req.postDataJSON() as { kindle_email: string }).kindle_email;
        return route.fulfill({ json: { kindle_email: saved, from_address: "books@example.com", smtp_configured: true } });
      }
      if (req.method() === "POST") {
        sent.push(new URL(req.url()).pathname);
        return route.fulfill({ status: 202, json: { status: "sending", existing: false } });
      }
      return route.fallback();
    });
    await page.goto("/shelf?book=1");
    const sheet = page.getByRole("dialog", { name: "A Field Guide to Tides" });
    await sheet.getByRole("button", { name: "Send to Kindle" }).tap();

    const setup = page.getByRole("dialog", { name: "Send to Kindle" });
    await expect(setup).toContainText("Approved Personal Document E-mail List");
    await expect(setup).toContainText("books@example.com");
    expect(sent).toEqual([]);
    await setup.getByPlaceholder("name_42@kindle.com").fill("reader_42@kindle.com");
    await setup.getByRole("button", { name: /Save and send/ }).tap();
    await expect.poll(() => sent).toEqual(["/api/v1/me/books/1/send-to-kindle"]);
    expect(saved).toBe("reader_42@kindle.com");
    await expect(page.getByText(/Sending “A Field Guide to Tides”/)).toBeVisible();
  });
});
