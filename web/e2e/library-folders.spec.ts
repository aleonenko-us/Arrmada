import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import type { MockRoute } from "./fixtures/routes";
import { libraryFoldersMulti } from "./fixtures/system";

// FOLDERS: more than one folder for Movies and TV. With one folder (the default fixture)
// nothing about folders shows on a title, an add or an approve; with several, the title
// shows its folder and can move, and the add and approve dialogs offer a folder picked by
// the rules.

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

const multi: MockRoute = { method: "GET", path: "/api/v1/library/folders", body: libraryFoldersMulti };
const suggestKids: MockRoute = { method: "GET", path: "/api/v1/library/folders/suggest", body: { folder_id: 3 } };

test.describe("admin", () => {
  test.use({ persona: "admin" });

  test("with one folder a film shows nothing about folders", async ({ page }) => {
    await page.goto("/movies/1");
    await page.getByRole("button", { name: "More actions" }).click();
    await expect(page.getByRole("menuitem", { name: "Refresh & rescan" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Move to folder…" })).toHaveCount(0);
    await expect(page.getByText("Folder:")).toHaveCount(0);
  });

  test("with several folders a film shows its folder and moves, following the job", async ({ page, api }) => {
    await override(page, api, [
      multi,
      { method: "POST", path: "/api/v1/movies/1/move", status: 202, body: { status: "moving", job_id: 901, folder: "Kids" } },
      { method: "GET", path: "/api/v1/jobs/901", body: {
        id: 901, kind: "library.move", target: "movie:1", trigger: "user:1", status: "succeeded", progress: 1,
        message: "The Cartographer moved to Kids", error: "", created_at: null, started_at: null, finished_at: null,
      } },
    ]);
    await page.goto("/movies/1");
    await expect(page.getByText("Folder:")).toBeVisible();
    await page.getByRole("button", { name: "More actions" }).click();
    await page.getByRole("menuitem", { name: "Move to folder…" }).click();
    const dialog = page.getByRole("dialog", { name: "Move to another folder" });
    await expect(dialog.getByRole("combobox")).toHaveValue("3");
    await expect(dialog.getByText("/media/kids")).toBeVisible();
    await dialog.getByRole("button", { name: "Move", exact: true }).click();
    await expect(dialog.getByText("The Cartographer moved to Kids")).toBeVisible();
    const calls = api.callsTo("POST", "/api/v1/movies/1/move");
    expect(calls).toHaveLength(1);
    expect(calls[0].body).toEqual({ folder_id: 3 });
  });

  test("the add dialog preselects the folder the rules pick and adds there", async ({ page, api }) => {
    await override(page, api, [
      multi, suggestKids,
      { method: "GET", path: "/api/v1/movies/lookup", body: { results: [
        { tmdb_id: 862, title: "Toy Story", year: 1995, overview: "Toys.", poster_url: "", vote_average: 8, in_library: false },
      ] } },
      { method: "POST", path: "/api/v1/movies", status: 201, body: { id: 98, tmdb_id: 862, title: "Toy Story", year: 1995, monitored: true, has_file: false, quality_profile: "hd", min_availability: "released", library_folder_id: 3 } },
    ]);
    await page.goto("/movies");
    await page.getByRole("button", { name: "+ Add movie" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Search TMDB for a movie").fill("toy story");
    await dialog.getByRole("button", { name: "Search" }).click();
    await dialog.getByRole("button", { name: /Toy Story/ }).click();
    await expect(dialog.getByLabel("Folder")).toHaveValue("3");
    await dialog.getByRole("button", { name: "Add movie" }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/movies").length).toBe(1);
    expect((api.callsTo("POST", "/api/v1/movies")[0].body as { folder_id?: number }).folder_id).toBe(3);
  });

  test("approving a request can pick the folder", async ({ page, api }) => {
    await override(page, api, [multi, suggestKids]);
    await page.goto("/requests?tab=needs&id=1");
    const sheet = page.getByRole("dialog", { name: "Saltwind" });
    await expect(sheet.getByLabel("Folder")).toHaveValue("3");
    await sheet.getByLabel("Folder").selectOption("0");
    await sheet.getByRole("button", { name: "Approve", exact: true }).click();
    await expect.poll(() => api.callsTo("POST", "/api/v1/requests/1/approve").length).toBe(1);
    expect((api.callsTo("POST", "/api/v1/requests/1/approve")[0].body as { folder_id?: number }).folder_id).toBe(0);
  });

  test("Settings → Library lists the extra folders with their rules", async ({ page, api }) => {
    await override(page, api, [multi]);
    await page.goto("/settings/library#more-folders");
    const card = page.locator("#more-folders");
    await expect(card.getByLabel("Name of the Kids folder")).toHaveValue("Kids");
    await expect(card.getByLabel("Name of the Anime folder")).toHaveValue("Anime");
    await expect(card.getByText("— the main folder")).toHaveCount(2);
    await expect(card.getByText("/media/kids")).toBeVisible();
  });
});

test.describe("requester", () => {
  test.use({ persona: "requester" });

  test("never sees or loads anything about folders", async ({ page, api }) => {
    await page.goto("/discover");
    await api.quiet();
    expect(api.callsTo("GET", "/api/v1/library/folders")).toHaveLength(0);
  });
});
