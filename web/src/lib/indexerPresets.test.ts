import { describe, expect, it } from "vitest";
import { INDEXER_PRESETS, presetById, presetsFor } from "./indexerPresets";

describe("indexer presets", () => {
  it("carry no credentials: only names, URLs, scopes and copy", () => {
    const allowed = new Set(["id", "label", "monogram", "blurb", "kind", "defaultName", "fields", "mediaTypes", "urlPlaceholder", "urlOptional", "help", "privateTracker", "usenet", "icon", "defaultUrl"]);
    for (const p of INDEXER_PRESETS) {
      for (const k of Object.keys(p)) expect(allowed.has(k), `${p.id}.${k}`).toBe(true);
      expect(JSON.stringify(p)).not.toMatch(/apikey=|passkey=|token=|api_key=/i);
    }
  });

  it("scope books-only and public trackers as the spec says", () => {
    expect(presetById("myanonamouse")?.mediaTypes).toEqual(["book"]);
    expect(presetById("1337x")?.mediaTypes).toEqual(["movie", "series"]);
    expect(presetById("torrentleech")?.fields).toEqual(["username", "password"]);
    expect(new Set(INDEXER_PRESETS.map((p) => p.id)).size).toBe(INDEXER_PRESETS.length);
    for (const p of INDEXER_PRESETS) expect(p.monogram).toMatch(/^[A-Z0-9]{2}$/);
  });

  // Nothing could download what a usenet indexer finds until SABnzbd is set up.
  it("offer Newznab only once a usenet download client exists", () => {
    expect(presetsFor(false).some((p) => p.kind === "newznab")).toBe(false);
    const nz = presetsFor(true).find((p) => p.kind === "newznab");
    expect(nz).toMatchObject({ usenet: true, fields: ["url", "api_key"] });
    expect(presetsFor(true)).toHaveLength(INDEXER_PRESETS.length);
  });

  it("fill in the API address of the known usenet indexers", () => {
    expect(presetById("nzbgeek")).toMatchObject({ kind: "newznab", usenet: true, defaultUrl: "https://api.nzbgeek.info/api" });
    for (const p of INDEXER_PRESETS.filter((x) => x.usenet && x.id !== "newznab")) expect(p.defaultUrl).toMatch(/^https:\/\/.+\/api$/);
  });
});
