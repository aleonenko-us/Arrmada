import { describe, expect, it } from "vitest";
import type { Episode, Series } from "../../lib/api";
import { episodeStatus } from "./EpisodeCard";
import { presetToast } from "./HeroActions";
import { manyOff } from "./library";
import { seriesStatusInput, waitingOnSearch } from "./util";
import { libraryStatus } from "../../lib/status";

const show = (over: Partial<Series> = {}): Series => ({ id: 1, tmdb_id: 1, title: "Show", year: 2020, monitored: true, quality_profile: "", ...over });
const ep = (over: Partial<Episode> = {}): Episode => ({
  id: 1, season_number: 3, episode_number: 1, title: "The Party", air_date: "2020-01-01", monitored: true, has_file: false, ...over,
});

// GH-52: "An already monitored episode says UNMONITORED" — the show's searching was off.
describe("episodeStatus", () => {
  it("says Searching off for a wanted episode of a show whose searching is off", () => {
    const st = episodeStatus(show({ monitored: false }), ep());
    expect(st.label).toBe("Searching off");
    expect(st.tone).toBe("faint");
    expect(st.title).toMatch(/nothing is searched/);
  });
  it("keeps Unmonitored for an episode, or season, nobody wants — whatever the show's switch", () => {
    expect(episodeStatus(show({ monitored: false }), ep({ monitored: false })).label).toBe("Unmonitored");
    expect(episodeStatus(show(), ep({ monitored: false })).label).toBe("Unmonitored");
    expect(episodeStatus(show(), ep(), false).label).toBe("Unmonitored");
    expect(episodeStatus(show(), ep({ monitored: false })).title).toBeUndefined();
  });
  it("is Wanted with searching on, and Downloaded or Unaired whatever the switch", () => {
    expect(episodeStatus(show(), ep()).label).toBe("Wanted");
    expect(episodeStatus(show({ monitored: false }), ep({ has_file: true })).label).toBe("Downloaded");
    expect(episodeStatus(show({ monitored: false }), ep({ air_date: "2999-01-01" })).label).toBe("Unaired");
  });
});

// The hero's and the library card's word for a whole show.
describe("seriesStatusInput", () => {
  const word = (over: Partial<Series>) => libraryStatus(seriesStatusInput(show(over))).label;
  const stats = (o: Partial<NonNullable<Series["stats"]>>) => ({ episodes: 0, have_files: 0, size_bytes: 0, seasons: 1, missing: 0, unmonitored_missing: 0, ...o });
  it("says Nothing monitored for a show searching for nothing, not Wanted", () => {
    expect(word({ stats: stats({ unmonitored_missing: 10 }) })).toBe("Nothing monitored");
    expect(word({ monitored: false, stats: stats({ unmonitored_missing: 10 }) })).toBe("Searching off");
  });
  it("keeps Wanted for a show whose wanted episodes are all still to come", () => {
    expect(word({ stats: stats({ next_air_date: "2999-01-01" }) })).toBe("Wanted");
    expect(word({ stats: stats({ episodes: 4, missing: 4 }) })).toBe("Wanted");
    expect(word({ stats: stats({ episodes: 4, have_files: 2, missing: 2 }) })).toBe("Partial");
    expect(word({ stats: stats({ episodes: 4, have_files: 4, unmonitored_missing: 3 }) })).toBe("Complete");
  });
});

describe("waitingOnSearch", () => {
  it("counts the aired, wanted regular episodes without a file, and whether any is monitored", () => {
    const s = show({
      monitored: false,
      seasons: [
        { id: 0, season_number: 0, monitored: true, episodes: [ep({ season_number: 0 })] },
        { id: 3, season_number: 3, monitored: true, episodes: [ep(), ep({ id: 2, episode_number: 2, has_file: true }), ep({ id: 3, episode_number: 3, air_date: "2999-01-01" })] },
        { id: 4, season_number: 4, monitored: false, episodes: [ep({ id: 4, season_number: 4, monitored: false })] },
      ],
    });
    expect(waitingOnSearch(s, "2026-10-19")).toEqual({ wanted: 1, anyMonitored: true });
    expect(waitingOnSearch(show({ seasons: [{ id: 1, season_number: 1, monitored: false, episodes: [ep({ monitored: false })] }] }))).toEqual({ wanted: 0, anyMonitored: false });
  });
});

// GH-53: a preset turns searching on, and says so.
describe("presetToast", () => {
  it("says searching came on with the preset when it did", () => {
    expect(presetToast("Missing episodes", true)).toBe("Monitoring missing episodes — searching turned on.");
    expect(presetToast("Missing episodes", false)).toBe("Monitoring: Missing episodes.");
  });
});

describe("manyOff", () => {
  const list = (on: number, off: number) => [...Array(on).fill({ monitored: true }), ...Array(off).fill({ monitored: false })];
  it("points at the bulk switch when every show, or most of many, have searching off", () => {
    expect(manyOff(list(0, 2))).toBe(2);
    expect(manyOff(list(5, 5))).toBe(5);
    expect(manyOff(list(6, 4))).toBe(0);
    expect(manyOff(list(10, 1))).toBe(0);
    expect(manyOff([])).toBe(0);
  });
});
