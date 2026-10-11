// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Downloads } from "./Downloads";
import { api, type ActivityFeed } from "../lib/api";
import { clearQueryCache } from "../lib/query";
import { resetLiveForTests } from "../lib/live";
import { ToastProvider } from "../ui";

// FE-13: a failed action on a download says so. act() used to leave it to the next poll,
// which only showed that nothing had changed.

const feed: ActivityFeed = {
  downloads: [{
    hash: "aaaa", name: "Driftwood.2025.2160p.WEB-DL", state: "downloading", progress: 0.42, size_bytes: 1e9,
    down_speed: 1e6, up_speed: 0, eta_seconds: 900, ratio: 0, quality_profile: "uhd", media_type: "movie",
  }],
  totals: { down_speed: 1e6, up_speed: 0, active: 1 },
  free_gb: 100,
  disk_path: "/downloads",
  clients: { configured: 1, enabled: 1, ok: true },
};

beforeEach(() => {
  clearQueryCache();
  resetLiveForTests();
  vi.stubGlobal("WebSocket", class { close() {} });
  vi.spyOn(api, "downloadClients").mockResolvedValue([]);
  vi.spyOn(api, "activity").mockResolvedValue(feed);
  vi.spyOn(api, "wanted").mockResolvedValue({ searching: [], upcoming: [] } as unknown as Awaited<ReturnType<typeof api.wanted>>);
});
afterEach(() => {
  cleanup();
  resetLiveForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("Downloads actions", () => {
  it("shows an error toast when pausing fails", async () => {
    vi.spyOn(api, "pauseDownload").mockRejectedValue(new Error("qBittorrent isn't answering"));
    render(<ToastProvider><MemoryRouter><Downloads /></MemoryRouter></ToastProvider>);
    const pause = await screen.findByRole("button", { name: "Pause" });
    await act(async () => { fireEvent.click(pause); });
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("qBittorrent isn't answering");
  });
});
