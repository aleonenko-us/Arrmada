// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { GroupTiersEditor } from "./GroupTiers";
import type { StoredProfile } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";
import { clearQueryCache } from "../../lib/query";

const base: StoredProfile = {
  id: 3, media_type: "movie", name: "Mine", allowed_resolutions: [], min_source: "", max_source: "", bitrate_cap_mbps: 0,
  small_bias: 0, min_format_score: 0, format_scores: {}, min_seeders: 0, stall_minutes: 0, upgrades_enabled: false, upgrade_min_percent: 0,
};
let latest: StoredProfile = base;

function Host({ initial }: { initial: StoredProfile }) {
  const [sp, setSp] = useState(initial);
  latest = sp;
  return <GroupTiersEditor sp={sp} patch={(p) => setSp((s) => ({ ...s, ...p }))} />;
}

beforeEach(() => {
  clearQueryCache();
  vi.spyOn(qualityApi, "qualityProfiles").mockResolvedValue({
    profiles: [], formats: [], unwanted_groups: ["YIFY", "YTS"], group_tiers: { tier1: ["FraMeSToR", "FLUX"], tier2: ["CMRG"] },
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("GroupTiersEditor", () => {
  it("starts from the suggestions, adds, moves and removes groups, and goes back", async () => {
    await act(async () => { render(<Host initial={base} />); });
    expect(await screen.findByText("FraMeSToR")).toBeTruthy();
    expect(screen.getByText(/suggestions until you change them/)).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Add a group to Tier 2"), { target: { value: "NTb" } });
    fireEvent.click(screen.getAllByText("Add")[1]);
    expect(latest.group_tiers).toEqual({ tier1: ["FraMeSToR", "FLUX"], tier2: ["CMRG", "NTb"] });

    // Adding a tier-2 group to tier 1 moves it.
    fireEvent.change(screen.getByLabelText("Add a group to Tier 1"), { target: { value: "cmrg" } });
    fireEvent.click(screen.getAllByText("Add")[0]);
    expect(latest.group_tiers).toEqual({ tier1: ["FraMeSToR", "FLUX", "cmrg"], tier2: ["NTb"] });

    fireEvent.click(screen.getByLabelText("Remove FLUX from Tier 1"));
    expect(latest.group_tiers?.tier1).toEqual(["FraMeSToR", "cmrg"]);

    fireEvent.click(screen.getByText("Use the suggestions"));
    expect(latest.group_tiers).toBeUndefined();
  });

  it("won't tier a low-quality group", async () => {
    await act(async () => { render(<Host initial={base} />); });
    await screen.findByText("FraMeSToR");
    fireEvent.change(screen.getByLabelText("Add a group to Tier 1"), { target: { value: "yts" } });
    fireEvent.click(screen.getAllByText("Add")[0]);
    expect(screen.getByRole("alert").textContent).toMatch(/low-quality groups/);
    expect(latest.group_tiers).toBeUndefined();
  });
});
