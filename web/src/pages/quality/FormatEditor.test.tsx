// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { CustomFormatsEditor } from "./FormatEditor";
import type { StoredProfile } from "../../lib/api";
import { qualityApi, type FormatCheck } from "../../lib/qualityApi";
import { clearQueryCache } from "../../lib/query";

const vocabulary = {
  codec: [{ value: "x265", label: "HEVC (x265, H.265)" }, { value: "x264", label: "H.264 (x264, AVC)" }, { value: "AV1", label: "AV1" }],
  source: [{ value: "Remux", label: "Remux" }, { value: "BluRay", label: "BluRay (encode)" }, { value: "WEB-DL", label: "WEB-DL" }],
};

const base: StoredProfile = {
  id: 3, media_type: "movie", name: "Mine", allowed_resolutions: [], min_source: "", max_source: "", bitrate_cap_mbps: 0,
  small_bias: 0, min_format_score: 0, format_scores: {}, min_seeders: 0, stall_minutes: 0, upgrades_enabled: false, upgrade_min_percent: 0,
};

let latest: StoredProfile = base;

// Host holds the profile the way VideoBuilder does, so every patch lands in state.
function Host({ initial }: { initial: StoredProfile }) {
  const [sp, setSp] = useState(initial);
  latest = sp;
  return <CustomFormatsEditor sp={sp} patch={(p) => setSp((s) => ({ ...s, ...p }))} />;
}

let check: (formats: unknown[]) => FormatCheck;

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  clearQueryCache();
  vi.spyOn(qualityApi, "qualityProfiles").mockResolvedValue({ profiles: [], formats: [], vocabulary });
  check = (formats) => ({ errors: [], warnings: [], matches: formats.map(() => 1), samples: 7 });
  vi.spyOn(qualityApi, "checkFormats").mockImplementation(async (fs) => check(fs));
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const settle = () => act(async () => { await vi.advanceTimersByTimeAsync(400); });

describe("CustomFormatsEditor", () => {
  it("adds a format with two conditions, one negated", async () => {
    render(<Host initial={base} />);
    await settle();
    fireEvent.click(screen.getByText("+ New custom format"));
    fireEvent.change(screen.getByLabelText("Format name"), { target: { value: "BluRay not YTS" } });
    fireEvent.change(screen.getByLabelText("Condition 1 type"), { target: { value: "source" } });
    fireEvent.change(screen.getByLabelText("Condition 1 value"), { target: { value: "BluRay" } });
    fireEvent.click(screen.getByText("+ Add condition"));
    fireEvent.change(screen.getByLabelText("Condition 2 type"), { target: { value: "release_group" } });
    fireEvent.change(screen.getByLabelText("Condition 2 value"), { target: { value: "YTS" } });
    fireEvent.click(screen.getByLabelText("Condition 2: NOT"));
    await settle();
    fireEvent.click(screen.getByText("Done"));

    expect(latest.custom_formats).toEqual([{ name: "BluRay not YTS", conditions: [
      { type: "source", value: "BluRay" },
      { type: "release_group", value: "YTS", negate: true },
    ] }]);
    expect(latest.format_scores["BluRay not YTS"]).toBe(50);
    expect(screen.getByText("Source BluRay AND NOT Release group YTS")).toBeTruthy();
    await settle();
    expect(screen.getByText("Matches 1 of the 7 sample releases")).toBeTruthy();
  });

  it("offers codec values from the vocabulary and stores the canonical one", async () => {
    render(<Host initial={base} />);
    await settle();
    fireEvent.click(screen.getByText("+ New custom format"));
    fireEvent.change(screen.getByLabelText("Format name"), { target: { value: "Efficient" } });
    fireEvent.change(screen.getByLabelText("Condition 1 type"), { target: { value: "codec" } });
    const value = screen.getByLabelText("Condition 1 value") as HTMLSelectElement;
    expect(within(value).getByText("HEVC (x265, H.265)")).toBeTruthy();
    fireEvent.change(value, { target: { value: "x265" } });
    await settle();
    fireEvent.click(screen.getByText("Done"));
    expect(latest.custom_formats?.[0].conditions).toEqual([{ type: "codec", value: "x265" }]);
  });

  it("edits an existing format in place, and a rename keeps its score and must", async () => {
    const sp: StoredProfile = {
      ...base,
      custom_formats: [
        { name: "Groups", conditions: [{ type: "release_group", value: "FLUX" }] },
        { name: "Other", conditions: [{ type: "source", value: "Remux" }] },
      ],
      format_scores: { Groups: 120, Other: 10 },
      required_formats: ["Groups"],
    };
    render(<Host initial={sp} />);
    await settle();
    fireEvent.click(screen.getAllByText("Edit")[0]);
    fireEvent.change(screen.getByLabelText("Format name"), { target: { value: "Tier 1" } });
    fireEvent.click(screen.getByText("Match any"));
    fireEvent.click(screen.getByText("+ Add condition"));
    fireEvent.change(screen.getByLabelText("Condition 2 type"), { target: { value: "release_group" } });
    fireEvent.change(screen.getByLabelText("Condition 2 value"), { target: { value: "NTb" } });
    await settle();
    fireEvent.click(screen.getByText("Done"));

    expect(latest.custom_formats).toEqual([
      { name: "Tier 1", match: "any", conditions: [{ type: "release_group", value: "FLUX" }, { type: "release_group", value: "NTb" }] },
      { name: "Other", conditions: [{ type: "source", value: "Remux" }] },
    ]);
    expect(latest.format_scores).toEqual({ "Tier 1": 120, Other: 10 });
    expect(latest.required_formats).toEqual(["Tier 1"]);
  });

  it("shows an invalid pattern inline and won't apply it", async () => {
    check = (formats) => {
      const last = formats.length - 1;
      return {
        errors: [{ format: "Bad", index: last, condition: 0, message: "condition 1's release-name pattern isn't valid: missing closing )" }],
        warnings: [], matches: formats.map(() => 0), samples: 7,
      };
    };
    render(<Host initial={base} />);
    await settle();
    fireEvent.click(screen.getByText("+ New custom format"));
    fireEvent.change(screen.getByLabelText("Format name"), { target: { value: "Bad" } });
    fireEvent.change(screen.getByLabelText("Condition 1 type"), { target: { value: "release_title" } });
    fireEvent.change(screen.getByLabelText("Condition 1 value"), { target: { value: "(FLUX" } });
    await settle();
    expect(screen.getByText(/pattern isn't valid/)).toBeTruthy();
    expect((screen.getByText("Done") as HTMLButtonElement).disabled).toBe(true);
    expect(latest.custom_formats ?? []).toEqual([]);
  });

  it("imports TRaSH formats with their warnings, adding the chosen ones unscored", async () => {
    const importTrash = vi.spyOn(qualityApi, "importTrash").mockResolvedValue({
      formats: [
        { format: { name: "x265 (HD)", conditions: [{ type: "release_title", value: "[xh][ ._-]?265" }, { type: "resolution", value: "2160p", negate: true }] }, suggested_score: -10000, warnings: [] },
        { format: { name: "Remaster", conditions: [{ type: "release_title", value: "\\bRemaster(ed)?\\b" }] }, warnings: ["Matches less than the original: skipped \"Restored\"."] },
        { format: { name: "Groups", conditions: [{ type: "release_group", value: "FLUX|NTb" }] }, warnings: [] },
      ],
      warnings: ['"BR-DISK" wasn\'t imported: it uses a lookaround'],
    });
    const sp: StoredProfile = { ...base, custom_formats: [{ name: "Groups", conditions: [{ type: "release_group", value: "FLUX" }] }], format_scores: { Groups: 40 } };
    render(<Host initial={sp} />);
    await settle();
    fireEvent.click(screen.getByText("Import from TRaSH"));
    fireEvent.change(screen.getByLabelText("TRaSH custom-format JSON"), { target: { value: '{"name":"x"}' } });
    await act(async () => { fireEvent.click(screen.getByText("Convert")); });
    expect(importTrash).toHaveBeenCalledWith('{"name":"x"}', "movie");
    expect(screen.getByText(/BR-DISK" wasn't imported/)).toBeTruthy();
    expect(screen.getByText(/Matches less than the original/)).toBeTruthy();
    expect(screen.getByText("TRaSH suggests -10000")).toBeTruthy();
    expect(screen.getByText("replaces the one in this profile")).toBeTruthy();

    fireEvent.click(screen.getByRole("checkbox", { name: /Remaster/ }));
    fireEvent.click(screen.getByText("Add 2 formats"));
    expect(latest.custom_formats).toEqual([
      { name: "Groups", conditions: [{ type: "release_group", value: "FLUX|NTb" }] },
      { name: "x265 (HD)", conditions: [{ type: "release_title", value: "[xh][ ._-]?265" }, { type: "resolution", value: "2160p", negate: true }] },
    ]);
    expect(latest.format_scores).toEqual({ Groups: 40 });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("refuses a name another format has, and removes a format with its score", async () => {
    const sp: StoredProfile = { ...base, custom_formats: [{ name: "Groups", conditions: [{ type: "release_group", value: "FLUX" }] }], format_scores: { Groups: 5 } };
    render(<Host initial={sp} />);
    await settle();
    fireEvent.click(screen.getByText("+ New custom format"));
    fireEvent.change(screen.getByLabelText("Format name"), { target: { value: "groups" } });
    fireEvent.change(screen.getByLabelText("Condition 1 value"), { target: { value: "NTb" } });
    expect(screen.getByText("Another custom format already has this name.")).toBeTruthy();
    expect((screen.getByText("Done") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByText("Cancel"));
    fireEvent.click(screen.getByLabelText("Remove Groups"));
    expect(latest.custom_formats).toEqual([]);
    expect(latest.format_scores).toEqual({});
  });
});
