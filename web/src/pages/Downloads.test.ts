import { describe, expect, it } from "vitest";
import { isProblem, mins, phaseLabel, sinceLabel, stallLine } from "./Downloads";
import type { ActivityDownload } from "../lib/api";

describe("sinceLabel", () => {
  const now = new Date(2026, 9, 9, 15, 30);
  it("shows only the time for an outage that began today", () => {
    const got = sinceLabel(new Date(2026, 9, 9, 14, 2).toISOString(), now);
    expect(got).toMatch(/02/);
    expect(got).not.toMatch(/Oct/);
  });
  it("adds the date for an outage that began before today", () => {
    expect(sinceLabel(new Date(2026, 9, 7, 14, 2).toISOString(), now)).toMatch(/7/);
  });
  it("says nothing for a timestamp it can't read", () => {
    expect(sinceLabel("not a time", now)).toBe("");
  });
});

const NOW = 1_800_000_000; // unix seconds
const dl = (over: Partial<ActivityDownload> = {}): ActivityDownload => ({
  hash: "h", name: "Show.S03.1080p", state: "downloading", progress: 0.2, size_bytes: 1, down_speed: 0, up_speed: 0,
  eta_seconds: 8640000, ratio: 0, quality_profile: "hd", ...over,
});

describe("phaseLabel", () => {
  it("names a download the server calls stalled as Stalled, with how long and its seeds", () => {
    const c = phaseLabel(dl({ phase: "stalled", seeds: 0, stalled: true, stalled_since: NOW - 3 * 3600, last_activity: NOW - 3 * 3600 }), NOW);
    expect(c.text).toBe("Stalled 3h · 0 seeds");
    expect(c.tone).toBe("var(--avoid-text)");
    // The stall is the server's clock, so a torrent qBittorrent calls downloading can be one.
    expect(phaseLabel(dl({ phase: "downloading", seeds: 2, stalled: true, stalled_since: NOW - 40 * 60 }), NOW).text).toBe("Stalled 40m · 2 seeds");
    expect(phaseLabel(dl({ phase: "metadata", stalled: true, stalled_since: NOW - 26 * 3600 }), NOW).text).toBe("Stalled 1d 2h · no file list");
  });
  it("calls a moment with no peer sending Waiting for peers, quietly, until it counts as stalled", () => {
    const c = phaseLabel(dl({ phase: "stalled", seeds: 1, last_activity: NOW - 12 * 60 }), NOW);
    expect(c.text).toBe("Waiting for peers · 1 seed · no data for 12m");
    expect(c.tone).toBe("var(--ink-faint)");
    expect(phaseLabel(dl({ phase: "stalled", seeds: 1, added_on: NOW - 5 * 60 }), NOW).text).toBe("Waiting for peers · 1 seed · no data yet (5m)");
  });
  it("labels metadata, queued and error phases, and plain downloading only for downloading", () => {
    expect(phaseLabel(dl({ phase: "metadata" }), NOW).text).toBe("Fetching metadata");
    expect(phaseLabel(dl({ phase: "queued" }), NOW).text).toBe("Queued");
    expect(phaseLabel(dl({ phase: "error" }), NOW)).toMatchObject({ text: "Failed", tone: "var(--avoid-text)" });
    expect(phaseLabel(dl({ phase: "downloading" }), NOW).text).toBe("Downloading");
  });
});

describe("stallLine", () => {
  it("says how long there has been no progress and when another release is tried", () => {
    expect(stallLine(dl({ phase: "stalled", stall: { idle_minutes: 180, failover_in_minutes: 180, off: false } }))).toBe("No progress for 3h · trying another release in 3h");
  });
  it("says when auto-retry is off", () => {
    expect(stallLine(dl({ phase: "stalled", stall: { idle_minutes: 0, failover_in_minutes: 0, off: true } }))).toBe("Auto-retry off");
  });
  it("stays quiet for a moving download or one Arrmada didn't grab", () => {
    expect(stallLine(dl({ phase: "downloading", stall: { idle_minutes: 2, failover_in_minutes: 358, off: false } }))).toBeNull();
    expect(stallLine(dl({ phase: "stalled" }))).toBeNull();
  });
});

describe("isProblem", () => {
  it("counts a stalled download, not a moment without peers", () => {
    expect(isProblem(dl({ phase: "stalled", stalled: true }))).toBe(true);
    expect(isProblem(dl({ phase: "stalled" }))).toBe(false);
    expect(isProblem(dl({ phase: "metadata" }))).toBe(false);
    expect(isProblem(dl({ phase: "error" }))).toBe(true);
    expect(isProblem(dl({ phase: "seeding", stage: "needs_review" }))).toBe(true);
  });
});

describe("mins", () => {
  it("humanizes minutes", () => {
    expect(mins(45)).toBe("45m");
    expect(mins(180)).toBe("3h");
    expect(mins(26 * 60)).toBe("1d 2h");
  });
});
