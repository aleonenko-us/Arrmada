import { describe, expect, it } from "vitest";
import type { DiscoverCard } from "../../lib/api";
import { activeFilters, FILTER_KEYS, hideRequested } from "./browseFilters";
import { visibleCards, type RowCtx } from "./shared";

const names = { genre: (id: number) => ({ 28: "Action", 35: "Comedy" })[id], provider: (id: number) => (id === 8 ? "Netflix" : undefined) };

describe("activeFilters", () => {
  it("names every filter in use, and each chip clears only its own keys", () => {
    const p = new URLSearchParams("media=movie&genre=28,35&rating=7&votes=1000&runtime_max=89&lang=ja&year_from=2010&year_to=2020&cert=PG-13&provider=8&sort=vote_average.desc&hide_requested=1");
    const chips = activeFilters(p, names);
    expect(chips.map((c) => c.label)).toEqual([
      "Highest rated", "Action", "Comedy", "★ 7+", "1,000+ votes", "Under 90 min", "Japanese", "2010–2020", "Rated up to PG-13", "Netflix", "Not requested yet",
    ]);
    expect(chips.find((c) => c.label === "Action")?.clear).toEqual({ genre: "35" });
    expect(chips.find((c) => c.label === "Comedy")?.clear).toEqual({ genre: "28" });
    expect(chips.find((c) => c.id === "runtime")?.clear).toEqual({ runtime_min: null, runtime_max: null });
    expect(chips.find((c) => c.id === "years")?.clear).toEqual({ year_from: null, year_to: null });
  });

  it("shows nothing for a plain list, and a lone year bound reads naturally", () => {
    expect(activeFilters(new URLSearchParams("list=popular&media=movie"), names)).toEqual([]);
    expect(activeFilters(new URLSearchParams("year_to=1999"), names)[0].label).toBe("Up to 1999");
    expect(activeFilters(new URLSearchParams("year_from=2001&year_to=2001"), names)[0].label).toBe("2001");
  });

  it("sends the server the new filters but not the page's own", () => {
    expect(FILTER_KEYS).toContain("votes");
    expect(FILTER_KEYS).toContain("cert");
    expect(FILTER_KEYS).not.toContain("hide_requested");
  });
});

const card = (id: number, extra: Partial<DiscoverCard> = {}): DiscoverCard => ({ media_type: "movie", tmdb_id: id, title: `T${id}`, year: 2020, vote_average: 7, in_library: false, has_file: false, ...extra });

describe("hideRequested", () => {
  it("drops pending and approved titles, this visit's asks included, and keeps declined ones", () => {
    const cards = [card(1), card(2, { request_status: "pending" }), card(3, { request_status: "approved" }), card(4, { request_status: "declined" }), card(5)];
    const asked = (c: DiscoverCard) => (c.tmdb_id === 5 ? "pending" as const : undefined);
    expect(hideRequested(cards, asked).map((c) => c.tmdb_id)).toEqual([1, 4]);
  });
});

describe("visibleCards", () => {
  const ctx = (over: Partial<RowCtx>): RowCtx => ({
    doRequest: async () => ({ subscribed: false, status: "pending" }), isRequested: () => undefined, canRequest: true, hideInLibrary: false, flash: () => {}, ...over,
  });
  it("leaves out what was hidden this visit and, when asked, what's in the library", () => {
    const cards = [card(1), card(2), card(3, { in_library: true, has_file: true })];
    expect(visibleCards(cards, ctx({ hidden: new Set(["movie:2"]) })).map((c) => c.tmdb_id)).toEqual([1, 3]);
    expect(visibleCards(cards, ctx({ hidden: new Set(["movie:2"]), hideInLibrary: true })).map((c) => c.tmdb_id)).toEqual([1]);
  });
});
