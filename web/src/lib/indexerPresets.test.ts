import { describe, expect, it } from "vitest";
import { INDEXER_PRESETS, presetById } from "./indexerPresets";

describe("indexer presets", () => {
  it("carry no credentials: only names, URLs, scopes and copy", () => {
    const allowed = new Set(["id", "label", "monogram", "blurb", "kind", "defaultName", "fields", "mediaTypes", "urlPlaceholder", "urlOptional", "help", "privateTracker"]);
    for (const p of INDEXER_PRESETS) {
      for (const k of Object.keys(p)) expect(allowed.has(k), `${p.id}.${k}`).toBe(true);
      expect(JSON.stringify(p)).not.toMatch(/apikey=|passkey=|token=|api_key=/i);
    }
  });

  it("scope books-only and public trackers as the spec says, and offer no Newznab", () => {
    expect(presetById("myanonamouse")?.mediaTypes).toEqual(["book"]);
    expect(presetById("1337x")?.mediaTypes).toEqual(["movie", "series"]);
    expect(presetById("torrentleech")?.fields).toEqual(["username", "password"]);
    expect(INDEXER_PRESETS.some((p) => (p.kind as string) === "newznab")).toBe(false);
    expect(new Set(INDEXER_PRESETS.map((p) => p.id)).size).toBe(INDEXER_PRESETS.length);
    for (const p of INDEXER_PRESETS) expect(p.monogram).toMatch(/^[A-Z0-9]{2}$/);
  });
});
