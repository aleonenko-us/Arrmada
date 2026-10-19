import { describe, expect, it } from "vitest";
import type { FitItem, MovieSummary } from "../../lib/api";
import { hdrBadge, isMisfit, movieLibraryConfig, resBadge } from "./library";

const movie = (id: number, over: Partial<MovieSummary> = {}): MovieSummary => ({
  id, title: `M${id}`, sort_title: `m${id}`, year: 2020, monitored: true, has_file: false, quality_profile: "", min_availability: "released", ...over,
});

const fits = new Map<number, FitItem>([
  [3, { movie_id: 3, season: 0, facts: {} as FitItem["facts"], fit: { status: "over" } }],
  [4, { movie_id: 4, season: 0, facts: {} as FitItem["facts"], fit: { status: "fits" } }],
]);
const config = movieLibraryConfig({ fits, queueKnown: true, actions: () => [], rowAction: () => null });
const filter = (key: string) => config.filters.find((f) => f.key === key)!;

describe("movie library config", () => {
  const lib = [
    movie(1), // wanted
    movie(2, { download: { state: "downloading", progress: 0.4, kind: "missing" } }), // downloading, not wanted
    movie(3, { has_file: true, media: { resolution: "2160p", hdr: ["DV", "HDR10"] } }),
    movie(4, { has_file: true, file_missing: true }),
    movie(5, { monitored: false }), // no file, unmonitored
    movie(6, { monitored: false, has_file: true, download: { state: "downloading", progress: 0.1, kind: "upgrade" } }),
  ];
  const ids = (key: string) => lib.filter(filter(key).match!).map((m) => m.id);

  it("filters by the badge words, and Downloading takes any download", () => {
    expect(config.filters.map((f) => f.label)).toEqual(["All", "Wanted", "Downloading", "No file (unmonitored)", "File missing", "Doesn't fit", "Unmonitored"]);
    expect(ids("wanted")).toEqual([1]);
    expect(ids("downloading")).toEqual([2, 6]);
    expect(ids("nofile")).toEqual([5]);
    expect(ids("filemissing")).toEqual([4]);
    expect(ids("misfit")).toEqual([3]);
    expect(ids("unmonitored")).toEqual([5, 6]);
  });

  it("sorts titles by their sort title, so The Matrix files under M", () => {
    const sorted = [movie(1, { title: "The Matrix", sort_title: "matrix" }), movie(2, { title: "Notting Hill", sort_title: "notting hill" }), movie(3, { title: "Alien", sort_title: "alien" })].sort(config.compare);
    expect(sorted.map((m) => m.title)).toEqual(["Alien", "The Matrix", "Notting Hill"]);
  });

  it("searches the title, the sort title and the year", () => {
    const text = config.searchText(movie(1, { title: "Dune", sort_title: "dune", year: 2021 })).toLowerCase();
    expect(["dune", "2021"].every((w) => text.includes(w))).toBe(true);
  });

  it("offers Title, Added, Year, Rating and Size", () => {
    expect(config.sorts.map((s) => s.label)).toEqual(["Title", "Added", "Year", "Rating", "Size"]);
  });

  // GitHub #36: every Sort menu order is a table column too, so the menu and the headers
  // are one sort.
  it("has a sortable column for every Sort menu order but Title", () => {
    const sortable = new Set(config.columns.filter((c) => c.sort).map((c) => c.key));
    for (const s of config.sorts.filter((s) => s.key !== "title")) expect(sortable.has(s.key)).toBe(true);
    expect(config.columns[0].key).toBe("year");
  });
});

describe("quality badges", () => {
  it("names the resolution and DV before HDR", () => {
    expect(resBadge({ resolution: "2160p" })).toBe("4K");
    expect(resBadge({ quality: "1080p WEB-DL" })).toBe("1080p");
    expect(resBadge({ resolution: "480p" })).toBe("SD");
    expect(resBadge(undefined)).toBe("");
    expect(hdrBadge({ hdr: ["DV", "HDR10"] })).toBe("DV");
    expect(hdrBadge({ hdr: ["HDR10"] })).toBe("HDR");
    expect(hdrBadge({})).toBe("");
  });
  it("counts only judged files that don't fit as misfits", () => {
    expect(isMisfit(fits.get(3))).toBe(true);
    expect(isMisfit(fits.get(4))).toBe(false);
    expect(isMisfit(undefined)).toBe(false);
  });
});
