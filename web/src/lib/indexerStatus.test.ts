import { describe, expect, it } from "vitest";
import { hhmm, prowlarrLastSyncLine, prowlarrSyncMessage, prowlarrUILink, usenetIndexerLine } from "./indexerStatus";

const now = Date.parse("2026-10-09T12:00:00Z");
const at = (minutes: number) => new Date(now + minutes * 60_000).toISOString();

describe("prowlarrSyncMessage", () => {
  const none = { added: 0, updated: 0, unchanged: 0, disabled: 0, reenabled: 0, skipped_usenet: 0, flaresolverr_ready: false };
  it("lists what changed and never claims FlareSolverr unless Prowlarr has it", () => {
    expect(prowlarrSyncMessage({ ...none, added: 2, updated: 1, disabled: 1, unchanged: 4, skipped_usenet: 1 })).toBe(
      "Added 2, updated 1, turned off 1 (disabled or removed in Prowlarr). 4 indexers unchanged. Skipped 1 usenet indexer: they come in once you add SABnzbd in Settings → Connections.",
    );
    expect(prowlarrSyncMessage({ ...none, unchanged: 3 })).toBe("Nothing changed.");
    expect(prowlarrSyncMessage({ ...none, reenabled: 1, flaresolverr_ready: true, notes: ["Note."] })).toBe(
      "Turned 1 back on. Prowlarr has a FlareSolverr proxy for Cloudflare trackers. Note.",
    );
  });
});

describe("usenetIndexerLine", () => {
  it("points at SABnzbd until there is one, then says it's searched", () => {
    expect(usenetIndexerLine(false)).toBe("Not searched until you add SABnzbd in Settings → Connections.");
    expect(usenetIndexerLine(true)).toMatch(/^Usenet indexer: searched/);
  });
});

describe("hhmm", () => {
  it("formats a 24-hour local time and a dash for nothing", () => {
    const d = new Date(2026, 9, 9, 7, 5);
    expect(hhmm(d.toISOString())).toBe("07:05");
    expect(hhmm(undefined)).toBe("—");
  });
});

describe("prowlarrLastSyncLine", () => {
  it("says when the last sync ran and what it changed", () => {
    const line = prowlarrLastSyncLine({ at: at(0), trigger: "schedule", added: 2, updated: 0, disabled: 1, reenabled: 0 });
    expect(line?.text).toBe(`Last synced ${hhmm(at(0))}: 2 added, 1 turned off`);
    expect(prowlarrLastSyncLine({ at: at(0), trigger: "manual", added: 0, updated: 0, disabled: 0, reenabled: 0 })?.text).toBe(`Last synced ${hhmm(at(0))}: nothing changed`);
  });

  it("shows a failed sync's error", () => {
    const line = prowlarrLastSyncLine({ at: at(0), trigger: "schedule", added: 0, updated: 0, disabled: 0, reenabled: 0, error: "prowlarr: invalid API key", failed_runs: 2 });
    expect(line?.text).toBe(`Last sync failed at ${hhmm(at(0))}: prowlarr: invalid API key`);
    expect(line?.color).toBe("var(--reject-text)");
    expect(prowlarrLastSyncLine(undefined)).toBeNull();
  });
});

describe("prowlarrUILink", () => {
  const loc = { protocol: "http:", hostname: "tower.local" };
  it("uses the published port, 9696 by default", () => {
    expect(prowlarrUILink({ ui_port: "9697" }, loc)).toBe("http://tower.local:9697");
    expect(prowlarrUILink(undefined, loc)).toBe("http://tower.local:9696");
  });
  it("prefers a saved http(s) address and ignores anything else", () => {
    expect(prowlarrUILink({ ui_port: "9697", ui_url: "https://prowlarr.example.test" }, loc)).toBe("https://prowlarr.example.test");
    expect(prowlarrUILink({ ui_port: "9697", ui_url: "javascript:alert(1)" }, loc)).toBe("http://tower.local:9697");
  });
});
