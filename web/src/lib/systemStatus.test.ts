import { describe, expect, it } from "vitest";
import { backupAge, versionLabel } from "./systemStatus";

describe("versionLabel", () => {
  it("adds the short commit in brackets", () => {
    expect(versionLabel("2026.10.08", "abc1234")).toBe("2026.10.08 (abc1234)");
  });
  it("leaves it off when unknown or already in the version", () => {
    expect(versionLabel("2026.10.08", "unknown")).toBe("2026.10.08");
    expect(versionLabel("2026.10.08", "")).toBe("2026.10.08");
    expect(versionLabel("2026.10.08-abc1234", "abc1234")).toBe("2026.10.08-abc1234");
  });
});

describe("backupAge", () => {
  const now = Date.parse("2026-10-11T12:00:00Z");
  it("says how long ago, and flags one older than two days", () => {
    expect(backupAge("2026-10-11T10:00:00Z", now)).toEqual({ text: "2 h ago", stale: false });
    expect(backupAge("2026-10-08T10:00:00Z", now)).toEqual({ text: "3 days ago", stale: true });
  });
  it("says never when there is none", () => {
    expect(backupAge(null, now)).toEqual({ text: "never", stale: true });
  });
});
