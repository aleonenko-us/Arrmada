import { describe, expect, it } from "vitest";
import { bandwidthLabels, dayLabel } from "./shared";

describe("bandwidthLabels", () => {
  it("labels the first bucket of each day with its date and leaves the rest blank", () => {
    const got = bandwidthLabels(["2026-10-08 22:00", "2026-10-08 23:00", "2026-10-09 00:00", "2026-10-09 01:00", "2026-10-10 13:00"]);
    expect(got).toEqual([dayLabel("2026-10-08"), "", dayLabel("2026-10-09"), "", dayLabel("2026-10-10")]);
    expect(got[0]).not.toBe("");
  });

  it("reads the date as a local calendar day, not shifted by the time zone", () => {
    expect(dayLabel("2026-01-01")).toBe(new Date(2026, 0, 1).toLocaleDateString(undefined, { month: "short", day: "numeric" }));
  });
});
