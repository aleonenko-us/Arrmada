import { describe, expect, it } from "vitest";
import { looksLikeNZB, nzbPreview } from "./nzb";

const NZB = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <head><meta type="name">Film.2024.1080p.WEB-DL &amp; Extras</meta><meta type="password">secret</meta></head>
  <file poster="a@b" date="1" subject="Film [1/2] - &quot;Film.2024.part01.rar&quot; yEnc (1/2)">
    <groups><group>alt.binaries.test</group></groups>
    <segments><segment bytes="700000" number="1">a@b</segment><segment bytes="300000" number="2">c@d</segment></segments>
  </file>
  <file poster="a@b" date="1" subject='Film [2/2] - "Film.2024.par2" yEnc (1/1)'>
    <segments><segment number="1" bytes="5000">e@f</segment></segments>
  </file>
</nzb>`;

describe("nzbPreview", () => {
  it("reads the job name, the files by their quoted names, and their sizes", () => {
    expect(nzbPreview(NZB, "upload.nzb")).toEqual({
      name: "Film.2024.1080p.WEB-DL & Extras",
      size_bytes: 1_005_000,
      files: [
        { path: "Film.2024.part01.rar", size_bytes: 1_000_000 },
        { path: "Film.2024.par2", size_bytes: 5_000 },
      ],
    });
  });

  it("names the job after the file when the NZB doesn't", () => {
    const bare = NZB.replace(/<head>[\s\S]*<\/head>/, "");
    expect(nzbPreview(bare, "Film.2024.1080p.nzb").name).toBe("Film.2024.1080p");
  });

  it("refuses what isn't an NZB with files", () => {
    expect(() => nzbPreview("d8:announce", "x.nzb")).toThrow(/not an NZB/);
    expect(() => nzbPreview("<nzb></nzb>", "x.nzb")).toThrow(/no files/);
  });
});

describe("looksLikeNZB", () => {
  it("goes by the name, else by the start of the file", () => {
    expect(looksLikeNZB("a.NZB", "")).toBe(true);
    expect(looksLikeNZB("a.torrent", "d8:announce")).toBe(false);
    expect(looksLikeNZB("download", "<?xml?><nzb xmlns=\"x\">")).toBe(true);
  });
});
