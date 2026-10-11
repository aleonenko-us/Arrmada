import { describe, expect, it } from "vitest";
import { renderNaming, seriesIdFormat } from "./naming";

// The same cases as the Go TestRenderNameDropsEmptyPlexGroups / TestMovieTargetTMDBToken,
// so the Settings preview says what the importer will do.
describe("renderNaming", () => {
  const empty = { title: "Heat", year: "1995", quality: "1080p", edition: "", tmdbid: "", imdbid: "", tvdbid: "" };
  it("drops empty Plex groups", () => {
    expect(renderNaming("{title} ({year}) {edition-{edition}} - {quality}", empty)).toBe("Heat (1995) - 1080p");
    expect(renderNaming("{title} ({year}) {tmdb-{tmdbid}}", empty)).toBe("Heat (1995)");
    expect(renderNaming("{title} {imdb-{imdbid}} {tvdb-{tvdbid}}", empty)).toBe("Heat");
    expect(renderNaming("{title} {edition-} - {quality}", empty)).toBe("Heat - 1080p");
  });
  it("fills the Plex recommended preset", () => {
    const s = { title: "Heat", year: "1995", quality: "1080p BluRay", edition: "Director's Cut", tmdbid: "949" };
    expect(renderNaming("{title} ({year}) {tmdb-{tmdbid}}", s)).toBe("Heat (1995) {tmdb-949}");
    expect(renderNaming("{title} ({year}) {edition-{edition}} - {quality}", s)).toBe("Heat (1995) {edition-Director's Cut} - 1080p BluRay");
  });
});

describe("seriesIdFormat", () => {
  it("falls back to the TMDB id when there's no TVDB id", () => {
    const f = "{title} ({year}) {tvdb-{tvdbid}}";
    expect(renderNaming(seriesIdFormat(f, { tmdbid: "777", tvdbid: "" }), { title: "Obscure", year: "2022", tmdbid: "777", tvdbid: "" })).toBe("Obscure (2022) {tmdb-777}");
    expect(renderNaming(seriesIdFormat(f, { tmdbid: "1", tvdbid: "403245" }), { title: "The Bear", year: "2022", tmdbid: "1", tvdbid: "403245" })).toBe("The Bear (2022) {tvdb-403245}");
  });
});
