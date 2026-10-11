import { describe, expect, it } from "vitest";
import { hhmm, prowlarrSyncMessage } from "./indexerStatus";

describe("prowlarrSyncMessage", () => {
  const none = { added: 0, updated: 0, unchanged: 0, disabled: 0, reenabled: 0, skipped_usenet: 0, flaresolverr_ready: false };
  it("lists what changed and never claims FlareSolverr unless Prowlarr has it", () => {
    expect(prowlarrSyncMessage({ ...none, added: 2, updated: 1, disabled: 1, unchanged: 4, skipped_usenet: 1 })).toBe(
      "Added 2, updated 1, turned off 1 (disabled or removed in Prowlarr). 4 indexers unchanged. Skipped 1 usenet indexer: Arrmada has no usenet download client.",
    );
    expect(prowlarrSyncMessage({ ...none, unchanged: 3 })).toBe("Nothing changed.");
    expect(prowlarrSyncMessage({ ...none, reenabled: 1, flaresolverr_ready: true, notes: ["Note."] })).toBe(
      "Turned 1 back on. Prowlarr has a FlareSolverr proxy for Cloudflare trackers. Note.",
    );
  });
});

describe("hhmm", () => {
  it("formats a 24-hour local time and a dash for nothing", () => {
    const d = new Date(2026, 9, 9, 7, 5);
    expect(hhmm(d.toISOString())).toBe("07:05");
    expect(hhmm(undefined)).toBe("—");
  });
});
