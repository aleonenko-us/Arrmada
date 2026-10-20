import { describe, expect, it } from "vitest";
import { ORIGINAL, hasMust, languageKeys, setLanguage } from "./AudioLanguageRow";
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

  it("offers the MULTi switch only beside a Must, the one thing it changes", () => {
    expect(hasMust(undefined)).toBe(false);
    expect(hasMust({ it: "want", [ORIGINAL]: "avoid" })).toBe(false);
    expect(hasMust({ it: "must", [ORIGINAL]: "avoid" })).toBe(true);
    expect(hasMust({ [ORIGINAL]: "must" })).toBe(true);
  });

  it("knows when the foreign-only item defers to the language rules", () => {
    expect(languageRulesDecide(undefined)).toBe(false);
    expect(languageRulesDecide({ language: { fr: "avoid" } })).toBe(false);
    expect(languageRulesDecide({ language: { de: "want" } })).toBe(true);
    expect(languageRulesDecide({ language: { original: "must" } })).toBe(true);
  });
});
