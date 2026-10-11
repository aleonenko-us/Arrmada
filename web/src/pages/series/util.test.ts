import { describe, expect, it } from "vitest";
import type { Episode, Season, Series } from "../../lib/api";
import { defaultSeason, fmtAirDay, nextEpisode, seasonCounts, sortSeasons } from "./util";

const ON = "2026-10-09";
const ep = (s: number, e: number, over: Partial<Episode> = {}): Episode => ({
  id: s * 100 + e, season_number: s, episode_number: e, air_date: "2026-01-01", monitored: true, has_file: true, ...over,
});
const season = (n: number, eps: Episode[], monitored = true): Season => ({ id: n, season_number: n, monitored, episodes: eps });

describe("sortSeasons", () => {
  it("puts Season 1 first and Specials last", () => {
    expect(sortSeasons([season(2, []), season(0, []), season(1, [])]).map((s) => s.season_number)).toEqual([1, 2, 0]);
  });
});

describe("defaultSeason", () => {
  it("opens on the first season with a monitored aired episode missing", () => {
    const seasons = [season(1, [ep(1, 1)]), season(2, [ep(2, 1), ep(2, 2, { has_file: false })]), season(3, [ep(3, 1, { has_file: false })])];
    expect(defaultSeason(seasons, ON)).toBe(2);
  });
  it("ignores gaps nobody monitors, and Specials", () => {
    const seasons = [season(0, [ep(0, 1, { has_file: false })]), season(1, [ep(1, 1), ep(1, 2, { has_file: false, monitored: false })])];
    expect(defaultSeason(seasons, ON)).toBe(1);
  });
  it("falls back to the latest season that has aired, not one still to come", () => {
    const seasons = [season(1, [ep(1, 1)]), season(2, [ep(2, 1)]), season(3, [ep(3, 1, { has_file: false, air_date: "2027-01-01" })])];
    expect(defaultSeason(seasons, ON)).toBe(2);
  });
  it("is the first season when nothing has aired", () => {
    expect(defaultSeason([season(1, [ep(1, 1, { has_file: false, air_date: "" })])], ON)).toBe(1);
  });
});

describe("seasonCounts", () => {
  it("counts files and aired wanted episodes, and greys out aired ones nobody monitors", () => {
    const sn = season(1, [ep(1, 1), ep(1, 2, { has_file: false }), ep(1, 3, { has_file: false, monitored: false }), ep(1, 4, { has_file: false, air_date: "2027-01-01" })]);
    expect(seasonCounts(sn, ON)).toMatchObject({ have: 1, counted: 2, notMonitored: 1, missing: 1, airedCount: 3, total: 4 });
  });
});

describe("nextEpisode", () => {
  const show = (next: string | undefined, seasons: Season[]): Series => ({
    id: 1, tmdb_id: 1, title: "S", year: 2020, monitored: true, quality_profile: "hd", seasons, stats: { episodes: 0, have_files: 0, size_bytes: 0, seasons: 1, next_air_date: next },
  });
  it("is the first monitored episode on the server's next air date", () => {
    const s = show("2026-10-20", [season(3, [ep(3, 6, { air_date: "2026-10-20", monitored: false }), ep(3, 5, { air_date: "2026-10-20" }), ep(3, 7, { air_date: "2026-10-20" })])]);
    expect(nextEpisode(s)?.episode_number).toBe(5);
  });
  it("is nothing when the server knows no date", () => {
    expect(nextEpisode(show(undefined, [season(1, [ep(1, 1, { air_date: "2027-01-01" })])]))).toBeUndefined();
  });
});

describe("fmtAirDay", () => {
  it("adds the year only when it isn't this one", () => {
    expect(fmtAirDay("2026-10-20", ON)).not.toMatch(/2026/);
    expect(fmtAirDay("2027-01-02", ON)).toMatch(/2027/);
  });
});
