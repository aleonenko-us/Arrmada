import { describe, expect, it } from "vitest";
import type { ConvertPlan } from "../../lib/api";
import { planChanges, trackLabel, videoLine } from "./plan";

const GB = 1024 ** 3;

const dvRemuxPlan: ConvertPlan = {
  key: "movie:7", kind: "movie", title: "Remux", size_bytes: 70 * GB,
  needs: { video: true, subs: true, audio: true, save: 30 * GB, worth: true },
  video: { from_codec: "HEVC", from_res: "2160p", from_hdr: "Dolby Vision", from_bits: 10, to_codec: "HEVC", to_bits: 10, to_hdr: "HDR10", crf: 18, est_bytes: 40 * GB },
  audio: [
    { type: "audio", index: 0, codec: "truehd", lang: "eng", channels: 8, keep: true },
    { type: "audio", index: 1, codec: "ac3", lang: "eng", channels: 2, title: "Commentary", keep: false, reason: "commentary" },
  ],
  subs: [
    { type: "subtitle", index: 0, codec: "hdmv_pgs_subtitle", lang: "eng", image: true, keep: false, reason: "image subtitle covered by text" },
    { type: "subtitle", index: 1, codec: "hdmv_pgs_subtitle", lang: "eng", image: true, forced: true, keep: true },
  ],
  container: { from: "MP4", to: "MKV" },
  warnings: ["Dolby Vision layer dropped — kept as HDR10"],
  eta_sec: null,
};

describe("conversion plan words", () => {
  it("names the picture before and after", () => {
    expect(videoLine(dvRemuxPlan)).toBe("HEVC 2160p Dolby Vision 10-bit → HEVC HDR10 10-bit · CRF 18");
    expect(videoLine({ ...dvRemuxPlan, video: { ...dvRemuxPlan.video, to_codec: undefined } })).toBe("HEVC 2160p Dolby Vision 10-bit — copied untouched");
  });

  it("names tracks the way a player menu would", () => {
    expect(trackLabel(dvRemuxPlan.audio[1])).toBe("ENG · AC-3 stereo · Commentary");
    expect(trackLabel(dvRemuxPlan.subs[1])).toBe("ENG · PGS · forced · image");
    expect(trackLabel({ type: "audio", index: 0, codec: "aac", keep: true })).toBe("untagged · AAC");
  });

  it("lists one line per change, removed tracks with their reasons", () => {
    const lines = planChanges(dvRemuxPlan);
    expect(lines[0]).toMatch(/^Picture: HEVC 2160p Dolby Vision/);
    expect(lines[1]).toBe("Size: 70.0 GB → ~40.0 GB (estimated)");
    expect(lines).toContain("Audio: 1 of 2 tracks removed — ENG · AC-3 stereo · Commentary (commentary)");
    expect(lines).toContain("Subtitles: 1 of 2 tracks removed — ENG · PGS · image (image subtitle covered by text)");
    expect(lines).toContain("Container: MP4 → MKV");
  });

  it("says a tracks-only rewrite leaves the picture alone", () => {
    const tidy: ConvertPlan = { ...dvRemuxPlan, video: { ...dvRemuxPlan.video, to_codec: undefined }, needs: { ...dvRemuxPlan.needs, video: false, save: 2 * GB } };
    expect(planChanges(tidy)[0]).toMatch(/^Size:/);
  });
});
