import { describe, expect, it } from "vitest";
import { ORIGINAL, languageKeys, setLanguage } from "./AudioLanguageRow";
import { languageRulesDecide } from "./UnwantedRules";

const names: Record<string, string> = { de: "German", fr: "French", ja: "Japanese" };
const name = (k: string) => names[k] ?? k;

describe("Audio language row", () => {
  it("lists Original language first, then by name, leaving out cleared entries", () => {
    expect(languageKeys({ ja: "want", de: "must", [ORIGINAL]: "want", fr: "" }, name)).toEqual([ORIGINAL, "de", "ja"]);
    expect(languageKeys(undefined, name)).toEqual([]);
  });

  it("sets a language, and — takes it off", () => {
    const row = setLanguage(undefined, "de", "must");
    expect(row).toEqual({ de: "must" });
    expect(setLanguage(row, ORIGINAL, "want")).toEqual({ de: "must", original: "want" });
    expect(setLanguage(row, "de", "")).toEqual({});
  });

  it("knows when the foreign-only item defers to the language rules", () => {
    expect(languageRulesDecide(undefined)).toBe(false);
    expect(languageRulesDecide({ language: { fr: "avoid" } })).toBe(false);
    expect(languageRulesDecide({ language: { de: "want" } })).toBe(true);
    expect(languageRulesDecide({ language: { original: "must" } })).toBe(true);
  });
});
