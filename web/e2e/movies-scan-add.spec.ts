import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";

// MOV-20: Scan library while a scan already runs follows that scan's live progress.
// MOV-23: the Add dialog marks a film already in the library (a link, not an add), and a
// pick opens a confirm step whose availability and search choice go with the add.

// override answers the given endpoints before the default fixtures (the last route
// registered is tried first), recording the calls like the defaults do.
async function override(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method() || url.pathname !== r.path) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(r.body ?? {}) });
    }
    return route.fallback();
  });
}

const runningScan = {
  id: 77, kind: "movie.scan", target: "all", trigger: "user:2", status: "running", progress: 0.5,
  message: "Scanning 612 / 1,224 folders", error: "", created_at: null, started_at: null, finished_at: null,
};

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("a second scan follows the running one's progress", async ({ page, api }) => {
    await override(page, api, [
      { method: "POST", path: "/api/v1/movies/scan", status: 409, body: { status: "error", message: "a library scan is already running", existing: true, job_id: 77 } },
      { method: "GET", path: "/api/v1/jobs/77", body: runningScan },
    ]);
    await page.goto("/movies");
    await page.getByRole("button", { name: "Scan library" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Scan library" }).click();
    await expect(page.getByText("A library scan is already running — showing its progress.")).toBeVisible();
    await expect(page.getByText("Scanning 612 / 1,224 folders")).toBeVisible();
    await expect(page.getByRole("button", { name: "Scanning…" })).toBeDisabled();
  });

  test("the add dialog marks films in the library and adds with an availability", async ({ page, api }) => {
    await override(page, api, [
      { method: "GET", path: "/api/v1/movies/lookup", body: { results: [
        { tmdb_id: 501, title: "Harbour Lights", year: 2019, overview: "Already here.", poster_url: "", vote_average: 7, in_library: true, library_id: 1 },
        { tmdb_id: 502, title: "Harbour Lights II", year: 2023, overview: "Not yet.", poster_url: "", vote_average: 6.5, in_library: false },
      ] } },
      { method: "POST", path: "/api/v1/movies", status: 201, body: { id: 99, tmdb_id: 502, title: "Harbour Lights II", year: 2023, monitored: true, has_file: false, quality_profile: "hd", min_availability: "announced" } },
    ]);
    await page.goto("/movies");
    await page.getByRole("button", { name: "+ Add movie" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Movie title").fill("harbour");
    await dialog.getByRole("button", { name: "Search" }).click();
    await expect(dialog.getByRole("link", { name: /Harbour Lights \(2019\).*In library/ })).toHaveAttribute("href", "/movies/1");

    await dialog.getByRole("button", { name: /Harbour Lights II/ }).click();
    expect(api.callsTo("POST", "/api/v1/movies")).toHaveLength(0); // a pick confirms first
    await dialog.getByLabel(/Minimum availability/).selectOption("announced");
    await dialog.getByRole("button", { name: "Add movie" }).click();
    await expect(dialog).toBeHidden();
    const posts = api.callsTo("POST", "/api/v1/movies").filter((c) => c.path === "/api/v1/movies");
    expect(posts).toHaveLength(1);
    expect(posts[0].body).toMatchObject({ tmdb_id: 502, min_availability: "announced", search_on_add: true });
    expect(api.callsTo("PUT", "/api/v1/settings")).toHaveLength(0);
    expect(api.callsTo("PATCH", "/api/v1/settings")).toHaveLength(0);
  });
});
