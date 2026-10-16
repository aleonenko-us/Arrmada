import { describe, expect, it } from "vitest";
import type { PreviewResult, RequestPreview } from "./api";
import { checkedAgo, forOtherProfile, grabApplies, previewHeadline, previewLine, reasonsLine, releaseFacts, seasonLine } from "./requestPreview";

const found = (over: Partial<PreviewResult> = {}): PreviewResult => ({
  outcome: "found", reason: "grabbed", profile: "hd", returned: 12, matching: 5, eligible: 3, searched: 2, queries: 1,
  releases: [{ title: "Heat.1995.1080p.BluRay.x264-GRP", indexer: "TL", summary: "1080p · BluRay", resolution: "1080p", source: "BluRay", codec: "x264", size_gb: 12.4, seeders: 40, transport: "torrent" }],
  ...over,
});
const preview = (result: PreviewResult | undefined, over: Partial<RequestPreview> = {}): RequestPreview => ({
  state: "done", checked_at: 1_760_000_000, profile: "hd", stale: false, grabbable: true, result, ...over,
});

describe("request previews", () => {
  it("says what would be grabbed, or why nothing would", () => {
    expect(previewHeadline(preview(found())).title).toBe("Would grab");
    expect(previewHeadline(preview(found({ outcome: "none", reason: "none-for-this-title", releases: [] }))).title).toBe("Nothing found yet");
    expect(previewHeadline(preview(found({ outcome: "unavailable", reason: "indexers-paused", releases: [] }))).title).toBe("Indexers unavailable");
    expect(previewHeadline(preview(undefined, { state: "queued", stale: true })).title).toBe("Not checked yet");
    expect(previewHeadline(preview(undefined, { state: "checking" })).title).toBe("Checking indexers…");
  });

  it("lists a release's facts, seeders or Usenet", () => {
    expect(releaseFacts(found().releases[0])).toEqual(["1080p", "BluRay", "x264", "12.4 GB", "40 seeders"]);
    expect(releaseFacts({ ...found().releases[0], resolution: "2160p", hdr: ["DV", "HDR10"], transport: "usenet" }))
      .toEqual(["4K", "BluRay", "x264", "DV", "HDR10", "12.4 GB", "Usenet"]);
    expect(previewLine(preview(found()))).toBe("Would grab 1080p · BluRay · x264 · 12.4 GB");
  });

  it("sums up a show's season: a pack, or episodes in all", () => {
    const pack = found({ season: 1, seasons_note: "2 more seasons not checked", releases: [{ ...found().releases[0], title: "Show.S01.1080p", kind: "pack", covers: "S01", size_gb: 22.4 }] });
    expect(seasonLine(pack)).toBe("Season 1: 1 pack · 1080p BluRay · 22.4 GB");
    const eps = found({ season: 2, releases: [1, 2].map((n) => ({ ...found().releases[0], title: `Show.S02E0${n}`, kind: "episode" as const, size_gb: 1.5 })) });
    expect(seasonLine(eps)).toBe("Season 2: 2 episodes · 1080p BluRay · 3.0 GB in all");
    expect(seasonLine(found())).toBe("");
  });

  it("names the commonest reasons nothing fit", () => {
    const none = found({ outcome: "none", releases: [], top_reasons: [{ code: "wrong_title", label: "for other titles", count: 9 }, { code: "seeders", label: "too few seeders", count: 3 }] });
    expect(reasonsLine(none)).toBe("12 releases · 9 for other titles, 3 too few seeders");
    expect(reasonsLine(found({ outcome: "none", returned: 0, releases: [] }))).toBe("The indexers returned no releases");
  });

  it("offers Approve & grab only for the profile and seasons it was checked for", () => {
    const movie = preview(found());
    expect(grabApplies(movie, "hd", [])).toBe(true);
    expect(grabApplies(movie, "uhd", [])).toBe(false);
    expect(grabApplies({ ...movie, grabbable: false }, "hd", [])).toBe(false);
    expect(grabApplies(preview(found({ outcome: "none", releases: [] })), "hd", [])).toBe(false);
    // A show: exactly the seasons it was checked for, in any order; the whole show only as itself.
    const show = preview(found({ season: 2 }), { seasons: [2, 3, 4] });
    expect(grabApplies(show, "hd", [4, 3, 2])).toBe(true);
    expect(grabApplies(show, "hd", [2, 3])).toBe(false);
    expect(grabApplies(show, "hd", [])).toBe(false);
    const whole = preview(found({ season: 1 }));
    expect(grabApplies(whole, "hd", [])).toBe(true);
    expect(grabApplies(whole, "hd", [1, 2, 3])).toBe(false);
  });

  it("follows a library title's own profile, whatever is picked", () => {
    const lib = preview(found({ in_library: true }), { profile: "own" });
    expect(forOtherProfile(lib, "uhd")).toBe(false);
    expect(grabApplies(lib, "uhd", [])).toBe(true);
    expect(forOtherProfile(preview(found()), "uhd")).toBe(true);
  });

  it("says how long ago it was checked", () => {
    const now = 1_760_000_000_000;
    expect(checkedAgo(1_760_000_000 - 30, now)).toBe("just now");
    expect(checkedAgo(1_760_000_000 - 12 * 60, now)).toBe("12 min ago");
    expect(checkedAgo(1_760_000_000 - 3 * 3600, now)).toBe("3 h ago");
    expect(checkedAgo(1_760_000_000 - 2 * 86400, now)).toBe("2 days ago");
    expect(checkedAgo(0, now)).toBe("");
  });
});
