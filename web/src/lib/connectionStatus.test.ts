import { describe, expect, it } from "vitest";
import type { ConnectionItem } from "./connectionsApi";
import { connectionDot, connectionStatusLine, countsLine, kindLabel, needsFix } from "./connectionStatus";
import { hhmm } from "./indexerStatus";

const item = (over: Partial<ConnectionItem> = {}): ConnectionItem => ({
  kind: "indexer", ref: "1", card: "indexer-1", name: "TorrentLeech", configured: true, enabled: true,
  state: "ok", testable: true, ...over,
});

const NOW = Date.parse("2026-10-11T14:30:00Z");

describe("connection status", () => {
  it("says when it last worked", () => {
    expect(connectionStatusLine(item({ last_ok_at: new Date(NOW - 3 * 60_000).toISOString() }), NOW).text).toBe("Last OK 3 min ago");
  });

  it("says since when it's failing, why, and when it's next tried", () => {
    const since = "2026-10-11T14:02:00Z";
    const next = "2026-10-11T15:00:00Z";
    const line = connectionStatusLine(item({ state: "backing_off", failing_since: since, last_error: "login failed", backoff_until: next }), NOW);
    expect(line.text).toBe(`Failing since ${hhmm(since)}: login failed · next try ${hhmm(next)}`);
    expect(line.color).toBe("var(--reject-text)");
    expect(connectionStatusLine(item({ state: "failing", last_error: "refused" }), NOW).text).toBe("Failing: refused");
  });

  it("names what isn't set up, switched off or never asked", () => {
    expect(connectionStatusLine(item({ configured: false, state: "unknown" })).text).toBe("Not set up");
    expect(connectionStatusLine(item({ state: "disabled", enabled: false })).text).toBe("Switched off");
    expect(connectionStatusLine(item({ state: "unknown" })).text).toBe("Not used yet");
    expect(connectionStatusLine(item({ kind: "apikey", state: "unknown" })).text).toBe("Not checked yet");
  });

  it("colours the dot by state, grey when not set up", () => {
    expect(connectionDot(item()).color).toBe("var(--good)");
    expect(connectionDot(item({ state: "failing" })).color).toBe("var(--avoid)");
    expect(connectionDot(item({ state: "backing_off" })).color).toBe("var(--reject)");
    expect(connectionDot(item({ configured: false, state: "unknown" })).label).toBe("Not set up");
    // FlareSolverr missing while TorrentLeech needs it is amber, set up or not.
    expect(connectionDot(item({ configured: false, state: "warn" })).color).toBe("var(--avoid)");
  });

  it("sums up an indexer's day", () => {
    expect(countsLine({ queries_24h: 214, failures_24h: 3, grabs_24h: 3 })).toBe("214 searches · 3 grabs · 3 failed (24h)");
    expect(countsLine({ queries_24h: 1, failures_24h: 0, grabs_24h: 1 })).toBe("1 search · 1 grab (24h)");
    expect(countsLine({ queries_24h: 0, failures_24h: 0, grabs_24h: 0 })).toBeNull();
    expect(countsLine(undefined)).toBeNull();
  });

  it("offers Fix only for a switched-on connection in trouble", () => {
    expect(needsFix(item({ state: "failing" }))).toBe(true);
    expect(needsFix(item({ state: "failing", enabled: false }))).toBe(false);
    expect(needsFix(item())).toBe(false);
    expect(kindLabel(item({ type: "torznab" }))).toBe("torznab");
    expect(kindLabel(item({ kind: "apikey", type: undefined }))).toBe("api key");
  });
});
