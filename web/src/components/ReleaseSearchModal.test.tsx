// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ReleaseSearchModal } from "./ReleaseSearchModal";
import type { ReleaseList } from "../lib/api";

afterEach(cleanup);

const list: ReleaseList = {
  profile: "custom:1",
  why: ["Highest quality that matches your goal"],
  chosen_over: "Chosen over the 1080p WEB-DL — lower resolution",
  releases: [
    { title: "Film.2021.2160p.WEB-DL.x265-A", indexer: "x", token: "t1", size_gb: 12, seeders: 40, summary: "4K · WEB-DL", eligible: true, recommended: true, matched: ["HEVC"] },
    { title: "Film.2021.1080p.WEB-DL.x265-B", indexer: "x", token: "t2", size_gb: 6, seeders: 40, summary: "1080p · WEB-DL", eligible: true, recommended: false, matched: ["HEVC"], lost_on: "lower resolution", lost_on_key: "resolution" },
    { title: "Film.2021.2160p.BluRay.DV.x264-C", indexer: "x", token: "t3", size_gb: 40, seeders: 9, summary: "4K · DV · BluRay", eligible: true, recommended: false, avoided: ["Dolby Vision"], bonus_waived: true, lost_on: "it has Dolby Vision, which you avoid" },
    { title: "Film.2021.720p.HDTV.x264-D", indexer: "x", token: "t4", size_gb: 1, seeders: 3, summary: "720p · HDTV", eligible: false, recommended: false, reject_reason: "Not in profile — 720p", lost_on: "should not show" },
  ],
};

describe("ReleaseSearchModal explanations", () => {
  it("explains every eligible row and what the recommendation beat", async () => {
    render(<ReleaseSearchModal title="Film" fetchReleases={() => Promise.resolve(list)} onGrab={vi.fn()} onClose={vi.fn()} />);
    expect(await screen.findByText("Chosen over the 1080p WEB-DL — lower resolution")).toBeTruthy();
    expect(screen.getAllByText("✓ HEVC")).toHaveLength(2);
    expect(screen.getByText("ranked lower: lower resolution")).toBeTruthy();
    expect(screen.getByText("⚠ Dolby Vision (avoided)")).toBeTruthy();
    expect(screen.getByText("preference ignored — bitrate too low")).toBeTruthy();
    expect(screen.getByText("ranked lower: it has Dolby Vision, which you avoid")).toBeTruthy();
    // A rejected row keeps its reject reason and nothing else.
    expect(screen.getByText("Not in profile — 720p")).toBeTruthy();
    expect(screen.queryByText(/should not show/)).toBeNull();
  });
});
