import { describe, expect, it } from "vitest";
import { progressBadge, rangeBounds } from "./HistoryView";

describe("rangeBounds", () => {
  const now = new Date(2026, 9, 13, 15, 30); // 13 Oct 2026, 15:30 local
  const midnight = (y: number, m: number, d: number) => Math.floor(new Date(y, m, d).getTime() / 1000);

  it("runs the presets from local midnight, so they hold still all day", () => {
    expect(rangeBounds("7d", "", "", now)).toEqual({ from: midnight(2026, 9, 7) });
    expect(rangeBounds("30d", "", "", now).from).toBe(midnight(2026, 8, 14));
    expect(rangeBounds("all", "", "", now)).toEqual({});
  });

  it("includes the whole of the custom range's last day", () => {
    expect(rangeBounds("custom", "2026-10-01", "2026-10-05", now)).toEqual({ from: midnight(2026, 9, 1), to: midnight(2026, 9, 6) });
    expect(rangeBounds("custom", "", "2026-10-05", now)).toEqual({ from: undefined, to: midnight(2026, 9, 6) });
  });
});

describe("progressBadge", () => {
  it("says Watched from 90%, Partial below, nothing without a runtime", () => {
    expect(progressBadge({ duration_ms: 1000, progress_pct: 95 })).toEqual({ label: "Watched", done: true });
    expect(progressBadge({ duration_ms: 1000, progress_pct: 90 })).toEqual({ label: "Watched", done: true });
    expect(progressBadge({ duration_ms: 1000, progress_pct: 42 })).toEqual({ label: "Partial 42%", done: false });
    expect(progressBadge({ duration_ms: 0, progress_pct: 0 })).toBeNull();
  });
});
