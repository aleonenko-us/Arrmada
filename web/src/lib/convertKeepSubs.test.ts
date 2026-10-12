import { describe, expect, it } from "vitest";
import { keepSubsLine } from "./convertKeepSubs";

// SUB-32: the Subtitles page says what Convert keeps, in words.
const names = (c: string) => ({ en: "English", fr: "French", ja: "Japanese" })[c] ?? c.toUpperCase();

describe("keepSubsLine", () => {
  it("names every case", () => {
    expect(keepSubsLine("", names)).toBe("every language");
    expect(keepSubsLine("en, fr", names)).toBe("English, French");
    expect(keepSubsLine("@subtitles", names)).toBe("the same languages as Subtitles");
    expect(keepSubsLine("@subtitles, ja", names)).toBe("the same languages as Subtitles, plus Japanese");
  });
});
