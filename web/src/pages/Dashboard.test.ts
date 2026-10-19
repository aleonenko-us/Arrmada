import { describe, expect, it } from "vitest";
import { downloadingSub, musicTileSub, releaseLine } from "./Dashboard";
import type { QueueSummary } from "../lib/api";

const q = (over: Partial<QueueSummary> = {}): QueueSummary => ({ downloading: 0, seeding: 0, paused: 0, errored: 0, down_speed: 0, up_speed: 0, ...over });

describe("downloadingSub", () => {
  it("says the client is unreachable instead of '0 seeding'", () => {
    expect(downloadingSub({ queue: q(), queue_note: "all download clients failed to list" })).toBe("Client unreachable");
  });

  it("counts errored torrents", () => {
    expect(downloadingSub({ queue: q({ errored: 2, seeding: 3 }) })).toBe("2 errors · 3 seeding");
  });

  it("mentions torrents waiting for peers only when there are some", () => {
    expect(downloadingSub({ queue: q({ seeding: 1 }) })).toBe("1 seeding");
    expect(downloadingSub({ queue: q({ seeding: 1, stalled: 4 }) })).toBe("4 stalled · 1 seeding");
  });
});

// MUS-24: the Albums tile and the New releases rows.
describe("music lines", () => {
  it("leads the Albums tile with what's missing, then the artists", () => {
    expect(musicTileSub({ artists: 12, albums_missing: 3 })).toBe("3 missing · 12 artists");
    expect(musicTileSub({ artists: 1, albums_missing: 0 })).toBe("1 artist");
  });

  it("writes a new release's artist, kind and date, skipping what's unknown", () => {
    expect(releaseLine({ artist_name: "Low", album_type: "EP", release_date: "2026-11-14" })).toBe("Low · EP · 2026-11-14");
    expect(releaseLine({ artist_name: "Low" })).toBe("Low");
  });
});
