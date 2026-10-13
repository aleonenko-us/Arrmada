import { describe, expect, it } from "vitest";
import { arrmadaPath, convertPath } from "./LibraryView";

describe("Library links", () => {
  it("opens a title's Arrmada page only when Arrmada has it", () => {
    expect(arrmadaPath({ kind: "movie", title: "Dune", thumb_url: "", media: "movie", arrmada_id: 12 })).toBe("/movies/12");
    expect(arrmadaPath({ kind: "show", title: "Severance", thumb_url: "", media: "series", arrmada_id: 3 })).toBe("/series/3");
    expect(arrmadaPath({ kind: "movie", title: "Elsewhere", thumb_url: "" })).toBeNull();
  });

  it("opens Convert's library list on the title, never a conversion", () => {
    expect(convertPath({ kind: "movie", title: "Dune: Part Two", thumb_url: "", media: "movie" })).toBe("/convert?tab=library&media=movies&q=Dune%3A+Part+Two");
    expect(convertPath({ kind: "show", title: "Severance", thumb_url: "", media: "series" })).toBe("/convert?tab=library&media=tv&q=Severance");
  });
});
