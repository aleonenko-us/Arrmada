import { describe, expect, it } from "vitest";
import { groupVersions, nestedVersions } from "./convertVersions";

const row = (key: string, movie_id?: number, version_id?: number) => ({ key, movie_id, version_id });

describe("groupVersions", () => {
  it("puts each version right under its film's default file", () => {
    // Sorted by saving: the 4K remux of film 1 saves the most.
    const list = [row("movie:1:v:7", 1, 7), row("movie:2", 2), row("movie:1", 1), row("movie:2:v:9", 2, 9)];
    expect(groupVersions(list).map((r) => r.key)).toEqual(["movie:2", "movie:2:v:9", "movie:1", "movie:1:v:7"]);
    expect([...nestedVersions(list)].map((r) => r.key).sort()).toEqual(["movie:1:v:7", "movie:2:v:9"]);
  });

  it("keeps a version in its own place when its film's default file isn't listed", () => {
    const list = [row("movie:3:v:4", 3, 4), row("movie:5", 5)];
    expect(groupVersions(list).map((r) => r.key)).toEqual(["movie:3:v:4", "movie:5"]);
    expect(nestedVersions(list).size).toBe(0);
  });

  it("leaves episodes alone", () => {
    const list = [row("episode:1:1:2"), row("episode:1:1:1")];
    expect(groupVersions(list).map((r) => r.key)).toEqual(["episode:1:1:2", "episode:1:1:1"]);
  });
});
