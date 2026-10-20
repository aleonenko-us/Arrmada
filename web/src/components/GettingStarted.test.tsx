// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { clearQueryCache } from "../lib/query";
import { api, type Job } from "../lib/api";
import { setupApi, type SetupChecklist } from "../lib/setupApi";
import { ToastProvider } from "../ui";
import { GettingStarted } from "./GettingStarted";

const list = (over: Partial<SetupChecklist> = {}): SetupChecklist => ({
  items: [
    { key: "tmdb", label: "Add your TMDB key", done: true, link: "/settings/connections#api-keys", link_key: "apiKeys" },
    { key: "indexer", label: "Add an indexer", done: false, link: "/settings/connections#search", link_key: "indexers" },
  ],
  done: false,
  dismissed: false,
  ...over,
});

const page = () => render(<MemoryRouter><ToastProvider><GettingStarted /></ToastProvider></MemoryRouter>);

afterEach(() => {
  cleanup();
  clearQueryCache();
  vi.restoreAllMocks();
});

describe("Getting started card", () => {
  it("lists each step ticked or not, linking to where it's done", async () => {
    vi.spyOn(setupApi, "checklist").mockResolvedValue(list());
    page();
    const todo = await screen.findByRole("link", { name: /Add an indexer \(to do\)/ });
    expect(todo.getAttribute("href")).toBe("/settings/connections#search");
    expect(screen.getByRole("link", { name: /Add your TMDB key \(done\)/ })).toBeTruthy();
    expect(screen.getByText(/1 to go/)).toBeTruthy();
  });

  it("stays gone once everything is done", async () => {
    const get = vi.spyOn(setupApi, "checklist").mockResolvedValue(list({ done: true }));
    const { container } = page();
    await act(async () => { await get.mock.results[0]?.value; });
    expect(container.textContent).toBe("");
  });

  it("Dismiss hides it for good", async () => {
    vi.spyOn(setupApi, "checklist").mockResolvedValue(list());
    const dismiss = vi.spyOn(setupApi, "dismissChecklist").mockResolvedValue(list({ dismissed: true }));
    const { container } = page();
    fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    await act(async () => { await dismiss.mock.results[0]?.value; });
    expect(dismiss).toHaveBeenCalledOnce();
    expect(container.textContent).toBe("");
  });
});

describe("Import the library you already have", () => {
  const withImport = (): SetupChecklist => list({
    items: [
      { key: "import_library", label: "Import the library you already have", done: false, link: "", action: "import_library", detail: "84 folders on disk, 0 titles in Arrmada" },
    ],
  });

  it("starts both library scans from the card and reports what needs you, with why", async () => {
    vi.spyOn(setupApi, "checklist").mockResolvedValue(withImport());
    const tv = vi.spyOn(api, "scanSeries").mockResolvedValue({ status: "scanning", job_id: 7 });
    const movies = vi.spyOn(api, "scanLibrary").mockResolvedValue({ status: "scanning", job_id: 8 });
    vi.spyOn(api, "jobs").mockResolvedValue([]);
    const finished = (id: number, kind: string, imported: number, unmatched: number): Job => ({
      id, kind, target: "all", status: "succeeded", progress: 1, message: "done", result: { imported, unmatched },
    } as unknown as Job);
    vi.spyOn(api, "job").mockImplementation(async (id: number) => (id === 7 ? finished(7, "series.scan", 81, 2) : finished(8, "movie.scan", 0, 0)));
    vi.spyOn(api, "seriesUnmatched").mockResolvedValue([
      { folder: "Looney Tunes", title: "Looney Tunes", year: 0, candidates: [], reason: "no_episodes", detail: "Added as “Looney Tunes”, but none of its 2 video files matched an episode: its files are numbered by year.", series_id: 12 },
      { folder: "Obscure Show", title: "Obscure Show", year: 0, candidates: [], reason: "no_results", detail: "TMDB found nothing called “Obscure Show”." },
    ]);
    vi.spyOn(api, "moviesUnmatched").mockResolvedValue([]);
    page();

    expect(await screen.findByText(/84 folders on disk, 0 titles in Arrmada/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Import my library" }));
    expect(await screen.findByText("81 added, 2 need you", {}, { timeout: 5000 })).toBeTruthy();
    expect(tv).toHaveBeenCalledOnce();
    expect(movies).toHaveBeenCalledOnce();
    // Every folder says why, and offers its fix.
    expect(await screen.findByText(/numbered by year/)).toBeTruthy();
    expect(screen.getByRole("link", { name: /Open the show/ }).getAttribute("href")).toBe("/series/12");
    expect(screen.getByText(/TMDB found nothing called/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Open Add series" }).getAttribute("href")).toBe("/series?find=Obscure%20Show");
  });
});
