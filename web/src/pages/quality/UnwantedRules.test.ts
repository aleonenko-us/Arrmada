import { describe, expect, it } from "vitest";
import type { UnwantedItemInfo } from "../../lib/api";
import { effectiveDefault, unwantedSummary } from "./UnwantedRules";

const catalog: UnwantedItemInfo[] = [
  { key: "full_disc", label: "Full disc image (BR-DISK)", description: "", default: "reject" },
  { key: "three_d", label: "3D", description: "", default: "reject" },
  { key: "low_quality_group", label: "Low-quality group", description: "", default: "avoid" },
  { key: "dv_no_fallback", label: "Dolby Vision without HDR fallback", description: "", default: "avoid" },
];
const groups = ["YIFY", "YTS", "MeGusta"];

describe("Unwanted rules", () => {
  it("allows Dolby Vision without a fallback by default when the target prefers Dolby Vision", () => {
    expect(effectiveDefault(catalog[3], {})).toBe("avoid");
    expect(effectiveDefault(catalog[3], { hdr: { DV: "want" } })).toBe("allow");
    expect(effectiveDefault(catalog[3], { hdr: { DV: "must" } })).toBe("allow");
    expect(effectiveDefault(catalog[1], { hdr: { DV: "want" } })).toBe("reject");
  });

  it("summarises only the choices that differ from the defaults", () => {
    expect(unwantedSummary(undefined, catalog, {}, groups)).toBe("");
    expect(unwantedSummary({ items: { three_d: "reject" }, low_quality_groups: null }, catalog, {}, groups)).toBe("");
    expect(unwantedSummary({ items: { three_d: "allow", full_disc: "avoid" }, low_quality_groups: null }, catalog, {}, groups))
      .toBe("full disc image avoided, 3D allowed");
    expect(unwantedSummary({ items: {}, low_quality_groups: ["yts", "megusta", "yify"] }, catalog, {}, groups)).toBe("");
    expect(unwantedSummary({ items: {}, low_quality_groups: ["BadGrp"] }, catalog, {}, groups)).toBe("edited low-quality groups");
  });
});
