import { describe, expect, it } from "vitest";
import type { ActivityDownload } from "./api";
import { inClient, isStalled, queueClients, showClientNames, stalledMinutes } from "./queueFilter";

const NOW = 1_800_000_000; // unix seconds

describe("isStalled", () => {
  it("is the server's verdict, never the client's phase of the moment", () => {
    expect(isStalled({ stalled: true })).toBe(true);
    expect(isStalled({ stalled: false })).toBe(false);
    expect(isStalled({})).toBe(false);
    // qBittorrent says stalledDL whenever no peer has sent anything for a few seconds.
    const blip: Pick<ActivityDownload, "phase" | "stalled"> = { phase: "stalled" };
    expect(isStalled(blip)).toBe(false);
  });
});

describe("stalledMinutes", () => {
  it("is how long a stalled download has gone without progress", () => {
    expect(stalledMinutes({ stalled: true, stalled_since: NOW - 2 * 3600 }, NOW)).toBe(120);
  });
  it("is null for a download that isn't stalled, or an unknown or future time", () => {
    expect(stalledMinutes({ stalled_since: NOW - 3600 }, NOW)).toBeNull();
    expect(stalledMinutes({ stalled: true }, NOW)).toBeNull();
    expect(stalledMinutes({ stalled: true, stalled_since: NOW + 60 }, NOW)).toBeNull();
  });
});

describe("showClientNames", () => {
  it("names clients only when more than one is switched on", () => {
    expect(showClientNames({ configured: 2, enabled: 2, ok: true })).toBe(true);
    expect(showClientNames({ configured: 1, enabled: 1, ok: true })).toBe(false);
    // A switched-off client gets no downloads: every row would name the same one.
    expect(showClientNames({ configured: 2, enabled: 1, ok: true })).toBe(false);
    expect(showClientNames(null)).toBe(false);
  });
});

describe("queueClients and inClient", () => {
  const rows = [
    { hash: "a", client_id: 2, client_name: "qBittorrent 2" },
    { hash: "b", client_id: 1, client_name: "qBittorrent" },
    { hash: "c", client_id: 2, client_name: "qBittorrent 2" },
    { hash: "d" }, // an older server, or a client that wasn't named
    { hash: "e", client_id: 3 },
  ];
  it("lists each client the rows are in, by name, with its count", () => {
    expect(queueClients(rows)).toEqual([
      { id: 3, name: "Client 3", count: 1 },
      { id: 1, name: "qBittorrent", count: 1 },
      { id: 2, name: "qBittorrent 2", count: 2 },
    ]);
  });
  it("narrows to one client by the id the address holds", () => {
    expect(inClient(rows, "2").map((r) => r.hash)).toEqual(["a", "c"]);
    expect(inClient(rows, "")).toBe(rows);
    expect(inClient(rows, "nonsense")).toBe(rows);
    expect(inClient(rows, "9")).toEqual([]);
  });
});
