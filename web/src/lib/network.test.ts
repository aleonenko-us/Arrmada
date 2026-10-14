import { describe, expect, it } from "vitest";
import type { ActivityDownload } from "./api";
import { clientKindName, hasUsenetClient, isUsenetDownload, leftLine, NO_TORRENT_CLIENT, NO_USENET_CLIENT, noClientFor, usenetFailed, usenetFailLine } from "./network";

describe("usenet rows", () => {
  it("tells a usenet job by its transport, else by its sab- key", () => {
    expect(isUsenetDownload({ hash: "sab-sabnzbd_nzo_abc", transport: undefined })).toBe(true);
    expect(isUsenetDownload({ hash: "SAB-SABnzbd_nzo_abc" })).toBe(true);
    expect(isUsenetDownload({ hash: "e2e0000000000000000000000000000000000001" })).toBe(false);
    // The server's word wins over the key.
    expect(isUsenetDownload({ hash: "abc", transport: "usenet" })).toBe(true);
    expect(isUsenetDownload({ hash: "sab-x", transport: "torrent" })).toBe(false);
  });

  it("knows a usenet client from a torrent one", () => {
    expect(hasUsenetClient([{ kind: "qbittorrent" }])).toBe(false);
    expect(hasUsenetClient([{ kind: "qbittorrent" }, { kind: "sabnzbd" }])).toBe(true);
    expect(hasUsenetClient(undefined)).toBe(false);
    expect(clientKindName("sabnzbd")).toBe("SABnzbd");
    expect(clientKindName("qbittorrent")).toBe("qBittorrent");
  });
});

describe("releases with no client for their network", () => {
  it("can't be grabbed, and only those", () => {
    expect(noClientFor({ reject_reason: NO_USENET_CLIENT })).toBe(true);
    expect(noClientFor({ reject_reason: NO_TORRENT_CLIENT })).toBe(true);
    expect(noClientFor({ reject_reason: "Not in your book languages" })).toBe(false);
    expect(noClientFor({})).toBe(false);
  });
});

describe("a usenet job's lines", () => {
  const bytes = (n: number) => `${Math.round(n / 1e6)} MB`;
  const eta = (s: number) => `${Math.round(s / 60)}m`;
  const job = (over: Partial<ActivityDownload>) => ({ size_bytes: 1_000_000_000, progress: 0.25, eta_seconds: 240, ...over });

  it("says what's left and when, from SABnzbd's figure when it gives one", () => {
    expect(leftLine(job({}), bytes, eta)).toBe("750 MB left · ETA 4m");
    expect(leftLine(job({ remaining_bytes: 500_000_000 }), bytes, eta)).toBe("500 MB left · ETA 4m");
  });

  it("says why it failed in plain words", () => {
    expect(usenetFailed({ phase: "error", state: "error" })).toBe(true);
    expect(usenetFailed({ stage: "failed", state: "downloading" })).toBe(true);
    expect(usenetFailed({ phase: "downloading", state: "downloading" })).toBe(false);
    // The pipeline's generic words say less than SABnzbd's own reason.
    expect(usenetFailLine({ why: "The download client stopped it with an error", fail_message: "Out of retention" })).toBe("Out of retention");
    expect(usenetFailLine({ why: "Articles are missing: trying another release" , fail_message: "Aborted" })).toBe("Articles are missing: trying another release");
    expect(usenetFailLine({})).toBe("SABnzbd couldn't finish this download");
  });
});
