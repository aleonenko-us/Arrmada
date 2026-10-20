import { describe, expect, it } from "vitest";
import { albumFileMarks, trackFileMark } from "./fileMarks";

// MUS-15: the file check's markers read as words, worst first.
describe("file marks", () => {
  it("says what the album's files are", () => {
    expect(albumFileMarks({})).toBe("");
    expect(albumFileMarks({ missing_tracks: 1 })).toBe("1 track file gone from disk");
    expect(albumFileMarks({ missing_tracks: 2, changed_tracks: 3 })).toBe("2 track files gone from disk · 3 changed outside Arrmada");
  });
  it("marks a track gone, not found once, or changed", () => {
    expect(trackFileMark({ has_file: true })).toBeNull();
    expect(trackFileMark({ has_file: false, missing: true })?.[0]).toBe("gone from disk");
    expect(trackFileMark({ has_file: true, not_found_since: "2026-10-20T08:00:00Z" })?.[0]).toBe("not found");
    expect(trackFileMark({ has_file: true, changed_at: "2026-10-20T08:00:00Z" })?.[0]).toBe("changed");
  });
});
