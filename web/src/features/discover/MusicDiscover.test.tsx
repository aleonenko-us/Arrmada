// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { api } from "../../lib/api";
import { MusicDiscover } from "./MusicDiscover";

beforeEach(() => {
  vi.spyOn(api, "artists").mockResolvedValue([
    { id: 1, mbid: "owned-mbid", name: "Library Artist", monitored: true, quality_profile: "" },
  ]);
  vi.spyOn(api, "lookupArtists").mockResolvedValue([
    { mbid: "owned-mbid", name: "Library Artist" },
    { mbid: "new-mbid", name: "New Artist" },
  ]);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Music Discover library filter", () => {
  it("hides owned artists and the library shelf when hide_in_library is enabled", async () => {
    render(
      <MemoryRouter initialEntries={["/discover?tab=music&hide_in_library=1"]}>
        <MusicDiscover canRequest />
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByRole("textbox", { name: "Search artists" }), { target: { value: "Artist" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByRole("button", { name: "View details for New Artist" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "View details for Library Artist" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Your artists" })).toBeNull();
  });
});
