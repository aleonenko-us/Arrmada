import { describe, expect, it } from "vitest";
import type { PlexMergePreview } from "../lib/plexApi";
import { mergeLines } from "./PlexMergeDialog";

const base: PlexMergePreview = {
  from: "dup", to: "owner", requests: 2, following: 0, notifications: 0, push_devices: 0, quota_usage: 0,
  audiobook_progress: false, audiobook_password: false, signed_in_devices: 0, listening_apps: 0,
};

describe("mergeLines", () => {
  it("names both links when both move", () => {
    expect(mergeLines({ ...base, plex_link: true, plex_username: "dupplex", jellyfin_link: true, jellyfin_username: "dupjf" }))
      .toEqual(["2 requests", "the Plex link (dupplex)", "the Jellyfin link (dupjf)"]);
  });
  it("leaves out a link the duplicate doesn't have", () => {
    expect(mergeLines({ ...base, plex_link: false, jellyfin_link: true, jellyfin_username: "dupjf" })).toEqual(["2 requests", "the Jellyfin link (dupjf)"]);
  });
  it("reads an older server's answer (no link flags) as a Plex merge", () => {
    expect(mergeLines({ ...base, plex_username: "dupplex" })).toEqual(["2 requests", "the Plex link (dupplex)"]);
  });
});
