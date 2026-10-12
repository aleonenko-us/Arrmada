// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { TestName } from "./TestName";
import type { QualityExplain, StoredProfile } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const profile = (must: boolean): StoredProfile => ({
  id: 0, media_type: "series", name: "", base: "", allowed_resolutions: [], min_source: "", max_source: "",
  bitrate_cap_mbps: 0, small_bias: 0, min_format_score: 0, format_scores: {}, min_seeders: 0, stall_minutes: 0,
  upgrades_enabled: true, upgrade_min_percent: 0, upgrade_trigger: "any", ideal: must ? { codec: { hevc: "must" } } : {},
});

const answer = (eligible: boolean): QualityExplain => ({
  release: { title: "Show", resolution: "1080p", source: "WEB-DL", source_inferred: false, codec: "x264", group: "GROUP" },
  evaluation: {
    candidate: { name: "Show.S01E01.1080p.WEB.h264-GROUP", release: { title: "Show" }, size_gb: 0, seeders: 1 },
    eligible, reject_reason: eligible ? undefined : "No HEVC — your profile requires it",
    quality_score: 0, format_score: 0, size_score: 0, total: 0,
  },
  formats: [{ name: "HEVC", matched: false, score: 50, required: !eligible }, { name: "H.264", matched: true, score: 0 }],
});

describe("Test a release name", () => {
  it("shows what was read and the verdict, and re-judges when the profile changes", async () => {
    const explain = vi.spyOn(qualityApi, "qualityExplain").mockImplementation(async (b) => answer(!b.profile.ideal?.codec?.hevc));
    const { rerender } = render(<TestName sp={profile(true)} />);
    fireEvent.change(screen.getByLabelText("Release name to test"), { target: { value: "Show.S01E01.1080p.WEB.h264-GROUP" } });

    expect(await screen.findByText("No HEVC — your profile requires it")).toBeTruthy();
    expect(screen.getByText("GROUP")).toBeTruthy();
    expect(screen.getByText("Must")).toBeTruthy();
    expect(explain).toHaveBeenLastCalledWith(expect.objectContaining({ name: "Show.S01E01.1080p.WEB.h264-GROUP" }));

    // Clearing the Must in the editor (an unsaved edit) re-judges the same name.
    rerender(<TestName sp={profile(false)} />);
    expect(await screen.findByText(/Eligible — this profile would grab it/)).toBeTruthy();
    await waitFor(() => expect(explain).toHaveBeenCalledTimes(2));
  });
});
