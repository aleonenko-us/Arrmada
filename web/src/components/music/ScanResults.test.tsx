// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ScanResultsModal } from "./ScanResults";
import { musicApi, type MusicScanSummary } from "../../lib/musicApi";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const summary: MusicScanSummary = {
  folders: 1200, skipped: 1150, artists: 2, albums: 40, tracks: 480, moved: 12, unmatched: ["/music/x"], reviews: 3,
  failed: 0, waiting: 0, remaining: 0, opened: 520, measured: 0, workers: 4, walk_ms: 850, read_ms: 4100, total_ms: 6200,
};

// MUS-37: the results drawer says what the scan found, what it skipped unopened, and
// links the folders to match in Reviews; lines with nothing to say are left out.
describe("ScanResultsModal", () => {
  it("shows the last scan's counts and the folders to match", async () => {
    vi.spyOn(musicApi, "lastScan").mockResolvedValue({ last: summary, finished_at: "2026-10-20T08:00:00Z" });
    const rescan = vi.fn();
    render(<MemoryRouter><ScanResultsModal onClose={() => {}} onRescan={rescan} /></MemoryRouter>);
    expect(await screen.findByText("Unchanged since the last scan")).toBeTruthy();
    expect(screen.getByText("1,150")).toBeTruthy();
    expect(screen.getByText("Files followed to a new folder")).toBeTruthy();
    expect(screen.queryByText("Couldn't be judged just now")).toBeNull();
    expect(screen.getByRole("link", { name: /Match the 3 folders in Reviews/ })).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByText("Scan everything")); });
    expect(rescan).toHaveBeenCalledWith(true);
  });

  it("says so before the first scan", async () => {
    vi.spyOn(musicApi, "lastScan").mockResolvedValue({ last: null });
    render(<MemoryRouter><ScanResultsModal onClose={() => {}} onRescan={() => {}} /></MemoryRouter>);
    expect(await screen.findByText("No scan has run yet.")).toBeTruthy();
  });
});
