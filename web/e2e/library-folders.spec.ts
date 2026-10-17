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

  // Issue #24: the Audiobooks folder in use doesn't exist yet. Create it sends that same
  // folder with create, and the row looks again and shows it's there.
  test("Create it makes the missing folder in use and shows it's there", async ({ page, api }) => {
    let made = false;
    const missing = (path: string) => ({
      path, exists: false, is_dir: false, writable: false, hardlink_with_downloads: null, under_data_dir: false,
      free_bytes: 0, total_bytes: 0, entries: 0, entries_capped: false,
      error: `Audiobooks: ${path} doesn't exist. Check the spelling, or create it.`,
    });
    await page.route((u) => u.pathname === "/api/v1/system/library/check", async (route) => {
      const url = new URL(route.request().url());
      const path = url.searchParams.get("path") ?? "";
      if (path !== "/media/audiobooks" || made) return route.fallback();
      return route.fulfill({ contentType: "application/json", body: JSON.stringify(missing(path)) });
    });
    await override(page, api, [{ method: "PUT", path: "/api/v1/system/library", body: { movies: "/media/movies", tv: "/media/tv", ebooks: "/media/ebooks", audiobooks: "/media/audiobooks", music: "/media/music", downloads: "/media/downloads" } }]);
    await page.route((u) => u.pathname === "/api/v1/system/library", async (route) => {
      if (route.request().method() === "PUT") made = true;
      return route.fallback();
    });
    await page.goto("/settings/library");
    await expect(page.getByText("/media/audiobooks doesn't exist")).toBeVisible();
    await page.getByRole("button", { name: "Create it" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/system/library").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/system/library")[0].body).toEqual({ audiobooks: "/media/audiobooks", create: true });
    await expect(page.getByText("/media/audiobooks doesn't exist")).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Create it" })).toHaveCount(0);
  });

  test("Create it says so when the folder couldn't be made", async ({ page, api }) => {
    await page.route((u) => u.pathname === "/api/v1/system/library/check", async (route) => {
      const path = new URL(route.request().url()).searchParams.get("path") ?? "";
      if (path !== "/media/audiobooks") return route.fallback();
      return route.fulfill({ contentType: "application/json", body: JSON.stringify({
        path, exists: false, is_dir: false, writable: false, hardlink_with_downloads: null, under_data_dir: false,
        free_bytes: 0, total_bytes: 0, entries: 0, entries_capped: false,
        error: `Audiobooks: ${path} doesn't exist. Check the spelling, or create it.`,
      }) });
    });
    await override(page, api, [{ method: "PUT", path: "/api/v1/system/library", status: 400, body: {
      status: "error", folder: "audiobooks", message: "Audiobooks: couldn't create /media/audiobooks (permission denied). Check that the folder above it is mounted and that Arrmada's user (PUID/PGID) can write there.",
    } }]);
    await page.goto("/settings/library");
    await page.getByRole("button", { name: "Create it" }).click();
    await expect(page.getByText("couldn't create /media/audiobooks (permission denied)")).toBeVisible();
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

  test("with one folder per library the downloads option waits behind one line until asked for", async ({ page }) => {
    await page.goto("/settings/library#more-folders");
    const card = page.locator("#more-folders");
    await expect(card.getByText("— the main folder")).toHaveCount(2);
    await expect(card.getByRole("button", { name: "Download to a folder on this disk…" })).toHaveCount(0);
    await card.getByRole("button", { name: /Libraries on different disks\?/ }).click();
    await expect(card.getByRole("button", { name: "Download to a folder on this disk…" })).toHaveCount(2);
  });

  test("a folder on another disk can download onto that disk (GitHub #11)", async ({ page, api }) => {
    await override(page, api, [
      multi,
      { method: "GET", path: "/api/v1/system/browse", body: { path: "/mnt/disk2/downloads", parent: "/mnt/disk2", dirs: [], path_disabled: false } },
      { method: "PUT", path: "/api/v1/system/library/folders/downloads", body: {
        folder: { ...libraryFoldersMulti.movie.folders[1], downloads: "/mnt/disk2/downloads" },
        warning: "/mnt/disk2/downloads isn't on the same filesystem as its library folder /media/kids, so imports from it are copied, not hardlinked — pick a downloads folder on the library's disk",
      } },
    ]);
    await page.goto("/settings/library#more-folders");
    const card = page.locator("#more-folders");
    // Every folder offers it, the main ones included; nothing is set yet.
    await expect(card.getByRole("button", { name: "Download to a folder on this disk…" })).toHaveCount(4);
    await expect(card.getByText("Downloads to")).toHaveCount(0);
    // The Kids row is the second Movies row (after the main folder).
    await card.getByRole("button", { name: "Download to a folder on this disk…" }).nth(1).click();
    await page.getByRole("button", { name: "Select this folder" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/system/library/folders/downloads").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/system/library/folders/downloads")[0].body).toEqual({ media: "movie", id: 3, path: "/mnt/disk2/downloads" });
    // Saved, but on another filesystem: the warning says so.
    await expect(page.getByText(/copied, not hardlinked/)).toBeVisible();
  });

  test("a folder's own downloads folder shows, warns, and can go back to Downloads", async ({ page, api }) => {
    const withOwn = {
      movie: { ...libraryFoldersMulti.movie, folders: [
        { ...libraryFoldersMulti.movie.folders[0], downloads: "/mnt/disk1/downloads" },
        { ...libraryFoldersMulti.movie.folders[1], downloads: "/mnt/disk1/downloads", downloads_warning: "/mnt/disk1/downloads isn't on the same filesystem as its library folder /media/kids, so imports from it are copied, not hardlinked" },
      ] },
      series: libraryFoldersMulti.series,
    };
    await override(page, api, [
      { method: "GET", path: "/api/v1/library/folders", body: withOwn },
      { method: "PUT", path: "/api/v1/system/library/folders/downloads", body: { folder: libraryFoldersMulti.movie.folders[1] } },
    ]);
    await page.goto("/settings/library#more-folders");
    const card = page.locator("#more-folders");
    await expect(card.getByText("/mnt/disk1/downloads", { exact: true })).toHaveCount(2);
    await expect(card.getByText(/copied, not hardlinked/)).toHaveCount(1);
    await card.getByRole("button", { name: "Use the Downloads folder" }).nth(1).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/system/library/folders/downloads").length).toBe(1);
    expect(api.callsTo("PUT", "/api/v1/system/library/folders/downloads")[0].body).toEqual({ media: "movie", id: 3, path: "" });
  });
});

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("sees a folder's downloads folder but can't change it, and nothing when there's none", async ({ page, api }) => {
    const withOwn = {
      movie: { ...libraryFoldersMulti.movie, folders: [libraryFoldersMulti.movie.folders[0], { ...libraryFoldersMulti.movie.folders[1], downloads: "/mnt/disk2/downloads" }] },
      series: libraryFoldersMulti.series,
    };
    await override(page, api, [{ method: "GET", path: "/api/v1/library/folders", body: withOwn }]);
    await page.goto("/settings/library#more-folders");
    const card = page.locator("#more-folders");
    await expect(card.getByText("/mnt/disk2/downloads")).toBeVisible();
    await expect(card.getByRole("button", { name: "Download to a folder on this disk…" })).toHaveCount(0);
    await expect(card.getByRole("button", { name: "Use the Downloads folder" })).toHaveCount(0);
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
