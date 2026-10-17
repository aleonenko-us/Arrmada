import { describe, expect, it } from "vitest";
import { HOMEPAGE_KEY_VAR, homepageYaml } from "./homepageYaml";

describe("homepageYaml", () => {
  it("fills in the address and the key", () => {
    const y = homepageYaml("http://192.168.1.10:7878/", "arrw_abc");
    expect(y).toContain("        href: http://192.168.1.10:7878\n");
    expect(y).toContain("          url: http://192.168.1.10:7878/api/v1/widget/homepage\n");
    expect(y).toContain("            X-Api-Key: arrw_abc\n");
    expect(y).toContain("type: customapi");
    expect(y).toContain("- field: needs_you");
  });

  it("uses a Homepage variable when the key isn't to hand", () => {
    expect(homepageYaml("http://arrmada:7878", "")).toContain(`X-Api-Key: ${HOMEPAGE_KEY_VAR}`);
  });

  // Homepage draws at most four blocks per widget and silently drops the rest.
  it("keeps the block widget to four fields", () => {
    const [blocks, list] = homepageYaml("http://x", "k").split("          - type: customapi").slice(1);
    expect(blocks.match(/- field: /g)).toHaveLength(4);
    expect(blocks).not.toContain("display:");
    expect(list).toContain("display: list");
  });

  it("never mixes tabs into the YAML", () => {
    expect(homepageYaml("http://x", "k")).not.toContain("\t");
  });
});
