import { describe, expect, it } from "vitest";
import type { Series } from "../../lib/api";
import { cardMeta, nextAiring } from "./library";

const now = new Date(2026, 9, 9, 15, 0); // Fri 9 Oct 2026, mid-afternoon

describe("nextAiring", () => {
  it("says today, tomorrow, a weekday within the week, then a date", () => {
    expect(nextAiring("2026-10-09", now)).toBe("Airs today");
    expect(nextAiring("2026-10-10", now)).toBe("Airs tomorrow");
    expect(nextAiring("2026-10-15", now)).toMatch(/^Next Thu/);
    expect(nextAiring("2026-10-20", now)).toMatch(/^Next Oct 20|^Next 20 Oct/);
  });
  it("says nothing for no date or one already past", () => {
    expect(nextAiring(undefined, now)).toBe("");
    expect(nextAiring("2026-10-01", now)).toBe("");
  });
});

describe("cardMeta", () => {
  const show = (over: Partial<Series>): Series => ({ id: 1, tmdb_id: 1, title: "S", year: 2019, monitored: true, quality_profile: "", stats: { episodes: 10, have_files: 9, size_bytes: 0, seasons: 3 }, ...over });
  it("names the network and the next episode for a show still airing", () => {
    expect(cardMeta(show({ network: "Coastal", status: "Returning Series", stats: { episodes: 10, have_files: 9, size_bytes: 0, seasons: 3, next_air_date: "2026-10-10" } }), now)).toBe("Coastal · Airs tomorrow");
  });
  it("falls back to year and seasons for an ended show or one with nothing coming", () => {
    expect(cardMeta(show({ status: "Ended", stats: { episodes: 10, have_files: 9, size_bytes: 0, seasons: 3, next_air_date: "2026-10-10" } }), now)).toBe("2019 · 3 sns");
    expect(cardMeta(show({ stats: { episodes: 1, have_files: 1, size_bytes: 0, seasons: 1 } }), now)).toBe("2019 · 1 sn");
  });
});
