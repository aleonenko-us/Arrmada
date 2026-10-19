import { describe, expect, it } from "vitest";
import type { ArrCounts, ArrResult } from "../../lib/importApi";
import { arrCountsLine, arrResultLine, notesByOutcome } from "./arrImportText";

const counts = (over: Partial<ArrCounts> = {}): ArrCounts => ({
  titles: 4, existing: 1, new: 3, unmatchable: 0, with_files: 2, files_found: 2, not_found: 0, attachable: 0, wanted: 1,
  ...over,
});

const result = (over: Partial<ArrResult> = {}): ArrResult => ({
  app: "radarr", media: "movie", name: "Radarr", total: 4, imported: 3, files_attached: 2, existing: 1, skipped: 0, failed: 0, paced: 1, notes: [],
  ...over,
});

describe("arrCountsLine", () => {
  it("says what would come over, and that wanted titles are paced", () => {
    expect(arrCountsLine("movie", counts())).toBe(
      "4 films: 1 already in Arrmada, 3 to add (2 with their files). 1 wanted film will be searched for a few at a time, never all at once.",
    );
  });
  it("names unmatchable titles and files for titles already here, in shows' words", () => {
    const line = arrCountsLine("series", counts({ titles: 1, existing: 1, new: 0, with_files: 0, wanted: 0, unmatchable: 2, attachable: 1 }));
    expect(line).toBe("1 show: 1 already in Arrmada, 0 to add, 2 can't be matched. 1 show already here will get the file Arrmada was missing.");
  });
});

describe("arrResultLine", () => {
  it("counts every outcome", () => {
    expect(arrResultLine(result())).toBe("Imported 3 films · 2 files recorded · 1 already here · 0 skipped · 0 failed · 1 wanted, searched for a few at a time");
    expect(arrResultLine(result({ media: "series", imported: 1, files_attached: 1, paced: 0 }))).toBe("Imported 1 show · 1 episode file recorded · 1 already here · 0 skipped · 0 failed");
  });
  it("says which titles came over unmonitored, and where to turn them on (GH-53)", () => {
    expect(arrResultLine(result({ media: "series", name: "Sonarr", imported: 3, files_attached: 0, paced: 0, unmonitored: 2 }))).toBe(
      "Imported 3 shows · 1 already here · 0 skipped · 0 failed · 2 shows added with searching off (as they were in Sonarr) — turn searching on from the Series list",
    );
    expect(arrResultLine(result({ unmonitored: 1 }))).toContain("1 film added unmonitored (as they were in Radarr) — monitor them from the Movies list");
  });
});

describe("notesByOutcome", () => {
  it("groups notes, failures first, and leaves out empty groups", () => {
    const groups = notesByOutcome([
      { title: "A", outcome: "skipped", reason: "file not found at /x" },
      { title: "B", outcome: "failed", reason: "boom" },
      { title: "C", outcome: "skipped", reason: "no TMDB id" },
    ]);
    expect(groups.map((g) => [g.label, g.notes.length])).toEqual([["Failed", 1], ["Skipped", 2]]);
  });
});
