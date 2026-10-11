// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AddMovieModal } from "./AddMovieModal";
import { api, type AppSettings, type Movie } from "../../lib/api";

// MOV-23: films already in the library link to themselves; any other opens a confirm step
// with the profile, minimum availability and a per-add search choice that leaves the
// shared default alone unless "Remember as my default" is ticked.

beforeEach(() => {
  vi.spyOn(api, "qualityProfiles").mockResolvedValue({ profiles: [{ key: "hd", name: "HD", is_default: true }], formats: [] } as unknown as Awaited<ReturnType<typeof api.qualityProfiles>>);
  vi.spyOn(api, "settings").mockResolvedValue({ search_on_add: true } as AppSettings);
  vi.spyOn(api, "lookupMovies").mockResolvedValue([
    { tmdb_id: 1, title: "Dune", year: 2021, overview: "", poster_url: "", vote_average: 8, in_library: true, library_id: 42 },
    { tmdb_id: 2, title: "Dune: Part Two", year: 2024, overview: "", poster_url: "", vote_average: 8 },
  ]);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const open = async () => {
  render(<MemoryRouter><AddMovieModal onClose={() => {}} onAdded={() => {}} /></MemoryRouter>);
  await act(async () => {});
  fireEvent.change(screen.getByLabelText("Search TMDB for a movie"), { target: { value: "dune" } });
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Search" })); });
};

describe("AddMovieModal", () => {
  it("links a film already in the library instead of adding it", async () => {
    const add = vi.spyOn(api, "addMovie");
    await open();
    const link = screen.getByRole("link", { name: /Dune.*In library/ });
    expect(link.getAttribute("href")).toBe("/movies/42");
    expect(add).not.toHaveBeenCalled();
  });

  it("adds with the chosen availability and search choice, without touching the default", async () => {
    const add = vi.spyOn(api, "addMovie").mockResolvedValue({} as Movie);
    const save = vi.spyOn(api, "updateSettings");
    await open();
    fireEvent.click(screen.getByRole("button", { name: /Dune: Part Two/ }));
    expect(add).not.toHaveBeenCalled(); // a click opens the confirm step, never adds at once
    fireEvent.change(screen.getByLabelText(/Minimum availability/), { target: { value: "inCinemas" } });
    fireEvent.click(screen.getByLabelText(/Add unmonitored/));
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Add movie" })); });
    expect(add).toHaveBeenCalledWith({ tmdb_id: 2, quality_profile: "hd", monitored: true, search_on_add: false, min_availability: "inCinemas" });
    expect(save).not.toHaveBeenCalled();
  });

  it("changes the shared default only when asked to", async () => {
    vi.spyOn(api, "addMovie").mockResolvedValue({} as Movie);
    const save = vi.spyOn(api, "updateSettings").mockResolvedValue({} as AppSettings);
    await open();
    fireEvent.click(screen.getByRole("button", { name: /Dune: Part Two/ }));
    fireEvent.click(screen.getByLabelText(/Add unmonitored/));
    fireEvent.click(screen.getByLabelText(/Remember as my default/));
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Add movie" })); });
    expect(save).toHaveBeenCalledWith({ search_on_add: false });
  });
});
