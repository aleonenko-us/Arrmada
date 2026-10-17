import { describe, expect, it } from "vitest";
import { shownTitle } from "./displayTitle";
import { seriesLibraryConfig } from "../pages/series/library";
import type { Series } from "./api";

describe("shownTitle", () => {
  it("shows the display title when the API sends one, else the title", () => {
    expect(shownTitle({ title: "The Travelling Players", display_title: "Ο Θίασος" })).toBe("Ο Θίασος");
    expect(shownTitle({ title: "Dune" })).toBe("Dune");
    expect(shownTitle({ title: "Dune", display_title: "" })).toBe("Dune");
  });
});

describe("series library", () => {
  const show = (id: number, title: string, display_title?: string) =>
    ({ id, tmdb_id: id, title, display_title, year: 2000, monitored: true, quality_profile: "hd" }) as unknown as Series;
  const config = seriesLibraryConfig({ fits: new Map(), actions: () => [], rowAction: () => null });

  it("sorts by the shown title and finds a show by either name", () => {
    const list = [show(1, "Gomorrah", "Gomorra"), show(2, "Fargo"), show(3, "Babylon Berlin")];
    expect([...list].sort(config.compare).map((s) => s.id)).toEqual([3, 2, 1]);
    const text = config.searchText(list[0]).toLowerCase();
    expect(text).toContain("gomorra");
    expect(text).toContain("gomorrah");
  });
});
