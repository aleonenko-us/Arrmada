import { describe, expect, it } from "vitest";
import type { QualityProfileInfo } from "../../lib/api";
import { requestProfiles, selectedProfile } from "./profilePick";

const p = (key: string, media: string, isDefault = false): QualityProfileInfo => ({ key, name: key, media_type: media, built_in: false, is_default: isDefault, summary: "" });

describe("requestProfiles", () => {
  it("keeps only the media's own profiles", () => {
    const all = [p("a", "movie"), p("b", "series"), p("c", "movie")];
    expect(requestProfiles(all, "movie").map((x) => x.key)).toEqual(["a", "c"]);
    expect(requestProfiles(all, "series").map((x) => x.key)).toEqual(["b"]);
  });
});

describe("selectedProfile", () => {
  const list = [p("hd720", "movie"), p("hd1080", "movie", true), p("uhd", "movie")];

  it("starts on the default", () => {
    expect(selectedProfile(list, "")?.key).toBe("hd1080");
  });

  it("follows the pick", () => {
    expect(selectedProfile(list, "hd720")?.key).toBe("hd720");
  });

  it("falls back to the default when the pick is gone", () => {
    expect(selectedProfile(list, "custom:99")?.key).toBe("hd1080");
  });

  it("takes the first when nothing is the default", () => {
    expect(selectedProfile([p("x", "movie"), p("y", "movie")], "")?.key).toBe("x");
  });

  it("is undefined with no profiles", () => {
    expect(selectedProfile([], "hd720")).toBeUndefined();
  });
});
