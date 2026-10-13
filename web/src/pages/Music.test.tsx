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

  it("offers the scan on an empty library", async () => {
    vi.spyOn(api, "artists").mockResolvedValue([]);
    const scan = vi.spyOn(api, "scanMusic").mockResolvedValue({ job_id: 0 } as Awaited<ReturnType<typeof api.scanMusic>>);
    page();
    expect(await screen.findByText(/catalogs what's in your music folder/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Scan library" })); });
    expect(scan).toHaveBeenCalledOnce();
  });
});
