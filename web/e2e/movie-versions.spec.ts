import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import type { Movie, MovieFile, MovieVersion, ReleaseList } from "../src/lib/api";

// MOV-14: a movie with two versions shows the full file panel on both, each version
// searches and imports into itself, an extra version's label/profile/edition can be
// edited, and every delete asks first.

const ID = 77;

const file = (name: string, quality: string, size: number): MovieFile => ({
  path: `/media/movies/Blade Runner (1982)/${name}`,
  filename: name,
  size_bytes: size,
  quality,
  codec: "HEVC",
  audio: ["DTS-HD MA 5.1"],
  subtitles: [name.replace(/\.mkv$/, ".en.srt")],
  probed: true,
  missing: false,
});

const theatrical = file("Blade Runner (1982) - 2160p BluRay.mkv", "2160p BluRay", 40e9);
const dcFile = file("Blade Runner (1982) {edition-Director's Cut} - 2160p BluRay.mkv", "2160p BluRay", 42e9);

const versions: MovieVersion[] = [
  { id: 0, is_default: true, label: "Default", quality_profile: "uhd-2160p", monitored: true, has_file: true, file_path: theatrical.path, file: theatrical,
    // Issue #12: each file's audio languages, judged against a profile that requires German.
    audio_langs: [{ code: "en", name: "English" }, { code: "de", name: "German" }, { code: "fr", name: "French" }],
    audio_lang_fit: { status: "good", note: "Has German audio, as this profile asks" } },
  { id: 5, is_default: false, label: "Director's Cut", quality_profile: "uhd-2160p", edition: "Director's Cut", monitored: true, has_file: true, file_path: dcFile.path, file: dcFile,
    audio_langs: [{ code: "en", name: "English" }, { code: "" }],
    audio_lang_fit: { status: "bad", note: "No German audio, which this profile requires" } },
];

const movie: Movie = {
  id: ID, tmdb_id: 78, title: "Blade Runner", year: 1982, monitored: true, quality_profile: "uhd-2160p",
  min_availability: "released", has_file: true, movie_file_path: theatrical.path, file: theatrical, versions,
};

const releases: ReleaseList = {
  profile: "uhd-2160p",
  searched: 1,
  releases: [
    { title: "Blade.Runner.1982.Directors.Cut.German.DL.2160p.UHD.BluRay-GRP", indexer: "Fake", token: "tok-dc", size_gb: 50, seeders: 20, peers: 3, summary: "4K · BluRay · Director's Cut", eligible: true, recommended: true, audio_langs: ["en", "de"] },
    { title: "Blade.Runner.1982.Directors.Cut.2160p.UHD.BluRay-ENG", indexer: "Fake", token: "tok-en", size_gb: 49, seeders: 25, peers: 3, summary: "4K · BluRay · Director's Cut", eligible: false, recommended: false, reject_reason: "Missing German audio — your profile requires it", audio_langs: ["en"], audio_langs_note: "not tagged — read as the original language" },
    { title: "Blade.Runner.1982.2160p.UHD.BluRay-GRP", indexer: "Fake", token: "tok-th", size_gb: 48, seeders: 30, peers: 3, summary: "4K · BluRay", eligible: false, recommended: false, reject_reason: "Not the Director's Cut edition this version wants" },
  ],
};

interface Seen { method: string; path: string; body: unknown }

// serve answers this movie's endpoints (registered after the fixture table, so they win)
// and records what the page sent.
async function serve(page: Page): Promise<Seen[]> {
  const seen: Seen[] = [];
  const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
  await page.route((u) => u.pathname.startsWith(`/api/v1/movies/${ID}`) || ["/api/v1/grab", "/api/v1/movies/editions", "/api/v1/recycle/mode"].includes(u.pathname), (route) => {
    const req = route.request();
    const url = new URL(req.url());
    let body: unknown;
    try { body = req.postDataJSON(); } catch { body = undefined; }
    seen.push({ method: req.method(), path: url.pathname + url.search, body });
    const p = url.pathname.replace(`/api/v1/movies/${ID}`, "");
    if (url.pathname === "/api/v1/grab") return route.fulfill(json({ status: "grabbed", title: "x" }));
    if (url.pathname === "/api/v1/recycle/mode") return route.fulfill(json({ enabled: true, dirs: ["/media/movies/.arrmada-recycle"], retention_days: 30, max_gb: 0 }));
    if (url.pathname === "/api/v1/movies/editions") return route.fulfill(json({ editions: ["Director's Cut", "Extended", "Theatrical", "IMAX", "Unrated", "Remastered", "Final Cut"] }));
    if (p === "" && req.method() === "GET") return route.fulfill(json(movie));
    if (p === "/releases") return route.fulfill(json(releases));
    if (p === "/history") return route.fulfill(json({ events: [] }));
    if (p === "/blocklist") return route.fulfill(json({ blocklist: [] }));
    if (p === "/watch-stats") return route.fulfill(json({ plays: 0, watchers: [] }));
    if (p === "/collection") return route.fulfill(json({ name: "", members: [] }));
    if (p === "/manualimport" && req.method() === "GET") {
      return route.fulfill(json({ dir: "/media/downloads", candidates: [{ path: "/media/downloads/BR.DC.mkv", filename: "BR.DC.mkv", size_bytes: 41e9, quality: "2160p" }], truncated: false }));
    }
    if (p === "/manualimport") return route.fulfill(json({ status: "imported" }));
    if (/^\/versions\/\d+$/.test(p) && req.method() === "PUT") return route.fulfill(json({ status: "updated", queued: true, position: 1 }));
    if (/^\/versions\/\d+\/file$/.test(p)) return route.fulfill({ status: 204 });
    return route.fulfill(json({ message: `e2e movie-versions: no answer for ${req.method()} ${url.pathname}` }, 501));
  });
  return seen;
}

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("both versions get the full file panel, their own search, import and edit, and confirmed deletes", async ({ page, api }) => {
    const seen = await serve(page);
    await page.goto(`/movies/${ID}`);
    await api.quiet();

    const cards = page.getByTestId("version-card");
    await expect(cards).toHaveCount(2);
    const dc = cards.filter({ hasText: "Director's Cut" }).last();
    // The full panel on the extra version too: media chips, subtitles, the file's path.
    await expect(dc.getByText("DTS-HD MA 5.1")).toBeVisible();
    await expect(dc.getByText(dcFile.subtitles![0])).toBeVisible();
    await expect(dc.getByRole("button", { name: dcFile.path })).toBeVisible();
    // The default version's file is the one Subtitles works on: its kept languages' state
    // and Find subtitles (SUB-29) stand in for the bare file names.
    await expect(cards.first().getByRole("button", { name: "Find subtitles" })).toBeVisible();
    await expect(cards.first().getByRole("button", { name: /✓ en · OpenSubtitles/ })).toBeVisible();
    await expect(dc.getByRole("button", { name: "Find subtitles" })).toHaveCount(0);

    // Each file's audio languages beside its media chips, the fit in a mark and words.
    const defLangs = cards.first().getByTestId("audio-langs");
    await expect(defLangs).toHaveAttribute("aria-label", "Audio: English, German, French. Languages fit: Has German audio, as this profile asks");
    await expect(defLangs).toContainText("✓");
    const dcLangs = dc.getByTestId("audio-langs");
    await expect(dcLangs).toHaveAttribute("data-fit", "bad");
    await expect(dcLangs).toHaveAttribute("aria-label", /an unidentified track\. Doesn't fit the language rules: No German audio/);
    await expect(dcLangs).toContainText("?");
    if (process.env.LANG_SHOTS) await cards.first().locator("..").screenshot({ path: `${process.env.LANG_SHOTS}/movie-versions.png` });

    // Search from the DC version ranks for it and grabs into it.
    await dc.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByText("Not the Director's Cut edition this version wants")).toBeVisible();
    // Release rows say what the name carries, and a language Must says what's missing.
    await expect(page.getByText("Missing German audio — your profile requires it")).toBeVisible();
    await expect(page.getByTestId("release-langs").first()).toHaveText("audio EN·DE");
    await expect(page.getByTestId("release-langs").nth(1)).toHaveText("audio EN*");
    if (process.env.LANG_SHOTS) await page.getByRole("dialog").screenshot({ path: `${process.env.LANG_SHOTS}/release-search.png` });
    await page.getByRole("button", { name: "Grab", exact: true }).first().click();
    await expect.poll(() => seen.filter((s) => s.path === "/api/v1/grab").length).toBe(1);
    expect(seen.find((s) => s.path.startsWith(`/api/v1/movies/${ID}/releases`))?.path).toBe(`/api/v1/movies/${ID}/releases?version=5`);
    expect(seen.find((s) => s.path === "/api/v1/grab")?.body).toEqual({ token: "tok-dc", movie_id: ID, version_id: 5 });
    await page.getByRole("dialog").getByRole("button", { name: "Close" }).click();

    // Import from the DC version imports into it.
    await dc.getByRole("button", { name: "Import", exact: true }).click();
    const imp = page.getByRole("dialog", { name: "Manual import" });
    await expect(imp.getByLabel("Import into")).toHaveValue("5");
    await imp.getByRole("button", { name: /BR\.DC\.mkv/ }).click();
    await expect.poll(() => seen.filter((s) => s.method === "POST" && s.path.endsWith("/manualimport")).length).toBe(1);
    expect(seen.find((s) => s.method === "POST" && s.path.endsWith("/manualimport"))?.body).toEqual({ path: "/media/downloads/BR.DC.mkv", version_id: 5 });

    // Edit saves the label, profile and edition.
    await dc.getByRole("button", { name: "Edit", exact: true }).click();
    const edit = page.getByRole("dialog", { name: /Edit/ });
    await edit.getByLabel("Label").fill("Final Cut 4K");
    await edit.getByRole("combobox", { name: "Edition" }).selectOption("Final Cut");
    await edit.getByRole("button", { name: "Save" }).click();
    await expect.poll(() => seen.filter((s) => s.method === "PUT").length).toBe(1);
    expect(seen.find((s) => s.method === "PUT")?.body).toMatchObject({ label: "Final Cut 4K", quality_profile: "uhd-2160p", edition: "Final Cut", monitored: true });

    // Deleting a version's file asks first; cancelling sends nothing.
    await dc.getByRole("button", { name: "Delete file" }).click();
    const confirm = page.getByRole("dialog", { name: /Delete the “Director's Cut” file/ });
    await expect(confirm).toBeVisible();
    await confirm.getByRole("button", { name: "Cancel" }).click();
    expect(seen.filter((s) => s.method === "DELETE")).toEqual([]);
    await dc.getByRole("button", { name: "Remove version" }).click();
    await expect(page.getByRole("dialog", { name: /Remove the “Director's Cut” version/ })).toBeVisible();
    await page.getByRole("button", { name: "Cancel" }).click();
    expect(seen.filter((s) => s.method === "DELETE")).toEqual([]);
  });
});
