import { describe, expect, it } from "vitest";
import type { Episode } from "./api";
import { chipText, chipTitle, langsLabel, seasonAudio, seasonFits } from "./audioLangs";

const ep = (n: number, langs?: string[], fit?: "good" | "partial" | "bad"): Episode => ({
  id: n, season_number: 1, episode_number: n, monitored: true, has_file: !!langs,
  audio_langs: langs?.map((code) => ({ code, name: code ? code.toUpperCase() + "-name" : undefined })),
  audio_lang_fit: fit ? { status: fit, note: "x" } : undefined,
});

describe("audio language badges", () => {
  it("labels a chip by code, ? for an unidentified track, and says a detection in words", () => {
    expect(chipText({ code: "de", name: "German" })).toBe("DE");
    expect(chipText({ code: "" })).toBe("?");
    expect(chipTitle({ code: "de", name: "German" })).toBe("German");
    expect(chipTitle({ code: "ja", name: "Japanese", detected: true })).toMatch(/Japanese — detected/);
    expect(chipTitle({ code: "" })).toMatch(/no language tag/);
  });

  it("names the languages and the fit for screen readers", () => {
    const l = langsLabel([{ code: "en", name: "English" }, { code: "de", name: "German", detected: true }, { code: "" }],
      { status: "partial", note: "No German audio, which this profile prefers" });
    expect(l).toBe("Audio: English, German (detected), an unidentified track. Missing a preferred language: No German audio, which this profile prefers");
  });

  it("summarises a season: in every file first, then by count, over the analysed files only", () => {
    const eps = [ep(1, ["en", "de"]), ep(2, ["en"]), ep(3, ["en", "de"]), ep(4), ep(5, ["en", "fr", "de"])];
    const s = seasonAudio(eps);
    expect(s.of).toBe(4);
    expect(s.langs).toEqual([{ code: "en", have: 4 }, { code: "de", have: 3 }, { code: "fr", have: 1 }]);
    expect(seasonAudio([ep(1), ep(2)])).toEqual({ langs: [], of: 0 });
  });

  it("counts the season's fits", () => {
    expect(seasonFits([ep(1, ["en"], "bad"), ep(2, ["en"], "partial"), ep(3, ["de"], "good"), ep(4, ["de"])]))
      .toEqual({ bad: 1, partial: 1, good: 1 });
  });
});
