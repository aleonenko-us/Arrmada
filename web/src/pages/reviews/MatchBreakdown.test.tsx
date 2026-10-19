// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { MusicMatchReport } from "../../lib/api";
import { MatchBreakdown, distanceParts } from "./MatchBreakdown";

// MUS-31: Reviews says in words why the music matcher held a download.

const report: MusicMatchReport = {
  reason: "partial", summary: "Only 3 of the 12 tracks are here.", expected: false, threshold: 0.04,
  candidates: [{
    album_id: 1, release_id: 2, album: "OK Computer", edition: "GB CD 1997 · 12 tracks", target: true, distance: 0.402, fit: 0.01,
    components: [
      { name: "missing_tracks", weight: 0.9, penalty: 9, count: 9, share: 0.39 },
      { name: "album", weight: 3, penalty: 0.1, count: 1, share: 0.012 },
      { name: "artist", weight: 3, penalty: 0, count: 1, share: 0 },
    ],
    tracks: [{ file: "02.flac", track: "02 Paranoid Android", distance: 0.13, notes: ["length off by 45 s"] }],
    missing: ["04 Exit Music (For a Film)"],
    extra: ["bonus.flac"],
  }],
  unplaced: [{ file: "zz.flac", why: "matches no track" }],
};

afterEach(cleanup);

describe("MatchBreakdown", () => {
  it("names what made up the distance, biggest first, leaving out what cost nothing", () => {
    expect(distanceParts(report.candidates![0])).toEqual(["tracks with no file +0.39", "album title +0.01"]);
  });

  it("says why, track by track, and what would be left behind", () => {
    render(<MemoryRouter><MatchBreakdown match={report} /></MemoryRouter>);
    expect(screen.getByText(/Nobody grabbed this for an album/)).toBeTruthy();
    expect(screen.getByText(/up to a distance of\s+0\.04/)).toBeTruthy();
    expect(screen.getByText("GB CD 1997 · 12 tracks")).toBeTruthy();
    expect(screen.getByText("distance 0.40")).toBeTruthy();
    expect(screen.getByText(/length off by 45 s/)).toBeTruthy();
    expect(screen.getByText("No file for: 04 Exit Music (For a Film)")).toBeTruthy();
    expect(screen.getByText("No track for: bonus.flac")).toBeTruthy();
    expect(screen.getByText(/zz\.flac \(matches no track\)/)).toBeTruthy();
    // AcoustID wasn't asked about, so nothing invites setting it up.
    expect(screen.queryByText(/AcoustID key/)).toBeNull();
  });

  it("points at the AcoustID key when it would have helped", () => {
    render(<MemoryRouter><MatchBreakdown match={{ ...report, fingerprint: "off" }} /></MemoryRouter>);
    expect(screen.getByRole("link", { name: "Settings → Connections" }).getAttribute("href")).toBe("/settings/connections#apikey-acoustid");
  });
});
