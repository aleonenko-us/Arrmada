// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Music } from "./Music";
import { api } from "../lib/api";
import { qualityApi } from "../lib/qualityApi";
import { ConfirmProvider, ToastProvider } from "../ui";

// COPY-14: Music says "No artists yet" only for a real empty answer, and then offers the
// scan; a failed load is an error with Retry.

beforeEach(() => {
  vi.spyOn(qualityApi, "qualityProfiles").mockResolvedValue({ profiles: [], formats: [] });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const page = () => render(<ToastProvider><ConfirmProvider><MemoryRouter><Music /></MemoryRouter></ConfirmProvider></ToastProvider>);

describe("Music list states", () => {
  it("shows an error with Retry, not 'No artists yet', when the load fails", async () => {
    const artists = vi.spyOn(api, "artists").mockRejectedValueOnce(new Error("backend unreachable"));
    page();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("No artists yet")).toBeNull();

    artists.mockResolvedValueOnce([]);
    await act(async () => { fireEvent.click(screen.getByText("Retry")); });
    expect(await screen.findByText("No artists yet")).toBeTruthy();
  });

  // MUS-29 (music-10): completeness is by album. A scanned artist with 1 of 20 albums
  // whose tracks all happen to be held (the other 19 not listed yet) used to read 12/12,
  // Complete; it now reads 1/20 albums and isn't under Complete.
  it("counts artists complete by their albums, not their tracks", async () => {
    vi.spyOn(api, "artists").mockResolvedValue([
      {
        id: 1, mbid: "a", name: "Scanned Act", monitored: false, quality_profile: "",
        stats: { albums: 20, tracks: 12, have_tracks: 12, size_bytes: 1, albums_monitored: 0, albums_complete: 1, albums_unlisted: 19, albums_with_files: 1, albums_wanted: 0 },
      },
      {
        id: 2, mbid: "b", name: "Done Band", monitored: true, quality_profile: "",
        stats: { albums: 2, tracks: 20, have_tracks: 20, size_bytes: 1, albums_monitored: 2, albums_complete: 2, albums_unlisted: 0, albums_with_files: 2, albums_wanted: 0 },
      },
      {
        id: 3, mbid: "c", name: "Half Done", monitored: true, quality_profile: "",
        stats: { albums: 3, tracks: 30, have_tracks: 25, size_bytes: 1, albums_monitored: 3, albums_complete: 2, albums_unlisted: 0, albums_with_files: 3, albums_wanted: 1 },
      },
    ]);
    page();
    expect(await screen.findByText("1/20 albums")).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: /^Complete/ })); });
    expect(screen.queryByText("Scanned Act")).toBeNull();
    expect(screen.queryByText("Half Done")).toBeNull();
    expect(screen.getByText("Done Band")).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: /^Wanted/ })); });
    expect(screen.getByText("Half Done")).toBeTruthy();
    expect(screen.queryByText("Done Band")).toBeNull();
  });

  it("offers the scan on an empty library", async () => {
    vi.spyOn(api, "artists").mockResolvedValue([]);
    const scan = vi.spyOn(api, "scanMusic").mockResolvedValue({ job_id: 0 } as Awaited<ReturnType<typeof api.scanMusic>>);
    page();
    expect(await screen.findByText(/catalogs what's in your music folder/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Scan library" })); });
    expect(scan).toHaveBeenCalledOnce();
  });
});
