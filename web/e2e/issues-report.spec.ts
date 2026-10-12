import { test, expect } from "./mockApi";
import { cards, partlyOwnedShow } from "./fixtures/discover";

// REQ-22: a requester reports a problem with a title in the library from its sheet, on a
// phone. The report sheet is its own chunk, loaded on the tap.

const inLibrary = cards[6]; // "The Cartographer": a movie in the library with a file

test.describe("a requester on a phone", () => {
  test.use({ persona: "requester" });

  test("reports a problem with a movie", async ({ page, api }) => {
    await page.goto(`/discover/movie/${inLibrary.tmdb_id}`);
    await api.quiet();
    const sheet = page.getByRole("dialog", { name: inLibrary.title });
    await sheet.getByRole("button", { name: "Report a problem" }).click();

    const report = page.getByRole("dialog", { name: `Report a problem with ${inLibrary.title}` });
    await expect(report).toBeVisible();
    const send = report.getByRole("button", { name: "Send report" });
    await expect(send).toBeDisabled(); // nothing picked yet
    await report.getByText("Sound", { exact: true }).click();
    await report.getByRole("textbox").fill("No English audio after the opening");
    await send.click();

    await expect(page.getByText("Thanks — we’ll let you know when it’s fixed.")).toBeVisible();
    await expect(report).toHaveCount(0);
    const calls = api.callsTo("POST", "/api/v1/issues");
    expect(calls).toHaveLength(1);
    expect(calls[0].body).toEqual({ media_type: "movie", tmdb_id: inLibrary.tmdb_id, category: "audio", note: "No English audio after the opening" });

    // The sheet fits the phone: nothing pushes the page sideways.
    const w = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, vw: document.documentElement.clientWidth }));
    expect(w.doc).toBeLessThanOrEqual(w.vw);
  });

  test("picks the episode for a show", async ({ page, api }) => {
    await page.goto(`/discover/series/${partlyOwnedShow.tmdb_id}`);
    await api.quiet();
    await page.getByRole("dialog").getByRole("button", { name: "Report a problem" }).click();
    const report = page.getByRole("dialog", { name: /^Report a problem with / });
    await report.getByRole("combobox", { name: "Episode", exact: true }).selectOption("4");
    await report.getByText("Subtitles", { exact: true }).click();
    await report.getByRole("button", { name: "Send report" }).click();
    await expect(page.getByText("Thanks — we’ll let you know when it’s fixed.")).toBeVisible();
    expect(api.callsTo("POST", "/api/v1/issues")[0].body).toEqual({ media_type: "series", tmdb_id: partlyOwnedShow.tmdb_id, category: "subtitles", season: 1, episode: 4 });
  });
});
